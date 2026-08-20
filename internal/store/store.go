package store

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrConflict      = errors.New("already exists")
	ErrInvalid       = errors.New("invalid")
	ErrUnauthorized  = errors.New("unauthorized")
	ErrForbidden     = errors.New("forbidden")
	ErrMergeConflict = errors.New("merge conflict")
)

type OwnerType string

const (
	OwnerUser OwnerType = "user"
	OwnerOrg  OwnerType = "org"
)

type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

type Session struct {
	ID        string
	UserID    string
	TokenHash string
	ExpiresAt time.Time
}

type SSHKey struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	Name        string    `json:"name"`
	PublicKey   string    `json:"-"`
	Fingerprint string    `json:"fingerprint"`
	CreatedAt   time.Time `json:"created_at"`
}

type Token struct {
	ID        string
	UserID    string
	Name      string
	TokenHash string
	Scopes    string
	ExpiresAt *time.Time
	CreatedAt time.Time
}

type Repo struct {
	ID            string
	OwnerType     OwnerType
	OwnerID       string
	OwnerName     string
	Name          string
	IsPrivate     bool
	DefaultBranch string
	RequireCIPass bool
	CreatedAt     time.Time
}

func (r Repo) FullName() string {
	if r.OwnerName == "" {
		return r.Name
	}
	return r.OwnerName + "/" + r.Name
}

type PullRequestState string

const (
	PROpen   PullRequestState = "open"
	PRMerged PullRequestState = "merged"
	PRClosed PullRequestState = "closed"
)

type PullRequest struct {
	ID           string
	RepoID       string
	Number       int
	Title        string
	Description  string
	SourceBranch string
	TargetBranch string
	AuthorID     string
	AuthorName   string
	State        PullRequestState
	MergeSHA     string
	CreatedAt    time.Time
}

