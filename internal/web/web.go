package web

import (
	"bytes"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hokan/hokan/internal/auth"
	"github.com/hokan/hokan/internal/avatar"
	"github.com/hokan/hokan/internal/config"
	"github.com/hokan/hokan/internal/git"
	"github.com/hokan/hokan/internal/store"
	"github.com/hokan/hokan/static"
	"github.com/hokan/hokan/templates"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
)

type Server struct {
	Store   store.Store
	Disk    *git.Disk
	Access  *auth.Access
	Config  config.Config
	Avatars *avatar.Service
	OnPR    func(repo *store.Repo, pr *store.PullRequest, sha string)
}

type page struct {
	User       *store.User
	Flash      string
	FlashType  string
	Repo       *store.Repo
	Repos      []store.Repo
	Keys       []store.SSHKey
	Tokens     []store.Token
	NewToken   string
	CloneHTTP  string
	CloneSSH   string
	Entries    []git.TreeEntry
	Parent     string
	PathPrefix string
	Ref        string
	Query      string
	ReadmeHTML template.HTML
	BlobHTML   template.HTML
	Commits    []git.Commit
	Branches   []string
	Hits       []git.SearchHit
	Pulls      []store.PullRequest
	PR         *store.PullRequest
	Diff       string
	DiffHTML   template.HTML
	Comments   []store.Comment
	MergeError string
	Issues     []store.Issue
	Issue      *store.Issue
	Jobs       []store.CIJob
	Job        *store.CIJob
	Orgs       []store.Org
	Org        *store.Org
	Teams      []store.Team
	Tab        string
	BlobPath   string
	Profile    *store.User
}

func (s *Server) Routes(r chi.Router) {
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(static.FS))))
	r.Get("/", s.home)
	r.Get("/signup", s.signupForm)
	r.Post("/signup", s.signup)
	r.Get("/login", s.loginForm)
	r.Post("/login", s.login)
	r.Get("/logout", s.logout)
	r.Get("/avatars/{username}", s.serveAvatar)
	r.Get("/settings/profile", s.profileSettings)
	r.Post("/settings/profile", s.uploadAvatar)
	r.Post("/settings/profile/delete", s.deleteAvatar)
	r.Get("/settings/keys", s.keys)
	r.Post("/settings/keys", s.addKey)
	r.Post("/settings/keys/{id}/delete", s.deleteKey)
	r.Get("/settings/tokens", s.tokens)
	r.Post("/settings/tokens", s.createToken)
	r.Post("/settings/tokens/{id}/delete", s.deleteToken)
	r.Get("/repos/new", s.repoNew)
	r.Post("/repos", s.repoCreate)
	r.Get("/orgs", s.orgs)
	r.Post("/orgs", s.orgCreate)
	r.Get("/orgs/{org}", s.orgView)
	r.Post("/orgs/{org}/teams", s.teamCreate)
	r.Post("/orgs/{org}/teams/{team}/members", s.teamAdd)

	r.Get("/{owner}/{name}", s.repoHome)
	r.Get("/{owner}/{name}/tree/{ref}/*", s.repoTree)
	r.Get("/{owner}/{name}/blob/{ref}/*", s.repoBlob)
	r.Get("/{owner}/{name}/commits", s.repoCommits)
	r.Get("/{owner}/{name}/branches", s.repoBranches)
	r.Get("/{owner}/{name}/search", s.repoSearch)
	r.Get("/{owner}/{name}/pulls", s.pulls)
	r.Get("/{owner}/{name}/pulls/new", s.pullNew)
	r.Post("/{owner}/{name}/pulls", s.pullCreate)
	r.Get("/{owner}/{name}/pulls/{number}", s.pullView)
	r.Post("/{owner}/{name}/pulls/{number}/comments", s.pullComment)
	r.Post("/{owner}/{name}/pulls/{number}/merge", s.pullMerge)
	r.Get("/{owner}/{name}/issues", s.issues)
	r.Get("/{owner}/{name}/issues/new", s.issueNew)
	r.Post("/{owner}/{name}/issues", s.issueCreate)
	r.Get("/{owner}/{name}/issues/{number}", s.issueView)
	r.Post("/{owner}/{name}/issues/{number}/comments", s.issueComment)
	r.Post("/{owner}/{name}/issues/{number}/close", s.issueClose)
	r.Post("/{owner}/{name}/issues/{number}/reopen", s.issueReopen)
	r.Get("/{owner}/{name}/ci", s.ciList)
	r.Get("/{owner}/{name}/ci/{id}/log", s.ciJobLog)
	r.Get("/{owner}/{name}/ci/{id}", s.ciJob)
	r.Get("/{owner}/{name}/tree/{ref}", s.repoTree)
	r.Get("/{username}", s.profile)
}

