package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/hokan/hokan/internal/auth"
	"github.com/hokan/hokan/internal/avatar"
	"github.com/hokan/hokan/internal/config"
	"github.com/hokan/hokan/internal/git"
	"github.com/hokan/hokan/internal/store"
)

type Handler struct {
	Store     store.Store
	Disk      *git.Disk
	Access    *auth.Access
	Config    config.Config
	Avatars   *avatar.Service
	OnPR      func(repo *store.Repo, pr *store.PullRequest, sha string)
	EnqueueCI func(repo *store.Repo, sha string, pr *store.PullRequest)
}

func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Post("/auth/signup", h.signup)
	r.Post("/auth/login", h.login)
	r.Get("/users/{username}", h.getUser)

	r.Group(func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return auth.RequireUser(next)
		})
		r.Get("/user", h.currentUser)
		r.Post("/user/avatar", h.uploadAvatar)
		r.Delete("/user/avatar", h.deleteAvatar)
		r.Get("/user/keys", h.listKeys)
		r.Post("/user/keys", h.addKey)
		r.Delete("/user/keys/{id}", h.deleteKey)
		r.Get("/user/tokens", h.listTokens)
		r.Post("/user/tokens", h.createToken)
		r.Delete("/user/tokens/{id}", h.deleteToken)

		r.Get("/repos", h.listRepos)
		r.Post("/repos", h.createRepo)

		r.Get("/orgs", h.listOrgs)
		r.Post("/orgs", h.createOrg)
		r.Get("/orgs/{org}", h.getOrg)
		r.Post("/orgs/{org}/teams", h.createTeam)
		r.Get("/orgs/{org}/teams", h.listTeams)
		r.Post("/orgs/{org}/teams/{team}/members", h.addTeamMember)
		r.Post("/orgs/{org}/repos/{name}/permissions", h.setRepoPermission)

		r.Post("/ci/runners", h.createRunner)
	})

	r.Get("/ci/jobs/wait", h.waitJob)
	r.Post("/ci/jobs/{id}/logs", h.appendJobLog)
	r.Post("/ci/jobs/{id}/finish", h.finishJob)

	r.Route("/repos/{owner}/{name}", func(r chi.Router) {
		r.Get("/", h.getRepo)
		r.Delete("/", h.requireUser(h.deleteRepo))
		r.Get("/branches", h.repoBranches)
		r.Get("/commits", h.repoCommits)
		r.Get("/tree", h.repoTree)
		r.Get("/blob", h.repoBlob)
		r.Get("/search", h.repoSearch)

		r.Get("/pulls", h.listPRs)
		r.Post("/pulls", h.requireUser(h.createPR))
		r.Get("/pulls/{number}", h.getPR)
		r.Post("/pulls/{number}/comments", h.requireUser(h.commentPR))
		r.Post("/pulls/{number}/merge", h.requireUser(h.mergePR))

		r.Get("/issues", h.listIssues)
		r.Post("/issues", h.requireUser(h.createIssue))
		r.Get("/issues/{number}", h.getIssue)
		r.Post("/issues/{number}/comments", h.requireUser(h.commentIssue))
		r.Post("/issues/{number}/close", h.requireUser(h.closeIssue))
		r.Post("/issues/{number}/reopen", h.requireUser(h.reopenIssue))

		r.Get("/ci/jobs", h.listJobs)
		r.Get("/ci/jobs/{id}", h.getJob)
		r.Post("/ci/trigger", h.requireUser(h.triggerCI))
		r.Post("/settings/protect", h.requireUser(h.setProtect))
	})
	return r
}

func (h *Handler) requireUser(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if auth.UserFrom(r.Context()) == nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		fn(w, r)
	}
}

func (h *Handler) lookupRepo(r *http.Request) (*store.Repo, error) {
	owner := chi.URLParam(r, "owner")
	name := chi.URLParam(r, "name")
	repo, err := h.Store.Repos().GetByOwnerName(r.Context(), store.OwnerUser, owner, name)
	if err != nil {
		repo, err = h.Store.Repos().GetByOwnerName(r.Context(), store.OwnerOrg, owner, name)
	}
	return repo, err
}

func (h *Handler) gitDir(repo *store.Repo) string {
	return h.Disk.Path(repo.OwnerName, repo.Name)
}

func (h *Handler) avatarURL(username string) string {
	base := strings.TrimRight(h.Config.BaseURL, "/")
	return base + "/avatars/" + url.PathEscape(username)
}

func (h *Handler) userJSON(u *store.User) map[string]any {
	return map[string]any{
		"id": u.ID, "username": u.Username, "email": u.Email, "created_at": u.CreatedAt,
		"avatar_url": h.avatarURL(u.Username), "has_avatar": u.HasAvatar,
	}
}

func repoJSON(r *store.Repo) map[string]any {
	return map[string]any{
		"id": r.ID, "owner": r.OwnerName, "owner_type": r.OwnerType, "name": r.Name,
		"full_name": r.FullName(), "private": r.IsPrivate, "default_branch": r.DefaultBranch,
		"require_ci_pass": r.RequireCIPass, "created_at": r.CreatedAt,
	}
}

func (h *Handler) currentUser(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, h.userJSON(auth.UserFrom(r.Context())))
}

func (h *Handler) getUser(w http.ResponseWriter, r *http.Request) {
	u, err := h.Store.Users().GetByUsername(r.Context(), chi.URLParam(r, "username"))
	if err != nil {
		mapStoreError(w, err)
		return
	}
	out := map[string]any{
		"id": u.ID, "username": u.Username, "created_at": u.CreatedAt,
		"avatar_url": h.avatarURL(u.Username), "has_avatar": u.HasAvatar,
	}
	if me := auth.UserFrom(r.Context()); me != nil && me.ID == u.ID {
		out["email"] = u.Email
	}
	writeJSON(w, 200, out)
}

func splitOwnerName(s string) (string, string, bool) {
	owner, name, ok := strings.Cut(s, "/")
	return owner, name, ok && owner != "" && name != ""
}
