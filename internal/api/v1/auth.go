package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hokan/hokan/internal/auth"
	"github.com/hokan/hokan/internal/store"
)

type signupReq struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *Handler) signupAllowed(r *http.Request) bool {
	if h.Config.AllowSignup {
		return true
	}
	n, err := h.Store.Users().Count(r.Context())
	return err == nil && n == 0
}

func (h *Handler) signup(w http.ResponseWriter, r *http.Request) {
	if !h.signupAllowed(r) {
		writeError(w, http.StatusForbidden, "signup is disabled")
		return
	}
	var req signupReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "invalid json")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(req.Email)
	if !gitValid(req.Username) || req.Email == "" || len(req.Password) < 8 {
		writeError(w, 400, "username, email, and password (8+) required")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, 500, "hash failed")
		return
	}
	u := &store.User{
		ID: uuid.NewString(), Username: req.Username, Email: req.Email,
		PasswordHash: hash, CreatedAt: time.Now().UTC(),
	}
	if err := h.Store.Users().Create(r.Context(), u); err != nil {
		mapStoreError(w, err)
		return
	}
	raw, _, err := h.newSession(r, u)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"user": userJSON(u), "token": raw})
}

func gitValid(name string) bool {
	return len(name) > 0 && !strings.Contains(name, "/") && !strings.Contains(name, "..")
}

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "invalid json")
		return
	}
	u, err := h.Store.Users().GetByUsername(r.Context(), req.Username)
	if err != nil || !auth.CheckPassword(u.PasswordHash, req.Password) {
		writeError(w, 401, "invalid credentials")
		return
	}
	raw, _, err := h.newSession(r, u)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"user": userJSON(u), "token": raw})
}

func (h *Handler) newSession(r *http.Request, u *store.User) (string, *store.Session, error) {
	raw, hash, err := auth.RandomToken("hoks_")
	if err != nil {
		return "", nil, err
	}
	sess := &store.Session{
		ID: uuid.NewString(), UserID: u.ID, TokenHash: hash,
		ExpiresAt: time.Now().UTC().Add(auth.SessionTTL),
	}
	if err := h.Store.Users().CreateSession(r.Context(), sess); err != nil {
		return "", nil, err
	}
	return raw, sess, nil
}

func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	keys, err := h.Store.Users().ListSSHKeys(r.Context(), u.ID)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, map[string]any{"id": k.ID, "name": k.Name, "fingerprint": k.Fingerprint, "created_at": k.CreatedAt})
	}
	writeJSON(w, 200, out)
}

type addKeyReq struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

func (h *Handler) addKey(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	var req addKeyReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "invalid json")
		return
	}
	fp, canonical, err := auth.Fingerprint(req.Key)
	if err != nil {
		writeError(w, 400, "invalid ssh public key")
		return
	}
	if req.Name == "" {
		req.Name = "key"
	}
	k := &store.SSHKey{
		ID: uuid.NewString(), UserID: u.ID, Name: req.Name,
		PublicKey: strings.TrimSpace(canonical), Fingerprint: fp, CreatedAt: time.Now().UTC(),
	}
	if err := h.Store.Users().CreateSSHKey(r.Context(), k); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": k.ID, "name": k.Name, "fingerprint": k.Fingerprint})
}

func (h *Handler) deleteKey(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if err := h.Store.Users().DeleteSSHKey(r.Context(), u.ID, chi.URLParam(r, "id")); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (h *Handler) listTokens(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	toks, err := h.Store.Users().ListTokens(r.Context(), u.ID)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(toks))
	for _, t := range toks {
		out = append(out, map[string]any{"id": t.ID, "name": t.Name, "scopes": t.Scopes, "created_at": t.CreatedAt})
	}
	writeJSON(w, 200, out)
}

type createTokenReq struct {
	Name   string `json:"name"`
	Scopes string `json:"scopes"`
}

func (h *Handler) createToken(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	var req createTokenReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "invalid json")
		return
	}
	if req.Name == "" {
		req.Name = "token"
	}
	raw, hash, err := auth.RandomToken("hok_")
	if err != nil {
		writeError(w, 500, "token generate failed")
		return
	}
	t := &store.Token{
		ID: uuid.NewString(), UserID: u.ID, Name: req.Name,
		TokenHash: hash, Scopes: req.Scopes, CreatedAt: time.Now().UTC(),
	}
	if err := h.Store.Users().CreateToken(r.Context(), t); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": t.ID, "name": t.Name, "token": raw})
}

func (h *Handler) deleteToken(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if err := h.Store.Users().DeleteToken(r.Context(), u.ID, chi.URLParam(r, "id")); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
