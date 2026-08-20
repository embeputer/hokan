package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hokan/hokan/internal/migrate"
	"github.com/hokan/hokan/internal/store"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := sqlDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(sqlDB); err != nil {
		t.Fatal(err)
	}
	return OpenDB(sqlDB)
}

func TestUserCreateAndGet(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	u := &store.User{
		ID: uuid.NewString(), Username: "alice", Email: "alice@example.com",
		PasswordHash: "hash", CreatedAt: time.Now().UTC(),
	}
	if err := db.Users().Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	got, err := db.Users().GetByUsername(ctx, "Alice")
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != u.Email {
		t.Fatalf("email = %q", got.Email)
	}
	n, err := db.Users().Count(ctx)
	if err != nil || n != 1 {
		t.Fatalf("count = %d, err=%v", n, err)
	}
}

func TestUserConflict(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	u := &store.User{ID: uuid.NewString(), Username: "bob", Email: "bob@example.com", PasswordHash: "h", CreatedAt: time.Now()}
	if err := db.Users().Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	u2 := &store.User{ID: uuid.NewString(), Username: "bob", Email: "other@example.com", PasswordHash: "h", CreatedAt: time.Now()}
	if err := db.Users().Create(ctx, u2); err != store.ErrConflict {
		t.Fatalf("got %v, want conflict", err)
	}
}

func TestSessionAndTokenAndKey(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	u := &store.User{ID: uuid.NewString(), Username: "carol", Email: "c@e.com", PasswordHash: "h", CreatedAt: time.Now()}
	if err := db.Users().Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	sess := &store.Session{ID: uuid.NewString(), UserID: u.ID, TokenHash: "sesshash", ExpiresAt: time.Now().Add(time.Hour)}
	if err := db.Users().CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	got, err := db.Users().GetSessionByTokenHash(ctx, "sesshash")
	if err != nil || got.UserID != u.ID {
		t.Fatalf("session: %v %#v", err, got)
	}
	tok := &store.Token{ID: uuid.NewString(), UserID: u.ID, Name: "cli", TokenHash: "tokhash", CreatedAt: time.Now()}
	if err := db.Users().CreateToken(ctx, tok); err != nil {
		t.Fatal(err)
	}
	gt, err := db.Users().GetTokenByHash(ctx, "tokhash")
	if err != nil || gt.Name != "cli" {
		t.Fatalf("token: %v %#v", err, gt)
	}
	k := &store.SSHKey{ID: uuid.NewString(), UserID: u.ID, Name: "laptop", PublicKey: "ssh-ed25519 AAAA", Fingerprint: "SHA256:abc", CreatedAt: time.Now()}
	if err := db.Users().CreateSSHKey(ctx, k); err != nil {
		t.Fatal(err)
	}
	gk, err := db.Users().GetSSHKeyByFingerprint(ctx, "SHA256:abc")
	if err != nil || gk.UserID != u.ID {
		t.Fatalf("key: %v %#v", err, gk)
	}
}

func TestRepoCRUD(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	u := &store.User{ID: uuid.NewString(), Username: "dave", Email: "d@e.com", PasswordHash: "h", CreatedAt: time.Now()}
	if err := db.Users().Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	r := &store.Repo{
		ID: uuid.NewString(), OwnerType: store.OwnerUser, OwnerID: u.ID,
		Name: "hello", DefaultBranch: "main", CreatedAt: time.Now(),
	}
	if err := db.Repos().Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, err := db.Repos().GetByOwnerName(ctx, store.OwnerUser, "dave", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerName != "dave" || got.Name != "hello" {
		t.Fatalf("got %#v", got)
	}
	list, err := db.Repos().ListVisible(ctx, u.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v err=%v", list, err)
	}
	if err := db.Repos().Delete(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Repos().GetByID(ctx, r.ID); err != store.ErrNotFound {
		t.Fatalf("want not found, got %v", err)
	}
}
