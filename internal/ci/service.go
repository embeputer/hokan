package ci

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hokan/hokan/internal/git"
	"github.com/hokan/hokan/internal/store"
	"gopkg.in/yaml.v3"
)

type ConfigFile struct {
	Jobs     map[string]JobSpec `yaml:"jobs"`
	Triggers []string           `yaml:"triggers"`
}

type JobSpec struct {
	Image string     `yaml:"image"`
	Steps []StepSpec `yaml:"steps"`
}

type StepSpec struct {
	Run string `yaml:"run"`
}

type Service struct {
	Store store.Store
	Disk  *git.Disk
	Log   *slog.Logger
}

func Parse(raw string) (*ConfigFile, error) {
	var c ConfigFile
	if err := yaml.Unmarshal([]byte(raw), &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Service) OnPush(owner, name, sha string) {
	ctx := context.Background()
	repo, err := s.Store.Repos().GetByOwnerName(ctx, store.OwnerUser, owner, name)
	if err != nil {
		repo, err = s.Store.Repos().GetByOwnerName(ctx, store.OwnerOrg, owner, name)
	}
	if err != nil {
		return
	}
	s.Enqueue(ctx, repo, sha, nil, "push")
	prs, err := s.Store.PullRequests().List(ctx, repo.ID)
	if err != nil {
		return
	}
	for i := range prs {
		pr := &prs[i]
		if pr.State != store.PROpen {
			continue
		}
		if src, err := git.RevParse(s.Disk.Path(repo.OwnerName, repo.Name), pr.SourceBranch); err == nil && src == sha {
			s.Enqueue(ctx, repo, sha, pr, "pull_request")
		}
	}
}

func (s *Service) OnPR(repo *store.Repo, pr *store.PullRequest, sha string) {
	s.Enqueue(context.Background(), repo, sha, pr, "pull_request")
}

func (s *Service) Enqueue(ctx context.Context, repo *store.Repo, sha string, pr *store.PullRequest, trigger string) {
	raw, err := git.Blob(s.Disk.Path(repo.OwnerName, repo.Name), sha, ".hokan/ci.yml")
	if err != nil {
		return
	}
	cfg, err := Parse(raw)
	if err != nil || len(cfg.Jobs) == 0 {
		return
	}
	if len(cfg.Triggers) > 0 {
		ok := false
		for _, t := range cfg.Triggers {
			if t == trigger {
				ok = true
				break
			}
		}
		if !ok {
			return
		}
	}
	for name, spec := range cfg.Jobs {
		j := &store.CIJob{
			ID: uuid.NewString(), RepoID: repo.ID, CommitSHA: sha, Name: name,
			Status: store.CIQueued, CreatedAt: time.Now().UTC(),
		}
		if pr != nil {
			id := pr.ID
			j.PRID = &id
		}
		if err := s.Store.CI().CreateJob(ctx, j); err != nil {
			if s.Log != nil {
				s.Log.Error("enqueue ci job", "err", err)
			}
			continue
		}
		_ = spec
	}
}

func JobCommands(raw string, jobName string) (image string, commands []string, err error) {
	cfg, err := Parse(raw)
	if err != nil {
		return "", nil, err
	}
	spec, ok := cfg.Jobs[jobName]
	if !ok {
		for _, v := range cfg.Jobs {
			spec = v
			break
		}
	}
	image = spec.Image
	if image == "" {
		image = "alpine:3.20"
	}
	for _, st := range spec.Steps {
		if strings.TrimSpace(st.Run) != "" {
			commands = append(commands, st.Run)
		}
	}
	return image, commands, nil
}