type Comment struct {
	ID         string    `json:"id"`
	ParentID   string    `json:"parent_id"`
	AuthorID   string    `json:"author_id"`
	AuthorName string    `json:"author"`
	Body       string    `json:"body"`
	FilePath   string    `json:"file_path,omitempty"`
	Line       *int      `json:"line,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type IssueState string

const (
	IssueOpen   IssueState = "open"
	IssueClosed IssueState = "closed"
)

type Issue struct {
	ID          string
	RepoID      string
	Number      int
	Title       string
	Description string
	AuthorID    string
	AuthorName  string
	State       IssueState
	CreatedAt   time.Time
}

type Org struct {
	ID            string
	Name          string
	CreatorUserID string
	CreatedAt     time.Time
}

type Permission string

const (
	PermRead  Permission = "read"
	PermWrite Permission = "write"
	PermAdmin Permission = "admin"
)

type Team struct {
	ID              string
	OrgID           string
	Name            string
	PermissionLevel Permission
	CreatedAt       time.Time
}

type RepoPermission struct {
	TeamID string
	RepoID string
	Level  Permission
}

type CIJobStatus string

const (
	CIQueued  CIJobStatus = "queued"
	CIRunning CIJobStatus = "running"
	CISuccess CIJobStatus = "success"
	CIFailure CIJobStatus = "failure"
)

type CIJob struct {
	ID         string      `json:"id"`
	RepoID     string      `json:"repo_id"`
	CommitSHA  string      `json:"commit_sha"`
	PRID       *string     `json:"pr_id,omitempty"`
	Name       string      `json:"name"`
	Status     CIJobStatus `json:"status"`
	RunnerID   *string     `json:"runner_id,omitempty"`
	LogText    string      `json:"log_text"`
	StartedAt  *time.Time  `json:"started_at,omitempty"`
	FinishedAt *time.Time  `json:"finished_at,omitempty"`
	CreatedAt  time.Time   `json:"created_at"`
}

type CIRunner struct {
	ID         string
	Name       string
	TokenHash  string
	LastSeenAt *time.Time
	Status     string
	CreatedAt  time.Time
}

type Store interface {
	Users() UserStore
	Repos() RepoStore
	PullRequests() PullRequestStore
	Issues() IssueStore
	Orgs() OrgStore
	CI() CIStore
	Close() error
}

type UserStore interface {
	Create(ctx context.Context, u *User) error
	GetByID(ctx context.Context, id string) (*User, error)
	GetByUsername(ctx context.Context, username string) (*User, error)
	Count(ctx context.Context) (int, error)

	CreateSession(ctx context.Context, s *Session) error
	GetSessionByTokenHash(ctx context.Context, hash string) (*Session, error)
	DeleteSession(ctx context.Context, id string) error
	DeleteExpiredSessions(ctx context.Context, now time.Time) error

	CreateSSHKey(ctx context.Context, k *SSHKey) error
	ListSSHKeys(ctx context.Context, userID string) ([]SSHKey, error)
	GetSSHKeyByFingerprint(ctx context.Context, fp string) (*SSHKey, error)
	DeleteSSHKey(ctx context.Context, userID, id string) error

	CreateToken(ctx context.Context, t *Token) error
	ListTokens(ctx context.Context, userID string) ([]Token, error)
	GetTokenByHash(ctx context.Context, hash string) (*Token, error)
	DeleteToken(ctx context.Context, userID, id string) error
}

type RepoStore interface {
	Create(ctx context.Context, r *Repo) error
	GetByID(ctx context.Context, id string) (*Repo, error)
	GetByOwnerName(ctx context.Context, ownerType OwnerType, ownerName, name string) (*Repo, error)
	ListVisible(ctx context.Context, userID string) ([]Repo, error)
	ListByOwner(ctx context.Context, ownerType OwnerType, ownerID string) ([]Repo, error)
	Delete(ctx context.Context, id string) error
	UpdateDefaultBranch(ctx context.Context, id, branch string) error
	UpdateRequireCIPass(ctx context.Context, id string, require bool) error
	NextNumber(ctx context.Context, repoID string) (int, error)
}

type PullRequestStore interface {
	Create(ctx context.Context, pr *PullRequest) error
	Get(ctx context.Context, repoID string, number int) (*PullRequest, error)
	GetByID(ctx context.Context, id string) (*PullRequest, error)
	List(ctx context.Context, repoID string) ([]PullRequest, error)
	SetState(ctx context.Context, id string, state PullRequestState, mergeSHA string) error
	CreateComment(ctx context.Context, c *Comment) error
	ListComments(ctx context.Context, prID string) ([]Comment, error)
}

type IssueStore interface {
	Create(ctx context.Context, issue *Issue) error
	Get(ctx context.Context, repoID string, number int) (*Issue, error)
	List(ctx context.Context, repoID string) ([]Issue, error)
	SetState(ctx context.Context, id string, state IssueState) error
	CreateComment(ctx context.Context, c *Comment) error
	ListComments(ctx context.Context, issueID string) ([]Comment, error)
}

type OrgStore interface {
	Create(ctx context.Context, o *Org) error
	GetByID(ctx context.Context, id string) (*Org, error)
	GetByName(ctx context.Context, name string) (*Org, error)
	ListForUser(ctx context.Context, userID string) ([]Org, error)

	CreateTeam(ctx context.Context, t *Team) error
	GetTeam(ctx context.Context, id string) (*Team, error)
	ListTeams(ctx context.Context, orgID string) ([]Team, error)
	AddTeamMember(ctx context.Context, teamID, userID string) error
	RemoveTeamMember(ctx context.Context, teamID, userID string) error
	ListTeamMembers(ctx context.Context, teamID string) ([]User, error)

	SetRepoPermission(ctx context.Context, teamID, repoID string, level Permission) error
	ListRepoPermissions(ctx context.Context, repoID string) ([]RepoPermission, error)
	BestPermission(ctx context.Context, userID string, repo *Repo) (Permission, error)
}

type CIStore interface {
	CreateRunner(ctx context.Context, r *CIRunner) error
	GetRunnerByTokenHash(ctx context.Context, hash string) (*CIRunner, error)
	GetRunner(ctx context.Context, id string) (*CIRunner, error)
	ListRunners(ctx context.Context) ([]CIRunner, error)
	TouchRunner(ctx context.Context, id string, now time.Time) error

	CreateJob(ctx context.Context, j *CIJob) error
	GetJob(ctx context.Context, id string) (*CIJob, error)
	ListJobs(ctx context.Context, repoID string) ([]CIJob, error)
	ListJobsByPR(ctx context.Context, prID string) ([]CIJob, error)
	ClaimQueuedJob(ctx context.Context, runnerID string, now time.Time) (*CIJob, error)
	AppendLog(ctx context.Context, jobID, chunk string) error
	FinishJob(ctx context.Context, id string, status CIJobStatus, now time.Time) error
	LatestForSHA(ctx context.Context, repoID, sha string) ([]CIJob, error)
}
