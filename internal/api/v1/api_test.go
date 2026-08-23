package api_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	api "github.com/hokan/hokan/internal/api/v1"
	"github.com/hokan/hokan/internal/auth"
	"github.com/hokan/hokan/internal/avatar"
	"github.com/hokan/hokan/internal/config"
	"github.com/hokan/hokan/internal/git"
	"github.com/hokan/hokan/internal/migrate"
	"github.com/hokan/hokan/internal/store/sqlite"
	_ "modernc.org/sqlite"
)

func startTestServer(t *testing.T) (string, *sqlite.DB, *git.Disk) {
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
	disk := &git.Disk{Root: filepath.Join(dir, "repos")}
	access := &auth.Access{Store: st}
	avatars := avatar.New(filepath.Join(dir, "avatars"), st.Users())
	h := &api.Handler{Store: st, Disk: disk, Access: access, Config: config.Config{BaseURL: "http://example", AllowSignup: true}, Avatars: avatars}
	gitHTTP := &git.HTTP{Disk: disk, Access: access, Store: st}
	r := chi.NewRouter()
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
	r.Get("/avatars/{username}", func(w http.ResponseWriter, req *http.Request) {
		avatars.Serve(w, req, chi.URLParam(req, "username"))
	})
	r.Mount("/api/v1", h.Router())
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv.URL, st, disk
}

func TestSignupRepoAndGitHTTP(t *testing.T) {
	base, _, _ := startTestServer(t)
	body, _ := json.Marshal(map[string]string{"username": "alice", "email": "a@b.c", "password": "password1"})
	res, err := http.Post(base+"/api/v1/auth/signup", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatalf("signup %s", res.Status)
	}
	var env struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", base+"/api/v1/repos", bytes.NewReader([]byte(`{"name":"app"}`)))
	req.Header.Set("Authorization", "Bearer "+env.Data.Token)
	req.Header.Set("Content-Type", "application/json")
	res2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.StatusCode != 201 {
		t.Fatalf("create repo %s", res2.Status)
	}

	work := t.TempDir()
	clone := filepath.Join(work, "app")
	url := strings.Replace(base, "http://", "http://alice:password1@", 1) + "/alice/app.git"
	cmd := exec.Command("git", "clone", url, clone)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = clone
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=A", "GIT_AUTHOR_EMAIL=a@b.c", "GIT_COMMITTER_NAME=A", "GIT_COMMITTER_EMAIL=a@b.c")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(clone, "README.md"), []byte("# hi\nhello searchterm\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")
	run("push", "origin", "HEAD:main")

	clone2 := filepath.Join(work, "app2")
	cmd = exec.Command("git", "clone", url, clone2)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("second clone: %v\n%s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(clone2, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("hello")) {
		t.Fatalf("content %s", b)
	}

	req, _ = http.NewRequest("GET", base+"/api/v1/repos/alice/app/search?q=searchterm", nil)
	req.Header.Set("Authorization", "Bearer "+env.Data.Token)
	res3, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res3.Body.Close()
	if res3.StatusCode != 200 {
		t.Fatalf("search %s", res3.Status)
	}
}

func TestPrivateRepoRequiresAuth(t *testing.T) {
	base, _, _ := startTestServer(t)
	body, _ := json.Marshal(map[string]string{"username": "bob", "email": "b@b.c", "password": "password1"})
	res, err := http.Post(base+"/api/v1/auth/signup", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	json.NewDecoder(res.Body).Decode(&env)
	res.Body.Close()
	req, _ := http.NewRequest("POST", base+"/api/v1/repos", bytes.NewReader([]byte(`{"name":"secret","private":true}`)))
	req.Header.Set("Authorization", "Bearer "+env.Data.Token)
	req.Header.Set("Content-Type", "application/json")
	res2, _ := http.DefaultClient.Do(req)
	res2.Body.Close()
	if res2.StatusCode != 201 {
		t.Fatalf("create %s", res2.Status)
	}
	cmd := exec.Command("git", "ls-remote", base+"/bob/secret.git")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if err := cmd.Run(); err == nil {
		t.Fatal("expected auth failure for private repo")
	}
}

func signup(t *testing.T, base, user, email string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": user, "email": email, "password": "password1"})
	res, err := http.Post(base+"/api/v1/auth/signup", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatalf("signup %s", res.Status)
	}
	var env struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	return env.Data.Token
}

func doJSON(t *testing.T, method, url, token string, body any, want int) *http.Response {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, url, rdr)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != want {
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		t.Fatalf("%s %s: got %s want %d: %s", method, url, res.Status, want, b)
	}
	return res
}

