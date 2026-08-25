package web_test

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hokan/hokan/internal/auth"
	"github.com/hokan/hokan/internal/avatar"
	"github.com/hokan/hokan/internal/config"
	"github.com/hokan/hokan/internal/git"
	"github.com/hokan/hokan/internal/migrate"
	"github.com/hokan/hokan/internal/store"
	"github.com/hokan/hokan/internal/store/sqlite"
	"github.com/hokan/hokan/internal/web"
	_ "modernc.org/sqlite"
)

func startWebServer(t *testing.T) (string, *sqlite.DB) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "t.db")
	sqlDB, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := migrate.Up(sqlDB); err != nil {
		t.Fatal(err)
	}
	st, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	access := &auth.Access{Store: st}
	avatars := avatar.New(filepath.Join(dir, "avatars"), st.Users())
	r := chi.NewRouter()
	r.Use(auth.Middleware(access))
	s := &web.Server{
		Store:   st,
		Disk:    &git.Disk{Root: filepath.Join(dir, "repos")},
		Access:  access,
		Config:  config.Config{BaseURL: "http://example", AllowSignup: true},
		Avatars: avatars,
	}
	s.Routes(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv.URL, st
}

func getBody(t *testing.T, client *http.Client, rawURL string) (int, string) {
	t.Helper()
	res, err := client.Get(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, string(b)
}

func TestHomeLandingWhenLoggedOut(t *testing.T) {
	base, st := startWebServer(t)
	ctx := context.Background()
	hash, err := auth.HashPassword("password1")
	if err != nil {
		t.Fatal(err)
	}
	u := &store.User{ID: uuid.NewString(), Username: "alice", Email: "a@b.c", PasswordHash: hash, CreatedAt: time.Now().UTC()}
	if err := st.Users().Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := st.Repos().Create(ctx, &store.Repo{
		ID: uuid.NewString(), OwnerType: store.OwnerUser, OwnerID: u.ID, OwnerName: u.Username,
		Name: "public-app", IsPrivate: false, DefaultBranch: "main", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Repos().Create(ctx, &store.Repo{
		ID: uuid.NewString(), OwnerType: store.OwnerUser, OwnerID: u.ID, OwnerName: u.Username,
		Name: "secret-app", IsPrivate: true, DefaultBranch: "main", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	code, body := getBody(t, http.DefaultClient, base+"/")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if !strings.Contains(body, `<main class="landing-page">`) {
		t.Fatalf("logged-out home should use a full landing shell, got:\n%s", body)
	}
	if !strings.Contains(body, "Git hosting you run yourself") {
		t.Fatal("missing landing headline")
	}
	if strings.Contains(body, "Repositories — Hokan") {
		t.Fatal("logged-out home rendered the repo dashboard")
	}
	if !strings.Contains(body, "public-app") {
		t.Fatal("public repo missing from landing")
	}
	if strings.Contains(body, "secret-app") {
		t.Fatal("private repo leaked on landing")
	}
}

func TestHomeDashboardWhenLoggedIn(t *testing.T) {
	base, _ := startWebServer(t)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	res, err := client.PostForm(base+"/signup", url.Values{
		"username": {"bob"},
		"email":    {"b@b.c"},
		"password": {"password1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 && res.Request.URL.Path != "/" {
		t.Fatalf("signup redirect %s %s", res.Status, res.Request.URL)
	}
	code, body := getBody(t, client, base+"/")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if !strings.Contains(body, "Repositories — Hokan") {
		t.Fatalf("logged-in home should be the repo dashboard, got:\n%s", body)
	}
	if strings.Contains(body, `<main class="landing-page">`) {
		t.Fatal("logged-in home rendered the landing page")
	}
}

func TestLogoutClearsSessionCookie(t *testing.T) {
	base, _ := startWebServer(t)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	res, err := client.PostForm(base+"/signup", url.Values{
		"username": {"carol"},
		"email":    {"c@c.c"},
		"password": {"password1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	noFollow := &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	res, err = noFollow.Get(base + "/logout")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout status %d", res.StatusCode)
	}
	var cleared *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == auth.CookieName {
			cleared = c
			break
		}
	}
	if cleared == nil {
		t.Fatal("logout did not set session cookie")
	}
	if cleared.MaxAge >= 0 && cleared.Value != "" {
		t.Fatalf("session cookie not expired: value=%q maxAge=%d", cleared.Value, cleared.MaxAge)
	}
	if cleared.Path != "/" {
		t.Fatalf("cookie path %q", cleared.Path)
	}
	if cleared.SameSite != http.SameSiteLaxMode {
		t.Fatalf("logout cookie SameSite=%v, want Lax so browsers actually clear it", cleared.SameSite)
	}
	if !cleared.HttpOnly {
		t.Fatal("logout cookie should stay HttpOnly")
	}
}
