package git

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/hokan/hokan/internal/store"
)

func mergeIdent(name, email string) []string {
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(email)
	if strings.ContainsRune(name, 0) {
		name = ""
	}
	if strings.ContainsRune(email, 0) {
		email = ""
	}
	if name == "" {
		name = "Hokan"
	}
	if email == "" {
		email = "hokan@localhost"
	}
	return []string{
		"GIT_AUTHOR_NAME=" + name,
		"GIT_AUTHOR_EMAIL=" + email,
		"GIT_COMMITTER_NAME=" + name,
		"GIT_COMMITTER_EMAIL=" + email,
	}
}

func MergeCommit(gitDir, source, target, message, authorName, authorEmail string) (string, error) {
	tmp, err := os.MkdirTemp("", "hokan-merge-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	add := exec.Command("git", "--git-dir", gitDir, "worktree", "add", tmp, target)
	if out, err := add.CombinedOutput(); err != nil {
		return "", fmt.Errorf("worktree add: %w: %s", err, out)
	}
	defer func() {
		_ = exec.Command("git", "--git-dir", gitDir, "worktree", "remove", "--force", tmp).Run()
	}()

	merge := exec.Command("git", "merge", "--no-ff", "--no-edit", "-m", message, source)
	merge.Dir = tmp
	merge.Env = append(os.Environ(), mergeIdent(authorName, authorEmail)...)
	out, err := merge.CombinedOutput()
	if err != nil {
		_ = exec.Command("git", "-C", tmp, "merge", "--abort").Run()
		combined := strings.ToLower(string(out) + err.Error())
		if strings.Contains(combined, "conflict") || strings.Contains(combined, "automatic merge failed") {
			return "", fmt.Errorf("%w: %s", store.ErrMergeConflict, strings.TrimSpace(string(out)))
		}
		return "", fmt.Errorf("merge failed: %w: %s", err, out)
	}
	sha, err := Run(gitDir, "rev-parse", "refs/heads/"+target)
	if err != nil {
		return "", err
	}
	return sha, nil
}
