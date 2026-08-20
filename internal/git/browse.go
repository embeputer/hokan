package git

import (
	"bufio"
	"fmt"
	"path"
	"strconv"
	"strings"
)

func IsGitPath(p string) bool {
	return strings.Contains(p, ".git/") || strings.HasSuffix(p, ".git")
}

type Commit struct {
	SHA     string
	Author  string
	Email   string
	Date    string
	Subject string
}

type TreeEntry struct {
	Mode string
	Type string
	SHA  string
	Name string
}

type SearchHit struct {
	Path    string
	Line    int
	Preview string
}

func DefaultRef(gitDir string) string {
	out, err := Run(gitDir, "symbolic-ref", "--short", "HEAD")
	if err != nil || out == "" {
		return "main"
	}
	return out
}

func ListBranches(gitDir string) ([]string, error) {
	out, err := Run(gitDir, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

func Log(gitDir, ref string, n int) ([]Commit, error) {
	if ref == "" {
		ref = DefaultRef(gitDir)
	}
	if n <= 0 {
		n = 50
	}
	out, err := Run(gitDir, "log", fmt.Sprintf("-%d", n), "--format=%H%x1f%an%x1f%ae%x1f%ad%x1f%s", "--date=iso", ref)
	if err != nil {
		if isEmptyRepo(gitDir) {
			return nil, nil
		}
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	var commits []Commit
	for _, line := range strings.Split(out, "\n") {
		p := strings.Split(line, "\x1f")
		if len(p) < 5 {
			continue
		}
		commits = append(commits, Commit{SHA: p[0], Author: p[1], Email: p[2], Date: p[3], Subject: p[4]})
	}
	return commits, nil
}

func Tree(gitDir, ref, dir string) ([]TreeEntry, error) {
	if ref == "" {
		ref = DefaultRef(gitDir)
	}
	spec := ref
	dir = strings.Trim(dir, "/")
	if dir != "" {
		spec = ref + ":" + dir
	} else {
		spec = ref + ":"
	}
	out, err := Run(gitDir, "ls-tree", spec)
	if err != nil {
		if isEmptyRepo(gitDir) {
			return nil, nil
		}
		return nil, err
	}
	var entries []TreeEntry
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		// <mode> SP <type> SP <sha> TAB <name>
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			continue
		}
		meta := strings.Fields(line[:tab])
		if len(meta) < 3 {
			continue
		}
		entries = append(entries, TreeEntry{Mode: meta[0], Type: meta[1], SHA: meta[2], Name: line[tab+1:]})
	}
	return entries, nil
}

func Blob(gitDir, ref, filePath string) (string, error) {
	if ref == "" {
		ref = DefaultRef(gitDir)
	}
	filePath = strings.TrimPrefix(filePath, "/")
	return Run(gitDir, "show", ref+":"+filePath)
}

func FileExists(gitDir, ref, filePath string) bool {
	_, err := Blob(gitDir, ref, filePath)
	return err == nil
}

func README(gitDir, ref string) (name, content string, ok bool) {
	for _, n := range []string{"README.md", "README", "readme.md", "Readme.md"} {
		c, err := Blob(gitDir, ref, n)
		if err == nil {
			return n, c, true
		}
	}
	return "", "", false
}

func Search(gitDir, ref, query string) ([]SearchHit, error) {
	if query == "" {
		return nil, nil
	}
	if ref == "" {
		ref = DefaultRef(gitDir)
	}
	out, err := Run(gitDir, "grep", "-n", "-I", "-E", "--max-count", "5", "-e", query, ref)
	hits := []SearchHit{}
	if err == nil && out != "" {
		for i, line := range strings.Split(out, "\n") {
			if i >= 100 {
				break
			}
			// ref:path:line:text  OR path:line:text
			rest := line
			if strings.HasPrefix(rest, ref+":") {
				rest = strings.TrimPrefix(rest, ref+":")
			}
			pathPart, after, ok := strings.Cut(rest, ":")
			if !ok {
				continue
			}
			lineStr, preview, ok := strings.Cut(after, ":")
			if !ok {
				continue
			}
			ln, _ := strconv.Atoi(lineStr)
			hits = append(hits, SearchHit{Path: pathPart, Line: ln, Preview: preview})
		}
	}
	files, _ := Run(gitDir, "ls-tree", "-r", "--name-only", ref)
	n := len(hits)
	if files != "" {
		q := strings.ToLower(query)
		for _, p := range strings.Split(files, "\n") {
			if n >= 100 {
				break
			}
			if strings.Contains(strings.ToLower(path.Base(p)), q) {
				hits = append(hits, SearchHit{Path: p, Preview: "(filename match)"})
				n++
			}
		}
	}
	return hits, nil
}

func Diff(gitDir, target, source string) (string, error) {
	out, err := Run(gitDir, "diff", target+"..."+source)
	if err != nil {
		return "", err
	}
	return out, nil
}

func BranchExists(gitDir, branch string) bool {
	_, err := Run(gitDir, "rev-parse", "--verify", "refs/heads/"+branch)
	return err == nil
}

func RevParse(gitDir, rev string) (string, error) {
	return Run(gitDir, "rev-parse", rev)
}

func isEmptyRepo(gitDir string) bool {
	_, err := Run(gitDir, "rev-parse", "--verify", "HEAD")
	return err != nil
}

func ParentPath(p string) string {
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return ""
	}
	return p[:i]
}
