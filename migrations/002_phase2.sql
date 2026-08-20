-- +goose Up
CREATE TABLE pull_requests (
    id TEXT PRIMARY KEY,
    repo_id TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    number INTEGER NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    source_branch TEXT NOT NULL,
    target_branch TEXT NOT NULL,
    author_id TEXT NOT NULL REFERENCES users(id),
    state TEXT NOT NULL,
    merge_sha TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    UNIQUE(repo_id, number)
);

CREATE TABLE pr_comments (
    id TEXT PRIMARY KEY,
    pr_id TEXT NOT NULL REFERENCES pull_requests(id) ON DELETE CASCADE,
    author_id TEXT NOT NULL REFERENCES users(id),
    body TEXT NOT NULL,
    file_path TEXT NOT NULL DEFAULT '',
    line INTEGER,
    created_at TEXT NOT NULL
);

CREATE TABLE issues (
    id TEXT PRIMARY KEY,
    repo_id TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    number INTEGER NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    author_id TEXT NOT NULL REFERENCES users(id),
    state TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE(repo_id, number)
);

CREATE TABLE issue_comments (
    id TEXT PRIMARY KEY,
    issue_id TEXT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    author_id TEXT NOT NULL REFERENCES users(id),
    body TEXT NOT NULL,
    created_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE issue_comments;
DROP TABLE issues;
DROP TABLE pr_comments;
DROP TABLE pull_requests;