var tmplFuncs = template.FuncMap{
	"shortSHA": func(s string) string {
		if len(s) >= 7 {
			return s[:7]
		}
		return s
	},
	"avatarURL": func(name string) string {
		name = strings.TrimSpace(name)
		if name == "" {
			name = "_"
		}
		return "/avatars/" + url.PathEscape(name)
	},
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, pageName string, p page) {
	p.User = auth.UserFrom(r.Context())
	if c, err := r.Cookie("flash"); err == nil {
		p.Flash = c.Value
		p.FlashType = "ok"
		http.SetCookie(w, &http.Cookie{Name: "flash", Value: "", Path: "/", MaxAge: -1})
	}
	tmpl, err := template.New("").Funcs(tmplFuncs).ParseFS(templates.FS, "layout.html", "partials.html", pageName)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "layout.html", p); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

func (s *Server) withRepo(repo *store.Repo, tab string) page {
	p := page{Repo: repo, Tab: tab}
	p.CloneHTTP, p.CloneSSH = s.cloneURLs(repo)
	return p
}

func (s *Server) flash(w http.ResponseWriter, msg string) {
	http.SetCookie(w, &http.Cookie{Name: "flash", Value: msg, Path: "/", MaxAge: 60})
}

func (s *Server) setSession(w http.ResponseWriter, raw string) {
	http.SetCookie(w, &http.Cookie{
		Name: auth.CookieName, Value: raw, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(auth.SessionTTL.Seconds()),
	})
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	uid := ""
	if u := auth.UserFrom(r.Context()); u != nil {
		uid = u.ID
	}
	repos, err := s.Store.Repos().ListVisible(r.Context(), uid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, r, "home.html", page{Repos: repos})
}

func (s *Server) signupForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "signup.html", page{})
}

func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	username := strings.TrimSpace(r.FormValue("username"))
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	if !s.Config.AllowSignup {
		if n, _ := s.Store.Users().Count(r.Context()); n > 0 {
			http.Error(w, "signup disabled", 403)
			return
		}
	}
	hash, err := auth.HashPassword(password)
	if err != nil || !git.ValidName(username) || len(password) < 8 {
		http.Error(w, "invalid signup", 400)
		return
	}
	u := &store.User{ID: uuid.NewString(), Username: username, Email: email, PasswordHash: hash, CreatedAt: time.Now().UTC()}
	if err := s.Store.Users().Create(r.Context(), u); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	raw, hashTok, _ := auth.RandomToken("hoks_")
	_ = s.Store.Users().CreateSession(r.Context(), &store.Session{ID: uuid.NewString(), UserID: u.ID, TokenHash: hashTok, ExpiresAt: time.Now().UTC().Add(auth.SessionTTL)})
	s.setSession(w, raw)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "login.html", page{})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	u, err := s.Store.Users().GetByUsername(r.Context(), r.FormValue("username"))
	if err != nil || !auth.CheckPassword(u.PasswordHash, r.FormValue("password")) {
		http.Error(w, "invalid credentials", 401)
		return
	}
	raw, hashTok, _ := auth.RandomToken("hoks_")
	_ = s.Store.Users().CreateSession(r.Context(), &store.Session{ID: uuid.NewString(), UserID: u.ID, TokenHash: hashTok, ExpiresAt: time.Now().UTC().Add(auth.SessionTTL)})
	s.setSession(w, raw)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: auth.CookieName, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) serveAvatar(w http.ResponseWriter, r *http.Request) {
	if s.Avatars == nil {
		http.NotFound(w, r)
		return
	}
	s.Avatars.Serve(w, r, chi.URLParam(r, "username"))
}

