package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hokan/hokan/internal/ci"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	server := env("HOKAN_SERVER", "http://localhost:8080")
	token := os.Getenv("HOKAN_RUNNER_TOKEN")
	if token == "" {
		log.Error("HOKAN_RUNNER_TOKEN is required")
		os.Exit(1)
	}
	client := &http.Client{Timeout: 45 * time.Second}
	log.Info("runner started", "server", server)
	for {
		job, repo, cloneURL, err := waitJob(client, server, token)
		if err != nil {
			log.Error("wait", "err", err)
			time.Sleep(2 * time.Second)
			continue
		}
		if job == nil {
			continue
		}
		log.Info("running job", "id", job.ID, "name", job.Name, "sha", job.CommitSHA)
		ok := runJob(log, client, server, token, job, repo, cloneURL)
		status := "success"
		if !ok {
			status = "failure"
		}
		_ = postJSON(client, server+"/api/v1/ci/jobs/"+job.ID+"/finish", token, map[string]string{"status": status})
	}
}

type job struct {
	ID        string `json:"ID"`
	Name      string `json:"Name"`
	CommitSHA string `json:"CommitSHA"`
	RepoID    string `json:"RepoID"`
}

func waitJob(client *http.Client, server, token string) (*job, map[string]any, string, error) {
	req, _ := http.NewRequest("GET", server+"/api/v1/ci/jobs/wait", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := client.Do(req)
	if err != nil {
		return nil, nil, "", err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNoContent {
		return nil, nil, "", nil
	}
	if res.StatusCode >= 400 {
		b, _ := io.ReadAll(res.Body)
		return nil, nil, "", fmt.Errorf("wait %s: %s", res.Status, b)
	}
	var env struct {
		Data struct {
			Job      map[string]any `json:"job"`
			Repo     map[string]any `json:"repo"`
			CloneURL string         `json:"clone_url"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
		return nil, nil, "", err
	}
	b, _ := json.Marshal(env.Data.Job)
	var j job
	_ = json.Unmarshal(b, &j)
	if j.ID == "" {
		j.ID = str(env.Data.Job["id"])
		j.Name = str(env.Data.Job["name"])
		j.CommitSHA = str(env.Data.Job["commit_sha"])
	}
	return &j, env.Data.Repo, env.Data.CloneURL, nil
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func runJob(log *slog.Logger, client *http.Client, server, token string, j *job, repo map[string]any, cloneURL string) bool {
	dir, err := os.MkdirTemp("", "hokan-job-*")
	if err != nil {
		return false
	}
	defer os.RemoveAll(dir)
	appendLog := func(s string) {
		_ = postJSON(client, server+"/api/v1/ci/jobs/"+j.ID+"/logs", token, map[string]string{"chunk": s})
	}
	u := cloneURL
	if token != "" && strings.HasPrefix(u, "http") {
		u = strings.Replace(u, "://", "://runner:"+token+"@", 1)
	}
	cmd := exec.Command("git", "clone", "--depth", "1", u, dir)
	out, err := cmd.CombinedOutput()
	appendLog(string(out))
	if err != nil {
		appendLog("clone failed: " + err.Error() + "\n")
		return false
	}
	if j.CommitSHA != "" {
		co := exec.Command("git", "fetch", "--depth", "1", "origin", j.CommitSHA)
		co.Dir = dir
		out, _ = co.CombinedOutput()
		appendLog(string(out))
		co = exec.Command("git", "checkout", j.CommitSHA)
		co.Dir = dir
		out, err = co.CombinedOutput()
		appendLog(string(out))
		if err != nil {
			return false
		}
	}
	yml, err := os.ReadFile(filepath.Join(dir, ".hokan", "ci.yml"))
	if err != nil {
		appendLog("no .hokan/ci.yml\n")
		return false
	}
	image, commands, err := ci.JobCommands(string(yml), j.Name)
	if err != nil {
		appendLog("parse ci.yml: " + err.Error() + "\n")
		return false
	}
	script := strings.Join(commands, "\n")
	docker := exec.Command("docker", "run", "--rm", "-v", dir+":/workspace", "-w", "/workspace", image, "sh", "-lc", script)
	var buf bytes.Buffer
	docker.Stdout = io.MultiWriter(&buf, os.Stdout)
	docker.Stderr = io.MultiWriter(&buf, os.Stderr)
	err = docker.Run()
	appendLog(buf.String())
	if err != nil {
		appendLog("docker: " + err.Error() + "\n")
		return false
	}
	return true
}

func postJSON(client *http.Client, url, token string, body any) error {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	io.Copy(io.Discard, res.Body)
	if res.StatusCode >= 400 {
		return fmt.Errorf("http %s", res.Status)
	}
	return nil
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
