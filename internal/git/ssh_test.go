package git

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	cryptox509 "crypto/x509"
	"github.com/google/uuid"
	"github.com/hokan/hokan/internal/auth"
	"github.com/hokan/hokan/internal/migrate"
	"github.com/hokan/hokan/internal/store"
	"github.com/hokan/hokan/internal/store/sqlite"
	"golang.org/x/crypto/ssh"
)

func TestSSHClonePush(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "t.db")
	sqlDB, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if err := migrate.Up(sqlDB); err != nil {
		t.Fatal(err)
	}
	st, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	hash, err := auth.HashPassword("password1")
	if err != nil {
		t.Fatal(err)
	}
	u := &store.User{ID: uuid.NewString(), Username: "alice", Email: "a@b.c", PasswordHash: hash, CreatedAt: time.Now()}
	if err := st.Users().Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pub := string(ssh.MarshalAuthorizedKey(signer.PublicKey()))
	fp, canonical, err := auth.Fingerprint(pub)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Users().CreateSSHKey(ctx, &store.SSHKey{
		ID: uuid.NewString(), UserID: u.ID, Name: "t", PublicKey: canonical, Fingerprint: fp, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	disk := &Disk{Root: filepath.Join(dir, "repos")}
	repo := &store.Repo{ID: uuid.NewString(), OwnerType: store.OwnerUser, OwnerID: u.ID, OwnerName: "alice", Name: "app", DefaultBranch: "main", CreatedAt: time.Now()}
	if err := st.Repos().Create(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if err := disk.CreateRepo("alice", "app"); err != nil {
		t.Fatal(err)
	}
	access := &auth.Access{Store: st}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	hostKey := filepath.Join(dir, "host")
	srv := &SSH{Addr: addr, HostKey: hostKey, Disk: disk, Access: access, Store: st}
	go func() { _ = srv.ListenAndServe() }()
	time.Sleep(200 * time.Millisecond)

	keyPath := filepath.Join(dir, "id_rsa")
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: cryptox509.MarshalPKCS1PrivateKey(priv)}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(addr)
	work := filepath.Join(dir, "work")
	sshCmd := fmt.Sprintf("ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i %s -p %s", keyPath, port)
	clone := exec.Command("git", "clone", fmt.Sprintf("ssh://git@127.0.0.1:%s/alice/app.git", port), work)
	clone.Env = append(os.Environ(), "GIT_SSH_COMMAND="+sshCmd)
	if out, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"config", "user.email", "a@b.c"},
		{"config", "user.name", "A"},
		{"add", "."},
		{"commit", "-m", "init"},
		{"push", "origin", "HEAD:main"},
	} {
		c := exec.Command("git", args...)
		c.Dir = work
		c.Env = append(os.Environ(), "GIT_SSH_COMMAND="+sshCmd)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}