func TestPRMergeAndConflict(t *testing.T) {
	base, _, _ := startTestServer(t)
	token := signup(t, base, "carol", "c@c.c")
	res := doJSON(t, "POST", base+"/api/v1/repos", token, map[string]any{"name": "app"}, 201)
	res.Body.Close()

	url := strings.Replace(base, "http://", "http://carol:password1@", 1) + "/carol/app.git"
	work := filepath.Join(t.TempDir(), "w")
	cmd := exec.Command("git", "clone", url, work)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	gitw := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = work
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=C", "GIT_AUTHOR_EMAIL=c@c.c", "GIT_COMMITTER_NAME=C", "GIT_COMMITTER_EMAIL=c@c.c", "GIT_TERMINAL_PROMPT=0")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.WriteFile(filepath.Join(work, "a.txt"), []byte("base\n"), 0o644)
	gitw("add", ".")
	gitw("commit", "-m", "base")
	gitw("push", "origin", "HEAD:main")
	gitw("checkout", "-b", "ok")
	os.WriteFile(filepath.Join(work, "b.txt"), []byte("ok\n"), 0o644)
	gitw("add", ".")
	gitw("commit", "-m", "ok")
	gitw("push", "origin", "ok")

	res = doJSON(t, "POST", base+"/api/v1/repos/carol/app/pulls", token, map[string]any{
		"title": "ok", "source_branch": "ok", "target_branch": "main",
	}, 201)
	var prEnv struct {
		Data struct {
			Number int `json:"number"`
		} `json:"data"`
	}
	json.NewDecoder(res.Body).Decode(&prEnv)
	res.Body.Close()
	res = doJSON(t, "POST", base+"/api/v1/repos/carol/app/pulls/"+fmt.Sprint(prEnv.Data.Number)+"/merge", token, map[string]any{}, 200)
	res.Body.Close()

	gitw("checkout", "main")
	gitw("pull")
	gitw("checkout", "-b", "left")
	os.WriteFile(filepath.Join(work, "a.txt"), []byte("left\n"), 0o644)
	gitw("commit", "-am", "left")
	gitw("push", "-u", "origin", "left")
	gitw("checkout", "main")
	gitw("checkout", "-b", "right")
	os.WriteFile(filepath.Join(work, "a.txt"), []byte("right\n"), 0o644)
	gitw("commit", "-am", "right")
	gitw("push", "-u", "origin", "right")

	res = doJSON(t, "POST", base+"/api/v1/repos/carol/app/pulls", token, map[string]any{
		"title": "conflict", "source_branch": "right", "target_branch": "left",
	}, 201)
	json.NewDecoder(res.Body).Decode(&prEnv)
	res.Body.Close()
	res = doJSON(t, "POST", base+"/api/v1/repos/carol/app/pulls/"+fmt.Sprint(prEnv.Data.Number)+"/merge", token, map[string]any{}, 409)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !bytes.Contains(b, []byte("conflict")) {
		t.Fatalf("expected conflict error, got %s", b)
	}
}

func TestOrgReadOnlyCannotPush(t *testing.T) {
	base, _, _ := startTestServer(t)
	admin := signup(t, base, "orgadmin", "oa@c.c")
	_ = signup(t, base, "reader", "r@c.c")
	res := doJSON(t, "POST", base+"/api/v1/orgs", admin, map[string]string{"name": "acme"}, 201)
	res.Body.Close()
	res = doJSON(t, "POST", base+"/api/v1/orgs/acme/teams", admin, map[string]string{"name": "viewers", "permission": "read"}, 201)
	res.Body.Close()
	res = doJSON(t, "POST", base+"/api/v1/orgs/acme/teams/viewers/members", admin, map[string]string{"username": "reader"}, 201)
	res.Body.Close()
	res = doJSON(t, "POST", base+"/api/v1/repos", admin, map[string]any{"name": "lib", "owner": "acme", "owner_type": "org"}, 201)
	res.Body.Close()
	res = doJSON(t, "POST", base+"/api/v1/orgs/acme/repos/lib/permissions", admin, map[string]string{"team": "viewers", "level": "read"}, 200)
	res.Body.Close()

	url := strings.Replace(base, "http://", "http://reader:password1@", 1) + "/acme/lib.git"
	cmd := exec.Command("git", "ls-remote", url)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("read should work: %v\n%s", err, out)
	}
	work := filepath.Join(t.TempDir(), "w")
	cmd = exec.Command("git", "clone", url, work)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	os.WriteFile(filepath.Join(work, "x.txt"), []byte("x\n"), 0o644)
	c := exec.Command("git", "add", ".")
	c.Dir = work
	c.Run()
	c = exec.Command("git", "commit", "-m", "nope")
	c.Dir = work
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=R", "GIT_AUTHOR_EMAIL=r@c.c", "GIT_COMMITTER_NAME=R", "GIT_COMMITTER_EMAIL=r@c.c")
	c.Run()
	c = exec.Command("git", "push", "origin", "HEAD:main")
	c.Dir = work
	c.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if err := c.Run(); err == nil {
		t.Fatal("read-only member should not be able to push")
	}
}

