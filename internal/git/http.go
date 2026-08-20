package git

import (
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"

	"github.com/hokan/hokan/internal/auth"
	"github.com/hokan/hokan/internal/store"
)

type HTTP struct {
	Disk   *Disk
	Access *auth.Access
	Store  store.Store
	OnPush func(owner, name, sha string)
}

func (h *HTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if !strings.Contains(path, ".git") {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	owner := parts[0]
	repoPart := parts[1]
	if !strings.HasSuffix(repoPart, ".git") {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimSuffix(repoPart, ".git")
	if !ValidName(owner) || !ValidName(name) {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()
	repo, err := h.Store.Repos().GetByOwnerName(ctx, store.OwnerUser, owner, name)
	if err != nil {
		repo, err = h.Store.Repos().GetByOwnerName(ctx, store.OwnerOrg, owner, name)
	}
	if err != nil {
		http.Error(w, "repository not found", http.StatusNotFound)
		return
	}

	user := h.Access.Authenticate(r)
	runnerRead := false
	if user == nil {
		if _, p, ok := r.BasicAuth(); ok {
			if _, err := h.Store.CI().GetRunnerByTokenHash(ctx, auth.HashSecret(p)); err == nil {
				runnerRead = true
			}
		}
	}
	writeOp := strings.Contains(path, "git-receive-pack") || r.URL.Query().Get("service") == "git-receive-pack"
	if writeOp {
		if user == nil || !h.Access.CanWrite(ctx, user, repo) {
			w.Header().Set("WWW-Authenticate", `Basic realm="hokan"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	} else if !runnerRead && !h.Access.CanRead(ctx, user, repo) {
		w.Header().Set("WWW-Authenticate", `Basic realm="hokan"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	gitDir := h.Disk.Path(owner, name)
	switch {
	case strings.HasSuffix(path, "/info/refs"):
		h.infoRefs(w, r, gitDir)
	case strings.HasSuffix(path, "/git-upload-pack"):
		h.serviceRPC(w, r, gitDir, "upload-pack")
	case strings.HasSuffix(path, "/git-receive-pack"):
		h.serviceRPC(w, r, gitDir, "receive-pack")
		if h.OnPush != nil {
			sha, err := Run(gitDir, "rev-parse", "HEAD")
			if err == nil && sha != "" && sha != "HEAD" {
				h.OnPush(owner, name, sha)
			}
		}
	default:
		http.NotFound(w, r)
	}
}

func (h *HTTP) infoRefs(w http.ResponseWriter, r *http.Request, gitDir string) {
	service := strings.TrimPrefix(r.URL.Query().Get("service"), "git-")
	if service != "upload-pack" && service != "receive-pack" {
		http.Error(w, "unsupported service", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", fmt.Sprintf("application/x-git-%s-advertisement", service))
	w.Header().Set("Cache-Control", "no-cache")
	if _, err := w.Write(pktLine("# service=git-" + service + "\n")); err != nil {
		return
	}
	if _, err := w.Write([]byte("0000")); err != nil {
		return
	}
	cmd := exec.Command("git", service, "--stateless-rpc", "--advertise-refs", gitDir)
	cmd.Stdout = w
	cmd.Stderr = io.Discard
	if proto := r.Header.Get("Git-Protocol"); proto != "" {
		cmd.Env = append(cmd.Environ(), "GIT_PROTOCOL="+proto)
	}
	_ = cmd.Run()
}

func (h *HTTP) serviceRPC(w http.ResponseWriter, r *http.Request, gitDir, service string) {
	w.Header().Set("Content-Type", fmt.Sprintf("application/x-git-%s-result", service))
	w.Header().Set("Cache-Control", "no-cache")
	cmd := exec.Command("git", service, "--stateless-rpc", gitDir)
	cmd.Stdin = r.Body
	cmd.Stdout = w
	cmd.Stderr = io.Discard
	if proto := r.Header.Get("Git-Protocol"); proto != "" {
		cmd.Env = append(cmd.Environ(), "GIT_PROTOCOL="+proto)
	}
	_ = cmd.Run()
}

func pktLine(s string) []byte {
	n := len(s) + 4
	return []byte(fmt.Sprintf("%04x%s", n, s))
}
