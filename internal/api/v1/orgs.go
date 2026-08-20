package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hokan/hokan/internal/auth"
	"github.com/hokan/hokan/internal/store"
)

type createOrgReq struct {
	Name string `json:"name"`
}

func (h *Handler) createOrg(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	var req createOrgReq
	if err := decodeJSON(r, &req); err != nil || !gitValid(req.Name) {
		writeError(w, 400, "valid name required")
		return
	}
	o := &store.Org{ID: uuid.NewString(), Name: req.Name, CreatorUserID: u.ID, CreatedAt: time.Now().UTC()}
	if err := h.Store.Orgs().Create(r.Context(), o); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, 201, o)
}

func (h *Handler) listOrgs(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	orgs, err := h.Store.Orgs().ListForUser(r.Context(), u.ID)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, 200, orgs)
}

func (h *Handler) getOrg(w http.ResponseWriter, r *http.Request) {
	o, err := h.Store.Orgs().GetByName(r.Context(), chi.URLParam(r, "org"))
	if err != nil {
		mapStoreError(w, err)
		return
	}
	teams, _ := h.Store.Orgs().ListTeams(r.Context(), o.ID)
	writeJSON(w, 200, map[string]any{"org": o, "teams": teams})
}

type createTeamReq struct {
	Name       string `json:"name"`
	Permission string `json:"permission"`
}

func (h *Handler) createTeam(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	o, err := h.Store.Orgs().GetByName(r.Context(), chi.URLParam(r, "org"))
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if o.CreatorUserID != u.ID {
		writeError(w, 403, "org admin required")
		return
	}
	var req createTeamReq
	if err := decodeJSON(r, &req); err != nil || req.Name == "" {
		writeError(w, 400, "name required")
		return
	}
	perm := store.Permission(req.Permission)
	if perm != store.PermRead && perm != store.PermWrite && perm != store.PermAdmin {
		perm = store.PermRead
	}
	t := &store.Team{ID: uuid.NewString(), OrgID: o.ID, Name: req.Name, PermissionLevel: perm, CreatedAt: time.Now().UTC()}
	if err := h.Store.Orgs().CreateTeam(r.Context(), t); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, 201, t)
}

func (h *Handler) listTeams(w http.ResponseWriter, r *http.Request) {
	o, err := h.Store.Orgs().GetByName(r.Context(), chi.URLParam(r, "org"))
	if err != nil {
		mapStoreError(w, err)
		return
	}
	teams, err := h.Store.Orgs().ListTeams(r.Context(), o.ID)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, 200, teams)
}

type memberReq struct {
	Username string `json:"username"`
}

func (h *Handler) addTeamMember(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	o, err := h.Store.Orgs().GetByName(r.Context(), chi.URLParam(r, "org"))
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if o.CreatorUserID != u.ID {
		writeError(w, 403, "org admin required")
		return
	}
	teams, err := h.Store.Orgs().ListTeams(r.Context(), o.ID)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	var team *store.Team
	for i := range teams {
		if teams[i].Name == chi.URLParam(r, "team") || teams[i].ID == chi.URLParam(r, "team") {
			team = &teams[i]
			break
		}
	}
	if team == nil {
		writeError(w, 404, "team not found")
		return
	}
	var req memberReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "username required")
		return
	}
	member, err := h.Store.Users().GetByUsername(r.Context(), req.Username)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if err := h.Store.Orgs().AddTeamMember(r.Context(), team.ID, member.ID); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"ok": true})
}

type permReq struct {
	Team  string `json:"team"`
	Level string `json:"level"`
}

func (h *Handler) setRepoPermission(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	o, err := h.Store.Orgs().GetByName(r.Context(), chi.URLParam(r, "org"))
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if o.CreatorUserID != u.ID {
		writeError(w, 403, "org admin required")
		return
	}
	repo, err := h.Store.Repos().GetByOwnerName(r.Context(), store.OwnerOrg, o.Name, chi.URLParam(r, "name"))
	if err != nil {
		mapStoreError(w, err)
		return
	}
	var req permReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "invalid json")
		return
	}
	teams, _ := h.Store.Orgs().ListTeams(r.Context(), o.ID)
	var teamID string
	for _, t := range teams {
		if t.Name == req.Team || t.ID == req.Team {
			teamID = t.ID
			break
		}
	}
	if teamID == "" {
		writeError(w, 404, "team not found")
		return
	}
	level := store.Permission(req.Level)
	if level == "" {
		level = store.PermRead
	}
	if err := h.Store.Orgs().SetRepoPermission(r.Context(), teamID, repo.ID, level); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