func (s *Server) requireUser(w http.ResponseWriter, r *http.Request) *store.User {
	u := auth.UserFrom(r.Context())
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
	return u
}

func (s *Server) profileSettings(w http.ResponseWriter, r *http.Request) {
	if s.requireUser(w, r) == nil {
		return
	}
	s.render(w, r, "settings_profile.html", page{Tab: "profile"})
}

func (s *Server) uploadAvatar(w http.ResponseWriter, r *http.Request) {
	u := s.requireUser(w, r)
	if u == nil {
		return
	}
	if s.Avatars == nil {
		http.Error(w, "avatars unavailable", 500)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, avatar.MaxBytes+4096)
	if err := r.ParseMultipartForm(avatar.MaxBytes); err != nil {
		if avatar.TooLarge(err) {
			http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid upload", 400)
		return
	}
	f, _, err := r.FormFile("avatar")
	if err != nil {
		http.Error(w, "choose an image to upload", 400)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, avatar.MaxBytes+1))
	if err != nil {
		http.Error(w, "could not read file", 400)
		return
	}
	if err := s.Avatars.AttachCustom(r.Context(), s.Store.Users(), u.ID, data); err != nil {
		code := 500
		if avatar.IsInput(err) {
			code = 400
		}
		http.Error(w, err.Error(), code)
		return
	}
	s.flash(w, "Avatar updated")
	http.Redirect(w, r, "/settings/profile", http.StatusSeeOther)
}

func (s *Server) deleteAvatar(w http.ResponseWriter, r *http.Request) {
	u := s.requireUser(w, r)
	if u == nil {
		return
	}
	if s.Avatars == nil {
		http.Error(w, "avatars unavailable", 500)
		return
	}
	if err := s.Avatars.DetachCustom(r.Context(), s.Store.Users(), u.ID); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.flash(w, "Avatar removed")
	http.Redirect(w, r, "/settings/profile", http.StatusSeeOther)
}

func (s *Server) keys(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	keys, _ := s.Store.Users().ListSSHKeys(r.Context(), u.ID)
	s.render(w, r, "keys.html", page{Keys: keys, Tab: "keys"})
}

func (s *Server) addKey(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	fp, canonical, err := auth.Fingerprint(r.FormValue("key"))
	if err != nil {
		http.Error(w, "invalid key", 400)
		return
	}
	_ = s.Store.Users().CreateSSHKey(r.Context(), &store.SSHKey{
		ID: uuid.NewString(), UserID: u.ID, Name: r.FormValue("name"), PublicKey: canonical, Fingerprint: fp, CreatedAt: time.Now().UTC(),
	})
	http.Redirect(w, r, "/settings/keys", http.StatusSeeOther)
}

func (s *Server) deleteKey(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	_ = s.Store.Users().DeleteSSHKey(r.Context(), u.ID, chi.URLParam(r, "id"))
	http.Redirect(w, r, "/settings/keys", http.StatusSeeOther)
}

func (s *Server) tokens(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	toks, _ := s.Store.Users().ListTokens(r.Context(), u.ID)
	s.render(w, r, "tokens.html", page{Tokens: toks, NewToken: r.URL.Query().Get("new"), Tab: "tokens"})
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	raw, hash, _ := auth.RandomToken("hok_")
	_ = s.Store.Users().CreateToken(r.Context(), &store.Token{
		ID: uuid.NewString(), UserID: u.ID, Name: r.FormValue("name"), TokenHash: hash, CreatedAt: time.Now().UTC(),
	})
	http.Redirect(w, r, "/settings/tokens?new="+raw, http.StatusSeeOther)
}

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	_ = s.Store.Users().DeleteToken(r.Context(), u.ID, chi.URLParam(r, "id"))
	http.Redirect(w, r, "/settings/tokens", http.StatusSeeOther)
}

