package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func ValidName(s string) bool {
	return nameRe.MatchString(s) && !strings.Contains(s, "..")
}

type Disk struct {
	Root string
}

func (d *Disk) Path(owner, name string) string {
	return filepath.Join(d.Root, owner, name+".git")
}

func (d *Disk) Exists(owner, name string) bool {
	st, err := os.Stat(d.Path(owner, name))
	return err == nil && st.IsDir()
}

func (d *Disk) CreateRepo(owner, name string) error {
	if !ValidName(owner) || !ValidName(name) {
		return fmt.Errorf("invalid owner or repo name")
	}
	path := d.Path(owner, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	cmd := exec.Command("git", "init", "--bare", "--initial-branch=main", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git init: %w: %s", err, out)
	}
	cfgs := [][]string{
		{"http.receivepack", "true"},
		{"receive.denyCurrentBranch", "ignore"},
		{"uploadpack.allowAnySHA1InWant", "true"},
	}
	for _, kv := range cfgs {
		c := exec.Command("git", "--git-dir", path, "config", kv[0], kv[1])
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("git config %s: %w: %s", kv[0], err, out)
		}
	}
	return nil
}

func (d *Disk) DeleteRepo(owner, name string) error {
	return os.RemoveAll(d.Path(owner, name))
}

func Run(gitDir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"--git-dir", gitDir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (d *Disk) Run(owner, name string, args ...string) (string, error) {
	return Run(d.Path(owner, name), args...)
}
