package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hokan/hokan/internal/auth"
	"github.com/hokan/hokan/internal/git"
	"github.com/hokan/hokan/internal/store"
)

func prJSON(pr *store.PullRequest) map[string]any {
	return map[string]any{
		"id": pr.ID, "number": pr.Number, "title": pr.Title, "description": pr.Description,
		"source_branch": pr.SourceBranch, "target_branch": pr.TargetBranch,
		"author": pr.AuthorName, "state": pr.State, "merge_sha": pr.MergeSHA, "created_at": pr.CreatedAt,
	}
}

func issueJSON(issue *store.Issue) map[string]any {
	return map[string]any{
		"id": issue.ID, "number": issue.Number, "title": issue.Title, "description": issue.Description,
		"author": issue.AuthorName, "state": issue.State, "created_at": issue.CreatedAt,
	}
}

type createPRReq struct {
	Title        string `json:"title"`
	Description  string `json:"description"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
}

func (h *Handler) createPR(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	repo, err := h.lookupRepo(r)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if !h.Access.CanRead(r.Context(), u, repo) {
		writeError(w, 404, "not found")
		return
	}
	var req createPRReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "invalid json")
		return
	}
	if strings.TrimSpace(req.Title) == "" || req.SourceBranch == "" {
		writeError(w, 400, "title and source_branch required")
		return
	}
	if req.TargetBranch == "" {
		req.TargetBranch = repo.DefaultBranch
	}
	dir := h.gitDir(repo)
	if !git.BranchExists(dir, req.SourceBranch) || !git.BranchExists(dir, req.TargetBranch) {
		writeError(w, 400, "source and target branches must exist")
		return
	}
	n, err := h.Store.Repos().NextNumber(r.Context(), repo.ID)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	pr := &store.PullRequest{
		ID: uuid.NewString(), RepoID: repo.ID, Number: n, Title: req.Title, Description: req.Description,
		SourceBranch: req.SourceBranch, TargetBranch: req.TargetBranch, AuthorID: u.ID, AuthorName: u.Username,
		State: store.PROpen, CreatedAt: time.Now().UTC(),
	}
	if err := h.Store.PullRequests().Create(r.Context(), pr); err != nil {
		mapStoreError(w, err)
		return
	}
	if h.OnPR != nil {
		sha, _ := git.RevParse(dir, req.SourceBranch)
		h.OnPR(repo, pr, sha)
	}
	writeJSON(w, 201, prJSON(pr))
}

func (h *Handler) listPRs(w http.ResponseWriter, r *http.Request) {
	repo, err := h.lookupRepo(r)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if !h.Access.CanRead(r.Context(), auth.UserFrom(r.Context()), repo) {
		writeError(w, 404, "not found")
		return
	}
	prs, err := h.Store.PullRequests().List(r.Context(), repo.ID)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(prs))
	for i := range prs {
		out = append(out, prJSON(&prs[i]))
	}
	writeJSON(w, 200, out)
}

func (h *Handler) getPR(w http.ResponseWriter, r *http.Request) {
	repo, pr, err := h.loadPR(r)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if !h.Access.CanRead(r.Context(), auth.UserFrom(r.Context()), repo) {
		writeError(w, 404, "not found")
		return
	}
	diff, _ := git.Diff(h.gitDir(repo), pr.TargetBranch, pr.SourceBranch)
	comments, _ := h.Store.PullRequests().ListComments(r.Context(), pr.ID)
	writeJSON(w, 200, map[string]any{"pull_request": prJSON(pr), "diff": diff, "comments": comments})
}

func (h *Handler) loadPR(r *http.Request) (*store.Repo, *store.PullRequest, error) {
	repo, err := h.lookupRepo(r)
	if err != nil {
		return nil, nil, err
	}
	n, err := strconv.Atoi(chi.URLParam(r, "number"))
	if err != nil {
		return nil, nil, store.ErrInvalid
	}
	pr, err := h.Store.PullRequests().Get(r.Context(), repo.ID, n)
	return repo, pr, err
}

type commentReq struct {
	Body     string `json:"body"`
	FilePath string `json:"file_path"`
	Line     *int   `json:"line"`
}

func (h *Handler) commentPR(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	repo, pr, err := h.loadPR(r)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if !h.Access.CanRead(r.Context(), u, repo) {
		writeError(w, 404, "not found")
		return
	}
	var req commentReq
	if err := decodeJSON(r, &req); err != nil || strings.TrimSpace(req.Body) == "" {
		writeError(w, 400, "body required")
		return
	}
	c := &store.Comment{
		ID: uuid.NewString(), ParentID: pr.ID, AuthorID: u.ID, AuthorName: u.Username,
		Body: req.Body, FilePath: req.FilePath, Line: req.Line, CreatedAt: time.Now().UTC(),
	}
	if err := h.Store.PullRequests().CreateComment(r.Context(), c); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, 201, c)
}

func (h *Handler) mergePR(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	repo, pr, err := h.loadPR(r)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if !h.Access.CanWrite(r.Context(), u, repo) {
		writeError(w, 403, "write access required")
		return
	}
	if pr.State != store.PROpen {
		writeError(w, 400, "pull request is not open")
		return
	}
	if repo.RequireCIPass {
		sha, _ := git.RevParse(h.gitDir(repo), pr.SourceBranch)
		jobs, _ := h.Store.CI().LatestForSHA(r.Context(), repo.ID, sha)
		if len(jobs) == 0 {
			writeError(w, 400, "CI has not run on this pull request")
			return
		}
		for _, j := range jobs {
			if j.Status != store.CISuccess {
				writeError(w, 400, "CI must pass before merge")
				return
			}
		}
	}
	msg := "Merge pull request #" + strconv.Itoa(pr.Number) + " from " + pr.SourceBranch
	sha, err := git.MergeCommit(h.gitDir(repo), pr.SourceBranch, pr.TargetBranch, msg)
	if err != nil {
		if errors.Is(err, store.ErrMergeConflict) {
			writeError(w, http.StatusConflict, "merge conflict: the branches do not merge cleanly; resolve locally and push, then retry")
			return
		}
		writeError(w, 500, err.Error())
		return
	}
	if err := h.Store.PullRequests().SetState(r.Context(), pr.ID, store.PRMerged, sha); err != nil {
		mapStoreError(w, err)
		return
	}
	pr.State = store.PRMerged
	pr.MergeSHA = sha
	writeJSON(w, 200, prJSON(pr))
}

type createIssueReq struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

func (h *Handler) createIssue(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	repo, err := h.lookupRepo(r)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if !h.Access.CanRead(r.Context(), u, repo) {
		writeError(w, 404, "not found")
		return
	}
	var req createIssueReq
	if err := decodeJSON(r, &req); err != nil || strings.TrimSpace(req.Title) == "" {
		writeError(w, 400, "title required")
		return
	}
	n, err := h.Store.Repos().NextNumber(r.Context(), repo.ID)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	issue := &store.Issue{
		ID: uuid.NewString(), RepoID: repo.ID, Number: n, Title: req.Title, Description: req.Description,
		AuthorID: u.ID, AuthorName: u.Username, State: store.IssueOpen, CreatedAt: time.Now().UTC(),
	}
	if err := h.Store.Issues().Create(r.Context(), issue); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, 201, issueJSON(issue))
}

func (h *Handler) listIssues(w http.ResponseWriter, r *http.Request) {
	repo, err := h.lookupRepo(r)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if !h.Access.CanRead(r.Context(), auth.UserFrom(r.Context()), repo) {
		writeError(w, 404, "not found")
		return
	}
	issues, err := h.Store.Issues().List(r.Context(), repo.ID)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(issues))
	for i := range issues {
		out = append(out, issueJSON(&issues[i]))
	}
	writeJSON(w, 200, out)
}

func (h *Handler) getIssue(w http.ResponseWriter, r *http.Request) {
	repo, issue, err := h.loadIssue(r)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if !h.Access.CanRead(r.Context(), auth.UserFrom(r.Context()), repo) {
		writeError(w, 404, "not found")
		return
	}
	comments, _ := h.Store.Issues().ListComments(r.Context(), issue.ID)
	writeJSON(w, 200, map[string]any{"issue": issueJSON(issue), "comments": comments})
}

func (h *Handler) loadIssue(r *http.Request) (*store.Repo, *store.Issue, error) {
	repo, err := h.lookupRepo(r)
	if err != nil {
		return nil, nil, err
	}
	n, err := strconv.Atoi(chi.URLParam(r, "number"))
	if err != nil {
		return nil, nil, store.ErrInvalid
	}
	issue, err := h.Store.Issues().Get(r.Context(), repo.ID, n)
	return repo, issue, err
}

func (h *Handler) commentIssue(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	repo, issue, err := h.loadIssue(r)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if !h.Access.CanRead(r.Context(), u, repo) {
		writeError(w, 404, "not found")
		return
	}
	var req commentReq
	if err := decodeJSON(r, &req); err != nil || strings.TrimSpace(req.Body) == "" {
		writeError(w, 400, "body required")
		return
	}
	c := &store.Comment{ID: uuid.NewString(), ParentID: issue.ID, AuthorID: u.ID, AuthorName: u.Username, Body: req.Body, CreatedAt: time.Now().UTC()}
	if err := h.Store.Issues().CreateComment(r.Context(), c); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, 201, c)
}

func (h *Handler) closeIssue(w http.ResponseWriter, r *http.Request) {
	h.setIssueState(w, r, store.IssueClosed)
}

func (h *Handler) reopenIssue(w http.ResponseWriter, r *http.Request) {
	h.setIssueState(w, r, store.IssueOpen)
}

func (h *Handler) setIssueState(w http.ResponseWriter, r *http.Request, st store.IssueState) {
	repo, issue, err := h.loadIssue(r)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if !h.Access.CanWrite(r.Context(), auth.UserFrom(r.Context()), repo) && auth.UserFrom(r.Context()).ID != issue.AuthorID {
		writeError(w, 403, "forbidden")
		return
	}
	if err := h.Store.Issues().SetState(r.Context(), issue.ID, st); err != nil {
		mapStoreError(w, err)
		return
	}
	issue.State = st
	writeJSON(w, 200, issueJSON(issue))
}
