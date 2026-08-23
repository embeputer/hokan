package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hokan/hokan/internal/store"
)

func TestMergeCommitAndConflict(t *testing.T) {
	root := t.TempDir()
	disk := &Disk{Root: root}
	if err := disk.CreateRepo("alice", "app"); err != nil {
		t.Fatal(err)
	}
	bare := disk.Path("alice", "app")
	work := filepath.Join(t.TempDir(), "work")
	runGit(t, "", "clone", bare, work)
	runGit(t, work, "config", "user.email", "a@b.c")
	runGit(t, work, "config", "user.name", "A")
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-m", "base")
	runGit(t, work, "push", "origin", "HEAD:main")

	runGit(t, work, "checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(work, "ok.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-m", "feature")
	runGit(t, work, "push", "origin", "feature")

	runGit(t, work, "checkout", "main")
	if err := os.WriteFile(filepath.Join(work, "g.txt"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-m", "main only")
	runGit(t, work, "push", "origin", "main")

	sha, err := MergeCommit(bare, "feature", "main", "merge feature", "A", "a@b.c")
	if err != nil {
		t.Fatal(err)
	}
	if sha == "" {
		t.Fatal("empty merge sha")
	}

	runGit(t, work, "checkout", "-b", "c1")
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("left\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "commit", "-am", "left")
	runGit(t, work, "push", "-u", "origin", "c1")
	runGit(t, work, "checkout", "main")
	runGit(t, work, "pull")
	runGit(t, work, "checkout", "-b", "c2")
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("right\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "commit", "-am", "right")
	runGit(t, work, "push", "-u", "origin", "c2")

	before, err := RevParse(bare, "refs/heads/c1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = MergeCommit(bare, "c2", "c1", "should conflict", "A", "a@b.c")
	if err == nil {
		t.Fatal("expected merge conflict")
	}
	if err != store.ErrMergeConflict && !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("got %v", err)
	}
	after, err := RevParse(bare, "refs/heads/c1")
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("target branch moved on conflict: %s -> %s", before, after)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