func (s *Server) repoNew(w http.ResponseWriter, r *http.Request) {
	if auth.UserFrom(r.Context()) == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, r, "repo_new.html", page{})
}

func (s *Server) repoCreate(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	name := r.FormValue("name")
	if !git.ValidName(name) {
		http.Error(w, "invalid name", 400)
		return
	}
	repo := &store.Repo{
		ID: uuid.NewString(), OwnerType: store.OwnerUser, OwnerID: u.ID, OwnerName: u.Username,
		Name: name, IsPrivate: r.FormValue("private") == "1", DefaultBranch: "main", CreatedAt: time.Now().UTC(),
	}
	if err := s.Store.Repos().Create(r.Context(), repo); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err := s.Disk.CreateRepo(u.Username, name); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/"+u.Username+"/"+name, http.StatusSeeOther)
}

func (s *Server) loadRepo(r *http.Request) (*store.Repo, error) {
	owner, name := chi.URLParam(r, "owner"), chi.URLParam(r, "name")
	repo, err := s.Store.Repos().GetByOwnerName(r.Context(), store.OwnerUser, owner, name)
	if err != nil {
		repo, err = s.Store.Repos().GetByOwnerName(r.Context(), store.OwnerOrg, owner, name)
	}
	if err != nil {
		return nil, err
	}
	if !s.Access.CanRead(r.Context(), auth.UserFrom(r.Context()), repo) {
		return nil, store.ErrNotFound
	}
	return repo, nil
}

func (s *Server) cloneURLs(repo *store.Repo) (httpURL, sshURL string) {
	base := strings.TrimRight(s.Config.BaseURL, "/")
	httpURL = base + "/" + repo.FullName() + ".git"
	host := "localhost"
	if u, err := url.Parse(base); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	port := "2222"
	sshAddr := s.Config.SSHAddr
	if strings.HasPrefix(sshAddr, ":") {
		port = strings.TrimPrefix(sshAddr, ":")
	} else if h, p, err := net.SplitHostPort(sshAddr); err == nil {
		if h != "" && h != "0.0.0.0" && h != "127.0.0.1" && h != "::" && h != "[::]" {
			host = h
		}
		port = p
	}
	if port == "" || port == "22" {
		sshURL = "ssh://git@" + host + "/" + repo.FullName() + ".git"
	} else {
		sshURL = "ssh://git@" + host + ":" + port + "/" + repo.FullName() + ".git"
	}
	return
}

