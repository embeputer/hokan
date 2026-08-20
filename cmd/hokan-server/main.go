package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	api "github.com/hokan/hokan/internal/api/v1"
	"github.com/hokan/hokan/internal/auth"
	"github.com/hokan/hokan/internal/ci"
	"github.com/hokan/hokan/internal/config"
	"github.com/hokan/hokan/internal/git"
	"github.com/hokan/hokan/internal/migrate"
	"github.com/hokan/hokan/internal/store"
	"github.com/hokan/hokan/internal/store/sqlite"
	"github.com/hokan/hokan/internal/web"
	_ "modernc.org/sqlite"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg := config.Load()

	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		if err := runMigrate(cfg); err != nil {
			log.Error("migrate", "err", err)
			os.Exit(1)
		}
		log.Info("migrations applied", "db", cfg.DBPath)
		return
	}

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Error("data dir", "err", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		log.Error("db dir", "err", err)
		os.Exit(1)
	}

	if err := runMigrate(cfg); err != nil {
		log.Error("migrate", "err", err)
		os.Exit(1)
	}

	st, err := sqlite.Open(cfg.DBPath)
	if err != nil {
		log.Error("store", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	disk := &git.Disk{Root: cfg.DataDir}
	access := &auth.Access{Store: st}
	ciSvc := &ci.Service{Store: st, Disk: disk, Log: log}

	gitHTTP := &git.HTTP{Disk: disk, Access: access, Store: st, OnPush: ciSvc.OnPush}
	apiH := &api.Handler{
		Store:  st,
		Disk:   disk,
		Access: access,
		Config: cfg,
		OnPR:   ciSvc.OnPR,
		EnqueueCI: func(repo *store.Repo, sha string, pr *store.PullRequest) {
			ciSvc.Enqueue(context.Background(), repo, sha, pr, "push")
		},
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(auth.Middleware(access))
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if git.IsGitPath(req.URL.Path) {
				gitHTTP.ServeHTTP(w, req)
				return
			}
			next.ServeHTTP(w, req)
		})
	})
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	r.Mount("/api/v1", apiH.Router())
	webS := &web.Server{Store: st, Disk: disk, Access: access, Config: cfg, OnPR: ciSvc.OnPR}
	webS.Routes(r)

	sshSrv := &git.SSH{
		Addr: cfg.SSHAddr, HostKey: cfg.SSHHostKey, Disk: disk, Access: access, Store: st, OnPush: ciSvc.OnPush, Log: log,
	}
	go func() {
		if err := sshSrv.ListenAndServe(); err != nil {
			log.Error("ssh", "err", err)
		}
	}()

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: r}
	go func() {
		log.Info("http listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("http", "err", err)
			os.Exit(1)
		}
	}()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func sqliteDSN(path string) string {
	return "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
}

func runMigrate(cfg config.Config) error {
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		return err
	}
	sqlDB, err := sql.Open("sqlite", sqliteDSN(cfg.DBPath))
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	if _, err := sqlDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return err
	}
	return migrate.Up(sqlDB)
}