func TestGetUserProfile(t *testing.T) {
	base, _, _ := startTestServer(t)
	token := signup(t, base, "alice", "a@b.c")

	res := doJSON(t, "GET", base+"/api/v1/users/alice", "", nil, 200)
	var anon struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&anon); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if anon.Data["username"] != "alice" {
		t.Fatalf("username: %v", anon.Data["username"])
	}
	if _, ok := anon.Data["email"]; ok {
		t.Fatal("public profile must omit email")
	}

	res = doJSON(t, "GET", base+"/api/v1/users/alice", token, nil, 200)
	var self struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&self); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if self.Data["email"] != "a@b.c" {
		t.Fatalf("self profile email: %v", self.Data["email"])
	}

	res = doJSON(t, "GET", base+"/api/v1/users/nobody", "", nil, 404)
	res.Body.Close()
}

func tinyPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func TestAvatarUploadAndDefault(t *testing.T) {
	base, _, _ := startTestServer(t)
	token := signup(t, base, "pixie", "p@p.p")

	res := doJSON(t, "GET", base+"/api/v1/users/pixie", "", nil, 200)
	var before struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&before); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if before.Data["has_avatar"] != false {
		t.Fatalf("has_avatar: %v", before.Data["has_avatar"])
	}
	url, _ := before.Data["avatar_url"].(string)
	if !strings.HasSuffix(url, "/avatars/pixie") {
		t.Fatalf("avatar_url %q", url)
	}

	imgRes, err := http.Get(base + "/avatars/pixie")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(imgRes.Body)
	imgRes.Body.Close()
	if imgRes.StatusCode != 200 {
		t.Fatalf("default avatar %s", imgRes.Status)
	}
	if !bytes.Contains(body, []byte("<svg")) {
		t.Fatal("expected default svg (blobatar or fallback)")
	}

	var mp bytes.Buffer
	w := multipart.NewWriter(&mp)
	fw, err := w.CreateFormFile("avatar", "me.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(tinyPNG()); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", base+"/api/v1/user/avatar", &mp)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", w.FormDataContentType())
	up, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	upBody, _ := io.ReadAll(up.Body)
	up.Body.Close()
	if up.StatusCode != 200 {
		t.Fatalf("upload %s: %s", up.Status, upBody)
	}

	res = doJSON(t, "GET", base+"/api/v1/user", token, nil, 200)
	var me struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&me); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if me.Data["has_avatar"] != true {
		t.Fatalf("has_avatar after upload: %v", me.Data["has_avatar"])
	}

	imgRes, err = http.Get(base + "/avatars/pixie")
	if err != nil {
		t.Fatal(err)
	}
	custom, _ := io.ReadAll(imgRes.Body)
	imgRes.Body.Close()
	if imgRes.StatusCode != 200 {
		t.Fatalf("custom avatar %s", imgRes.Status)
	}
	ct := imgRes.Header.Get("Content-Type")
	if !strings.Contains(ct, "png") {
		t.Fatalf("content-type %s", ct)
	}
	if bytes.Contains(custom, []byte("<svg")) {
		t.Fatal("custom avatar should not be svg")
	}

	del, err := http.NewRequest("DELETE", base+"/api/v1/user/avatar", nil)
	if err != nil {
		t.Fatal(err)
	}
	del.Header.Set("Authorization", "Bearer "+token)
	gone, err := http.DefaultClient.Do(del)
	if err != nil {
		t.Fatal(err)
	}
	goneBody, _ := io.ReadAll(gone.Body)
	gone.Body.Close()
	if gone.StatusCode != 200 {
		t.Fatalf("delete %s: %s", gone.Status, goneBody)
	}

	res = doJSON(t, "GET", base+"/api/v1/user", token, nil, 200)
	if err := json.NewDecoder(res.Body).Decode(&me); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if me.Data["has_avatar"] != false {
		t.Fatalf("has_avatar after delete: %v", me.Data["has_avatar"])
	}

	imgRes, err = http.Get(base + "/avatars/pixie")
	if err != nil {
		t.Fatal(err)
	}
	after, _ := io.ReadAll(imgRes.Body)
	imgRes.Body.Close()
	if imgRes.StatusCode != 200 {
		t.Fatalf("default after delete %s", imgRes.Status)
	}
	if !bytes.Contains(after, []byte("<svg")) {
		t.Fatal("expected svg after delete")
	}
}
