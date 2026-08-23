package api

import (
	"io"
	"net/http"

	"github.com/hokan/hokan/internal/auth"
	"github.com/hokan/hokan/internal/avatar"
)

func (h *Handler) uploadAvatar(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if h.Avatars == nil {
		writeError(w, 500, "avatars unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, avatar.MaxBytes+4096)
	if err := r.ParseMultipartForm(avatar.MaxBytes); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "file too large")
		return
	}
	f, _, err := r.FormFile("avatar")
	if err != nil {
		writeError(w, 400, "avatar file required")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, avatar.MaxBytes+1))
	if err != nil {
		writeError(w, 400, "could not read file")
		return
	}
	if err := h.Avatars.AttachCustom(r.Context(), h.Store.Users(), u.ID, data); err != nil {
		if avatar.IsInput(err) {
			writeError(w, 400, err.Error())
			return
		}
		writeError(w, 500, err.Error())
		return
	}
	u.HasAvatar = true
	writeJSON(w, 200, h.userJSON(u))
}

func (h *Handler) deleteAvatar(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if h.Avatars == nil {
		writeError(w, 500, "avatars unavailable")
		return
	}
	if err := h.Avatars.DetachCustom(r.Context(), h.Store.Users(), u.ID); err != nil {
		mapStoreError(w, err)
		return
	}
	u.HasAvatar = false
	writeJSON(w, 200, h.userJSON(u))
}