func (s *Server) repoHome(w http.ResponseWriter, r *http.Request) {
	repo, err := s.loadRepo(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	dir := s.Disk.Path(repo.OwnerName, repo.Name)
	ref := git.DefaultRef(dir)
	entries, _ := git.Tree(dir, ref, "")
	p := s.withRepo(repo, "code")
	p.Entries = entries
	p.Ref = ref
	if _, md, ok := git.README(dir, ref); ok {
		p.ReadmeHTML = renderMarkdown(md)
	}
	s.render(w, r, "repo.html", p)
}

func (s *Server) repoTree(w http.ResponseWriter, r *http.Request) {
	repo, err := s.loadRepo(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ref := chi.URLParam(r, "ref")
	path := strings.TrimPrefix(chi.URLParam(r, "*"), "/")
	dir := s.Disk.Path(repo.OwnerName, repo.Name)
	entries, _ := git.Tree(dir, ref, path)
	prefix := path
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	p := s.withRepo(repo, "code")
	p.Entries = entries
	p.Ref = ref
	p.Parent = git.ParentPath(path)
	p.PathPrefix = prefix
	s.render(w, r, "repo.html", p)
}

func (s *Server) repoBlob(w http.ResponseWriter, r *http.Request) {
	repo, err := s.loadRepo(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ref := chi.URLParam(r, "ref")
	filePath := strings.TrimPrefix(chi.URLParam(r, "*"), "/")
	content, err := git.Blob(s.Disk.Path(repo.OwnerName, repo.Name), ref, filePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var htmlContent template.HTML
	if strings.HasSuffix(strings.ToLower(filePath), ".md") {
		htmlContent = renderMarkdown(content)
	} else {
		htmlContent = highlight(filePath, content)
	}
	p := s.withRepo(repo, "code")
	p.Ref = ref
	p.BlobHTML = htmlContent
	p.BlobPath = filePath
	s.render(w, r, "blob.html", p)
}

func (s *Server) repoCommits(w http.ResponseWriter, r *http.Request) {
	repo, err := s.loadRepo(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	commits, _ := git.Log(s.Disk.Path(repo.OwnerName, repo.Name), "", 50)
	p := s.withRepo(repo, "commits")
	p.Commits = commits
	s.render(w, r, "commits.html", p)
}

func (s *Server) repoBranches(w http.ResponseWriter, r *http.Request) {
	repo, err := s.loadRepo(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	branches, _ := git.ListBranches(s.Disk.Path(repo.OwnerName, repo.Name))
	p := s.withRepo(repo, "branches")
	p.Branches = branches
	s.render(w, r, "branches.html", p)
}

func (s *Server) repoSearch(w http.ResponseWriter, r *http.Request) {
	repo, err := s.loadRepo(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query().Get("q")
	hits, _ := git.Search(s.Disk.Path(repo.OwnerName, repo.Name), "", q)
	p := s.withRepo(repo, "code")
	p.Hits = hits
	p.Query = q
	p.Ref = git.DefaultRef(s.Disk.Path(repo.OwnerName, repo.Name))
	s.render(w, r, "search.html", p)
}

func (s *Server) pulls(w http.ResponseWriter, r *http.Request) {
	repo, err := s.loadRepo(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	prs, _ := s.Store.PullRequests().List(r.Context(), repo.ID)
	p := s.withRepo(repo, "pulls")
	p.Pulls = prs
	s.render(w, r, "pulls.html", p)
}

func (s *Server) pullNew(w http.ResponseWriter, r *http.Request) {
	repo, err := s.loadRepo(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	branches, _ := git.ListBranches(s.Disk.Path(repo.OwnerName, repo.Name))
	p := s.withRepo(repo, "pulls")
	p.Branches = branches
	s.render(w, r, "pull_new.html", p)
}

func (s *Server) pullCreate(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	repo, err := s.loadRepo(r)
	if err != nil || u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	n, err := s.Store.Repos().NextNumber(r.Context(), repo.ID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	pr := &store.PullRequest{
		ID: uuid.NewString(), RepoID: repo.ID, Number: n, Title: r.FormValue("title"), Description: r.FormValue("description"),
		SourceBranch: r.FormValue("source_branch"), TargetBranch: r.FormValue("target_branch"),
		AuthorID: u.ID, AuthorName: u.Username, State: store.PROpen, CreatedAt: time.Now().UTC(),
	}
	if err := s.Store.PullRequests().Create(r.Context(), pr); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if s.OnPR != nil {
		sha, _ := git.RevParse(s.Disk.Path(repo.OwnerName, repo.Name), pr.SourceBranch)
		s.OnPR(repo, pr, sha)
	}
	http.Redirect(w, r, "/"+repo.FullName()+"/pulls/"+strconv.Itoa(n), http.StatusSeeOther)
}

func (s *Server) pullView(w http.ResponseWriter, r *http.Request) {
	repo, err := s.loadRepo(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	n, _ := strconv.Atoi(chi.URLParam(r, "number"))
	pr, err := s.Store.PullRequests().Get(r.Context(), repo.ID, n)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	diff, _ := git.Diff(s.Disk.Path(repo.OwnerName, repo.Name), pr.TargetBranch, pr.SourceBranch)
	comments, _ := s.Store.PullRequests().ListComments(r.Context(), pr.ID)
	p := s.withRepo(repo, "pulls")
	p.PR = pr
	p.DiffHTML = renderDiff(diff)
	p.Comments = comments
	p.MergeError = r.URL.Query().Get("error")
	s.render(w, r, "pull.html", p)
}

func (s *Server) pullComment(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	repo, err := s.loadRepo(r)
	if err != nil || u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	n, _ := strconv.Atoi(chi.URLParam(r, "number"))
	pr, err := s.Store.PullRequests().Get(r.Context(), repo.ID, n)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	var line *int
	if v := r.FormValue("line"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			line = &n
		}
	}
	_ = s.Store.PullRequests().CreateComment(r.Context(), &store.Comment{
		ID: uuid.NewString(), ParentID: pr.ID, AuthorID: u.ID, AuthorName: u.Username,
		Body: r.FormValue("body"), FilePath: r.FormValue("file_path"), Line: line, CreatedAt: time.Now().UTC(),
	})
	http.Redirect(w, r, "/"+repo.FullName()+"/pulls/"+strconv.Itoa(n), http.StatusSeeOther)
}

func (s *Server) pullMerge(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	repo, err := s.loadRepo(r)
	if err != nil || u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !s.Access.CanWrite(r.Context(), u, repo) {
		http.Error(w, "forbidden", 403)
		return
	}
	n, _ := strconv.Atoi(chi.URLParam(r, "number"))
	pr, err := s.Store.PullRequests().Get(r.Context(), repo.ID, n)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if repo.RequireCIPass {
		sha, _ := git.RevParse(s.Disk.Path(repo.OwnerName, repo.Name), pr.SourceBranch)
		jobs, _ := s.Store.CI().LatestForSHA(r.Context(), repo.ID, sha)
		ok := len(jobs) > 0
		for _, j := range jobs {
			if j.Status != store.CISuccess {
				ok = false
			}
		}
		if !ok {
			http.Redirect(w, r, "/"+repo.FullName()+"/pulls/"+strconv.Itoa(n)+"?error=CI+must+pass+before+merge", http.StatusSeeOther)
			return
		}
	}
	sha, err := git.MergeCommit(s.Disk.Path(repo.OwnerName, repo.Name), pr.SourceBranch, pr.TargetBranch, "Merge pull request #"+strconv.Itoa(n), u.Username, u.Email)
	if err != nil {
		http.Redirect(w, r, "/"+repo.FullName()+"/pulls/"+strconv.Itoa(n)+"?error="+err.Error(), http.StatusSeeOther)
		return
	}
	_ = s.Store.PullRequests().SetState(r.Context(), pr.ID, store.PRMerged, sha)
	http.Redirect(w, r, "/"+repo.FullName()+"/pulls/"+strconv.Itoa(n), http.StatusSeeOther)
}

func (s *Server) issues(w http.ResponseWriter, r *http.Request) {
	repo, err := s.loadRepo(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	issues, _ := s.Store.Issues().List(r.Context(), repo.ID)
	p := s.withRepo(repo, "issues")
	p.Issues = issues
	s.render(w, r, "issues.html", p)
}

func (s *Server) issueNew(w http.ResponseWriter, r *http.Request) {
	repo, err := s.loadRepo(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, r, "issue_new.html", s.withRepo(repo, "issues"))
}

func (s *Server) issueCreate(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	repo, err := s.loadRepo(r)
	if err != nil || u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	n, err := s.Store.Repos().NextNumber(r.Context(), repo.ID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	issue := &store.Issue{
		ID: uuid.NewString(), RepoID: repo.ID, Number: n, Title: r.FormValue("title"), Description: r.FormValue("description"),
		AuthorID: u.ID, AuthorName: u.Username, State: store.IssueOpen, CreatedAt: time.Now().UTC(),
	}
	if err := s.Store.Issues().Create(r.Context(), issue); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/"+repo.FullName()+"/issues/"+strconv.Itoa(n), http.StatusSeeOther)
}

func (s *Server) issueView(w http.ResponseWriter, r *http.Request) {
	repo, err := s.loadRepo(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	n, _ := strconv.Atoi(chi.URLParam(r, "number"))
	issue, err := s.Store.Issues().Get(r.Context(), repo.ID, n)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	comments, _ := s.Store.Issues().ListComments(r.Context(), issue.ID)
	p := s.withRepo(repo, "issues")
	p.Issue = issue
	p.Comments = comments
	s.render(w, r, "issue.html", p)
}

func (s *Server) issueComment(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	repo, err := s.loadRepo(r)
	if err != nil || u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	n, _ := strconv.Atoi(chi.URLParam(r, "number"))
	issue, err := s.Store.Issues().Get(r.Context(), repo.ID, n)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	_ = s.Store.Issues().CreateComment(r.Context(), &store.Comment{
		ID: uuid.NewString(), ParentID: issue.ID, AuthorID: u.ID, AuthorName: u.Username, Body: r.FormValue("body"), CreatedAt: time.Now().UTC(),
	})
	http.Redirect(w, r, "/"+repo.FullName()+"/issues/"+strconv.Itoa(n), http.StatusSeeOther)
}

func (s *Server) issueClose(w http.ResponseWriter, r *http.Request) {
	s.setIssue(w, r, store.IssueClosed)
}

func (s *Server) issueReopen(w http.ResponseWriter, r *http.Request) {
	s.setIssue(w, r, store.IssueOpen)
}

func (s *Server) setIssue(w http.ResponseWriter, r *http.Request, st store.IssueState) {
	repo, err := s.loadRepo(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	n, _ := strconv.Atoi(chi.URLParam(r, "number"))
	issue, err := s.Store.Issues().Get(r.Context(), repo.ID, n)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = s.Store.Issues().SetState(r.Context(), issue.ID, st)
	http.Redirect(w, r, "/"+repo.FullName()+"/issues/"+strconv.Itoa(n), http.StatusSeeOther)
}

func (s *Server) ciList(w http.ResponseWriter, r *http.Request) {
	repo, err := s.loadRepo(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	jobs, _ := s.Store.CI().ListJobs(r.Context(), repo.ID)
	p := s.withRepo(repo, "ci")
	p.Jobs = jobs
	s.render(w, r, "ci.html", p)
}

func (s *Server) ciJob(w http.ResponseWriter, r *http.Request) {
	repo, err := s.loadRepo(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	job, err := s.Store.CI().GetJob(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p := s.withRepo(repo, "ci")
	p.Job = job
	s.render(w, r, "ci_job.html", p)
}

func (s *Server) ciJobLog(w http.ResponseWriter, r *http.Request) {
	repo, err := s.loadRepo(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	job, err := s.Store.CI().GetJob(r.Context(), chi.URLParam(r, "id"))
	if err != nil || job.RepoID != repo.ID {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	template.HTMLEscape(w, []byte(job.LogText))
}

func (s *Server) orgs(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	orgs, _ := s.Store.Orgs().ListForUser(r.Context(), u.ID)
	s.render(w, r, "orgs.html", page{Orgs: orgs})
}

func (s *Server) orgCreate(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	o := &store.Org{ID: uuid.NewString(), Name: r.FormValue("name"), CreatorUserID: u.ID, CreatedAt: time.Now().UTC()}
	if err := s.Store.Orgs().Create(r.Context(), o); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/orgs/"+o.Name, http.StatusSeeOther)
}

func (s *Server) orgView(w http.ResponseWriter, r *http.Request) {
	o, err := s.Store.Orgs().GetByName(r.Context(), chi.URLParam(r, "org"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	teams, _ := s.Store.Orgs().ListTeams(r.Context(), o.ID)
	s.render(w, r, "org.html", page{Org: o, Teams: teams})
}

func (s *Server) teamCreate(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	o, err := s.Store.Orgs().GetByName(r.Context(), chi.URLParam(r, "org"))
	if err != nil || u == nil || o.CreatorUserID != u.ID {
		http.Error(w, "forbidden", 403)
		return
	}
	_ = r.ParseForm()
	_ = s.Store.Orgs().CreateTeam(r.Context(), &store.Team{
		ID: uuid.NewString(), OrgID: o.ID, Name: r.FormValue("name"), PermissionLevel: store.Permission(r.FormValue("permission")), CreatedAt: time.Now().UTC(),
	})
	http.Redirect(w, r, "/orgs/"+o.Name, http.StatusSeeOther)
}

func (s *Server) teamAdd(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	o, err := s.Store.Orgs().GetByName(r.Context(), chi.URLParam(r, "org"))
	if err != nil || u == nil || o.CreatorUserID != u.ID {
		http.Error(w, "forbidden", 403)
		return
	}
	_ = r.ParseForm()
	teams, _ := s.Store.Orgs().ListTeams(r.Context(), o.ID)
	var teamID string
	for _, t := range teams {
		if t.Name == chi.URLParam(r, "team") {
			teamID = t.ID
		}
	}
	member, err := s.Store.Users().GetByUsername(r.Context(), r.FormValue("username"))
	if err != nil {
		http.Error(w, "user not found", 404)
		return
	}
	_ = s.Store.Orgs().AddTeamMember(r.Context(), teamID, member.ID)
	http.Redirect(w, r, "/orgs/"+o.Name, http.StatusSeeOther)
}

func (s *Server) profile(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "username")
	u, err := s.Store.Users().GetByUsername(r.Context(), name)
	if err != nil {
		if o, oerr := s.Store.Orgs().GetByName(r.Context(), name); oerr == nil {
			http.Redirect(w, r, "/orgs/"+o.Name, http.StatusSeeOther)
			return
		}
		http.NotFound(w, r)
		return
	}
	owned, _ := s.Store.Repos().ListByOwner(r.Context(), store.OwnerUser, u.ID)
	viewer := auth.UserFrom(r.Context())
	var repos []store.Repo
	for i := range owned {
		if s.Access.CanRead(r.Context(), viewer, &owned[i]) {
			repos = append(repos, owned[i])
		}
	}
	orgs, _ := s.Store.Orgs().ListForUser(r.Context(), u.ID)
	s.render(w, r, "profile.html", page{Profile: u, Repos: repos, Orgs: orgs})
}

func renderDiff(diff string) template.HTML {
	if strings.TrimSpace(diff) == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="diff">`)
	for _, line := range strings.Split(diff, "\n") {
		class := "diff-line"
		switch {
		case strings.HasPrefix(line, "diff ") || strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			class += " diff-file"
		case strings.HasPrefix(line, "@@"):
			class += " diff-hunk"
		case strings.HasPrefix(line, "+"):
			class += " diff-add"
		case strings.HasPrefix(line, "-"):
			class += " diff-del"
		}
		b.WriteString(`<div class="` + class + `">`)
		if line == "" {
			b.WriteString(" ")
		} else {
			b.WriteString(template.HTMLEscapeString(line))
		}
		b.WriteString("</div>")
	}
	b.WriteString("</div>")
	return template.HTML(b.String())
}

func renderMarkdown(src string) template.HTML {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		return template.HTML("<pre>" + template.HTMLEscapeString(src) + "</pre>")
	}
	return template.HTML(buf.String())
}

func highlight(filename, src string) template.HTML {
	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	iterator, err := lexer.Tokenise(nil, src)
	if err != nil {
		return template.HTML("<pre>" + template.HTMLEscapeString(src) + "</pre>")
	}
	formatter := html.New(html.WithClasses(false), html.TabWidth(4))
	var buf bytes.Buffer
	style := styles.Get("github-dark")
	if style == nil {
		style = styles.Fallback
	}
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return template.HTML("<pre>" + template.HTMLEscapeString(src) + "</pre>")
	}
	return template.HTML(buf.String())
}
