package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hokan/hokan/internal/auth"
	"github.com/hokan/hokan/internal/git"
	"github.com/hokan/hokan/internal/store"
)

func (h *Handler) runnerFromRequest(r *http.Request) *store.CIRunner {
	hdr := r.Header.Get("Authorization")
	if len(hdr) < 8 {
		return nil
	}
	raw := hdr
	if len(hdr) > 7 && hdr[:7] == "Bearer " {
		raw = hdr[7:]
	}
	rn, err := h.Store.CI().GetRunnerByTokenHash(r.Context(), auth.HashSecret(raw))
	if err != nil {
		return nil
	}
	return rn
}

type createRunnerReq struct {
	Name string `json:"name"`
}

func (h *Handler) createRunner(w http.ResponseWriter, r *http.Request) {
	var req createRunnerReq
	if err := decodeJSON(r, &req); err != nil || req.Name == "" {
		writeError(w, 400, "name required")
		return
	}
	raw, hash, err := auth.RandomToken("hokr_")
	if err != nil {
		writeError(w, 500, "token generate failed")
		return
	}
	rn := &store.CIRunner{
		ID: uuid.NewString(), Name: req.Name, TokenHash: hash, Status: "offline", CreatedAt: time.Now().UTC(),
	}
	if err := h.Store.CI().CreateRunner(r.Context(), rn); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": rn.ID, "name": rn.Name, "token": raw})
}

func (h *Handler) waitJob(w http.ResponseWriter, r *http.Request) {
	rn := h.runnerFromRequest(r)
	if rn == nil {
		writeError(w, 401, "invalid runner token")
		return
	}
	_ = h.Store.CI().TouchRunner(r.Context(), rn.ID, time.Now().UTC())
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		job, err := h.Store.CI().ClaimQueuedJob(r.Context(), rn.ID, time.Now().UTC())
		if err == nil {
			repo, _ := h.Store.Repos().GetByID(r.Context(), job.RepoID)
			writeJSON(w, 200, map[string]any{
				"job":       job,
				"repo":      repoJSON(repo),
				"clone_url": h.Config.BaseURL + "/" + repo.FullName() + ".git",
			})
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(1 * time.Second):
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

type logReq struct {
	Chunk string `json:"chunk"`
}

func (h *Handler) appendJobLog(w http.ResponseWriter, r *http.Request) {
	if h.runnerFromRequest(r) == nil && auth.UserFrom(r.Context()) == nil {
		writeError(w, 401, "unauthorized")
		return
	}
	var req logReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "invalid json")
		return
	}
	if err := h.Store.CI().AppendLog(r.Context(), chi.URLParam(r, "id"), req.Chunk); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

type finishReq struct {
	Status string `json:"status"`
}

func (h *Handler) finishJob(w http.ResponseWriter, r *http.Request) {
	if h.runnerFromRequest(r) == nil {
		writeError(w, 401, "invalid runner token")
		return
	}
	var req finishReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "invalid json")
		return
	}
	st := store.CIJobStatus(req.Status)
	if st != store.CISuccess && st != store.CIFailure {
		st = store.CIFailure
	}
	if err := h.Store.CI().FinishJob(r.Context(), chi.URLParam(r, "id"), st, time.Now().UTC()); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (h *Handler) listJobs(w http.ResponseWriter, r *http.Request) {
	repo, err := h.lookupRepo(r)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if !h.Access.CanRead(r.Context(), auth.UserFrom(r.Context()), repo) {
		writeError(w, 404, "not found")
		return
	}
	jobs, err := h.Store.CI().ListJobs(r.Context(), repo.ID)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, 200, jobs)
}

func (h *Handler) getJob(w http.ResponseWriter, r *http.Request) {
	job, err := h.Store.CI().GetJob(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, 200, job)
}

func (h *Handler) triggerCI(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	repo, err := h.lookupRepo(r)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if !h.Access.CanWrite(r.Context(), u, repo) {
		writeError(w, 403, "forbidden")
		return
	}
	ref := r.URL.Query().Get("ref")
	if ref == "" {
		ref = repo.DefaultBranch
	}
	sha, err := git.RevParse(h.gitDir(repo), ref)
	if err != nil {
		writeError(w, 400, "unknown ref")
		return
	}
	if h.EnqueueCI != nil {
		h.EnqueueCI(repo, sha, nil)
	}
	writeJSON(w, 202, map[string]any{"queued": true, "sha": sha})
}

type protectReq struct {
	RequireCIPass bool `json:"require_ci_pass"`
}

func (h *Handler) setProtect(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	repo, err := h.lookupRepo(r)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if !h.Access.CanAdmin(r.Context(), u, repo) {
		writeError(w, 403, "forbidden")
		return
	}
	var req protectReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid json")
		return
	}
	if err := h.Store.Repos().UpdateRequireCIPass(r.Context(), repo.ID, req.RequireCIPass); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"require_ci_pass": req.RequireCIPass})
}
