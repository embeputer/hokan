package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hokan/hokan/internal/auth"
	"github.com/hokan/hokan/internal/git"
	"github.com/hokan/hokan/internal/store"
)

type createRepoReq struct {
	Name      string `json:"name"`
	Private   bool   `json:"private"`
	Owner     string `json:"owner"`
	OwnerType string `json:"owner_type"`
}

func (h *Handler) listRepos(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	uid := ""
	if u != nil {
		uid = u.ID
	}
	repos, err := h.Store.Repos().ListVisible(r.Context(), uid)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(repos))
	for i := range repos {
		out = append(out, repoJSON(&repos[i]))
	}
	writeJSON(w, 200, out)
}

func (h *Handler) createRepo(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	var req createRepoReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "invalid json")
		return
	}
	if !git.ValidName(req.Name) {
		writeError(w, 400, "invalid repo name")
		return
	}
	ownerType := store.OwnerUser
	ownerID := u.ID
	ownerName := u.Username
	if req.OwnerType == "org" || (req.Owner != "" && req.Owner != u.Username) {
		orgName := req.Owner
		if orgName == "" {
			writeError(w, 400, "owner required for org repo")
			return
		}
		org, err := h.Store.Orgs().GetByName(r.Context(), orgName)
		if err != nil {
			mapStoreError(w, err)
			return
		}
		fake := &store.Repo{OwnerType: store.OwnerOrg, OwnerID: org.ID, ID: ""}
		perm, _ := h.Store.Orgs().BestPermission(r.Context(), u.ID, fake)
		if org.CreatorUserID != u.ID && perm != store.PermAdmin {
			writeError(w, 403, "org admin required")
			return
		}
		ownerType = store.OwnerOrg
		ownerID = org.ID
		ownerName = org.Name
	}
	repo := &store.Repo{
		ID: uuid.NewString(), OwnerType: ownerType, OwnerID: ownerID, OwnerName: ownerName,
		Name: req.Name, IsPrivate: req.Private, DefaultBranch: "main", CreatedAt: time.Now().UTC(),
	}
	if err := h.Store.Repos().Create(r.Context(), repo); err != nil {
		mapStoreError(w, err)
		return
	}
	if err := h.Disk.CreateRepo(ownerName, req.Name); err != nil {
		_ = h.Store.Repos().Delete(r.Context(), repo.ID)
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, repoJSON(repo))
}

func (h *Handler) getRepo(w http.ResponseWriter, r *http.Request) {
	repo, err := h.lookupRepo(r)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if !h.Access.CanRead(r.Context(), auth.UserFrom(r.Context()), repo) {
		writeError(w, 404, "not found")
		return
	}
	writeJSON(w, 200, repoJSON(repo))
}

func (h *Handler) deleteRepo(w http.ResponseWriter, r *http.Request) {
	repo, err := h.lookupRepo(r)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if !h.Access.CanAdmin(r.Context(), auth.UserFrom(r.Context()), repo) {
		writeError(w, 403, "forbidden")
		return
	}
	if err := h.Store.Repos().Delete(r.Context(), repo.ID); err != nil {
		mapStoreError(w, err)
		return
	}
	_ = h.Disk.DeleteRepo(repo.OwnerName, repo.Name)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (h *Handler) repoBranches(w http.ResponseWriter, r *http.Request) {
	repo, err := h.lookupRepo(r)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if !h.Access.CanRead(r.Context(), auth.UserFrom(r.Context()), repo) {
		writeError(w, 404, "not found")
		return
	}
	branches, err := git.ListBranches(h.gitDir(repo))
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, branches)
}

func (h *Handler) repoCommits(w http.ResponseWriter, r *http.Request) {
	repo, err := h.lookupRepo(r)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if !h.Access.CanRead(r.Context(), auth.UserFrom(r.Context()), repo) {
		writeError(w, 404, "not found")
		return
	}
	ref := r.URL.Query().Get("ref")
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	commits, err := git.Log(h.gitDir(repo), ref, n)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, commits)
}

func (h *Handler) repoTree(w http.ResponseWriter, r *http.Request) {
	repo, err := h.lookupRepo(r)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if !h.Access.CanRead(r.Context(), auth.UserFrom(r.Context()), repo) {
		writeError(w, 404, "not found")
		return
	}
	entries, err := git.Tree(h.gitDir(repo), r.URL.Query().Get("ref"), r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, entries)
}

func (h *Handler) repoBlob(w http.ResponseWriter, r *http.Request) {
	repo, err := h.lookupRepo(r)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if !h.Access.CanRead(r.Context(), auth.UserFrom(r.Context()), repo) {
		writeError(w, 404, "not found")
		return
	}
	content, err := git.Blob(h.gitDir(repo), r.URL.Query().Get("ref"), r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, 404, "file not found")
		return
	}
	writeJSON(w, 200, map[string]any{"path": r.URL.Query().Get("path"), "content": content})
}

func (h *Handler) repoSearch(w http.ResponseWriter, r *http.Request) {
	repo, err := h.lookupRepo(r)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if !h.Access.CanRead(r.Context(), auth.UserFrom(r.Context()), repo) {
		writeError(w, 404, "not found")
		return
	}
	hits, err := git.Search(h.gitDir(repo), r.URL.Query().Get("ref"), strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, hits)
}
