-- +goose Up
CREATE TABLE orgs (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE COLLATE NOCASE,
    creator_user_id TEXT NOT NULL REFERENCES users(id),
    created_at TEXT NOT NULL
);

CREATE TABLE teams (
    id TEXT PRIMARY KEY,
    org_id TEXT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    permission_level TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE(org_id, name)
);

CREATE TABLE team_members (
    team_id TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (team_id, user_id)
);

CREATE TABLE repo_permissions (
    team_id TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    repo_id TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    level TEXT NOT NULL,
    PRIMARY KEY (team_id, repo_id)
);

CREATE TABLE ci_runners (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    last_seen_at TEXT,
    status TEXT NOT NULL DEFAULT 'offline',
    created_at TEXT NOT NULL
);

CREATE TABLE ci_jobs (
    id TEXT PRIMARY KEY,
    repo_id TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    commit_sha TEXT NOT NULL,
    pr_id TEXT REFERENCES pull_requests(id) ON DELETE SET NULL,
    name TEXT NOT NULL,
    status TEXT NOT NULL,
    runner_id TEXT REFERENCES ci_runners(id),
    started_at TEXT,
    finished_at TEXT,
    created_at TEXT NOT NULL
);

CREATE TABLE ci_logs (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL REFERENCES ci_jobs(id) ON DELETE CASCADE,
    chunk TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX idx_ci_jobs_queued ON ci_jobs(status, created_at);
CREATE INDEX idx_ci_logs_job ON ci_logs(job_id);

-- +goose Down
DROP TABLE ci_logs;
DROP TABLE ci_jobs;
DROP TABLE ci_runners;
DROP TABLE repo_permissions;
DROP TABLE team_members;
DROP TABLE teams;
DROP TABLE orgs;
