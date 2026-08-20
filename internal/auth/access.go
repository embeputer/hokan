package auth

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/hokan/hokan/internal/store"
)

type contextKey int

const (
	userKey contextKey = iota
	repoKey
	permKey
)

const (
	CookieName   = "hokan_session"
	SessionTTL   = 30 * 24 * time.Hour
	BearerPrefix = "Bearer "
)

func WithUser(ctx context.Context, u *store.User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

func UserFrom(ctx context.Context) *store.User {
	u, _ := ctx.Value(userKey).(*store.User)
	return u
}

func WithRepo(ctx context.Context, r *store.Repo) context.Context {
	return context.WithValue(ctx, repoKey, r)
}

func RepoFrom(ctx context.Context) *store.Repo {
	r, _ := ctx.Value(repoKey).(*store.Repo)
	return r
}

func WithPerm(ctx context.Context, p store.Permission) context.Context {
	return context.WithValue(ctx, permKey, p)
}

func PermFrom(ctx context.Context) store.Permission {
	p, _ := ctx.Value(permKey).(store.Permission)
	return p
}

type Access struct {
	Store store.Store
}

func rank(p store.Permission) int {
	switch p {
	case store.PermAdmin:
		return 3
	case store.PermWrite:
		return 2
	case store.PermRead:
		return 1
	default:
		return 0
	}
}

func (a *Access) Permission(ctx context.Context, user *store.User, repo *store.Repo) store.Permission {
	if user != nil {
		if p, err := a.Store.Orgs().BestPermission(ctx, user.ID, repo); err == nil && p != "" {
			return p
		}
		if repo.OwnerType == store.OwnerUser && user.ID == repo.OwnerID {
			return store.PermAdmin
		}
	}
	if !repo.IsPrivate {
		return store.PermRead
	}
	return ""
}

func (a *Access) CanRead(ctx context.Context, user *store.User, repo *store.Repo) bool {
	return rank(a.Permission(ctx, user, repo)) >= rank(store.PermRead)
}

func (a *Access) CanWrite(ctx context.Context, user *store.User, repo *store.Repo) bool {
	return rank(a.Permission(ctx, user, repo)) >= rank(store.PermWrite)
}

func (a *Access) CanAdmin(ctx context.Context, user *store.User, repo *store.Repo) bool {
	return rank(a.Permission(ctx, user, repo)) >= rank(store.PermAdmin)
}

func (a *Access) Authenticate(r *http.Request) *store.User {
	ctx := r.Context()
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, BearerPrefix) {
		raw := strings.TrimSpace(strings.TrimPrefix(h, BearerPrefix))
		if u := a.userFromSecret(ctx, raw); u != nil {
			return u
		}
	}
	if u, p, ok := r.BasicAuth(); ok {
		user, err := a.Store.Users().GetByUsername(ctx, u)
		if err == nil && CheckPassword(user.PasswordHash, p) {
			return user
		}
		if user != nil {
			if tok, err := a.Store.Users().GetTokenByHash(ctx, HashSecret(p)); err == nil && tok.UserID == user.ID {
				if found, err := a.Store.Users().GetByID(ctx, tok.UserID); err == nil {
					return found
				}
			}
		}
	}
	if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
		return a.userFromSecret(ctx, c.Value)
	}
	return nil
}

func (a *Access) userFromSecret(ctx context.Context, raw string) *store.User {
	hash := HashSecret(raw)
	if sess, err := a.Store.Users().GetSessionByTokenHash(ctx, hash); err == nil {
		if time.Now().After(sess.ExpiresAt) {
			return nil
		}
		u, err := a.Store.Users().GetByID(ctx, sess.UserID)
		if err == nil {
			return u
		}
	}
	if tok, err := a.Store.Users().GetTokenByHash(ctx, hash); err == nil {
		u, err := a.Store.Users().GetByID(ctx, tok.UserID)
		if err == nil {
			return u
		}
	}
	return nil
}

func Middleware(a *Access) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if u := a.Authenticate(r); u != nil {
				r = r.WithContext(WithUser(r.Context(), u))
			}
			next.ServeHTTP(w, r)
		})
	}
}

func RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if UserFrom(r.Context()) == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
