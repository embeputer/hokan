package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/hokan/hokan/internal/store"
	_ "modernc.org/sqlite"
)

type DB struct {
	sql *sql.DB
}

func Open(path string) (*DB, error) {
	dsn := dsnFor(path)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetConnMaxLifetime(0)
	if _, err := sqlDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return &DB{sql: sqlDB}, nil
}

func OpenDB(sqlDB *sql.DB) *DB {
	return &DB{sql: sqlDB}
}

func dsnFor(path string) string {
	if path == ":memory:" {
		return "file:hokanmem?mode=memory&cache=shared&_pragma=foreign_keys(1)"
	}
	abs := path
	if !filepath.IsAbs(path) {
		if a, err := filepath.Abs(path); err == nil {
			abs = a
		}
	}
	return "file:" + abs + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
}

func (d *DB) SQL() *sql.DB { return d.sql }

func (d *DB) Users() store.UserStore               { return &userStore{d} }
func (d *DB) Repos() store.RepoStore               { return &repoStore{d} }
func (d *DB) PullRequests() store.PullRequestStore { return &prStore{d} }
func (d *DB) Issues() store.IssueStore             { return &issueStore{d} }
func (d *DB) Orgs() store.OrgStore                 { return &orgStore{d} }
func (d *DB) CI() store.CIStore                    { return &ciStore{d} }

func (d *DB) Close() error { return d.sql.Close() }

func isUnique(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	if isUnique(err) {
		return store.ErrConflict
	}
	return err
}

func nowString(t time.Time) string {
	if t.IsZero() {
		t = time.Now().UTC()
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return nowString(*t)
}

func scanNullTime(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	t := parseTime(ns.String)
	return &t
}

type userStore struct{ db *DB }

func (s *userStore) Create(ctx context.Context, u *store.User) error {
	_, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO users (id, username, email, password_hash, created_at) VALUES (?, ?, ?, ?, ?)`,
		u.ID, u.Username, u.Email, u.PasswordHash, nowString(u.CreatedAt),
	)
	return mapErr(err)
}

func (s *userStore) GetByID(ctx context.Context, id string) (*store.User, error) {
	return s.scanUser(s.db.sql.QueryRowContext(ctx,
		`SELECT id, username, email, password_hash, created_at FROM users WHERE id = ?`, id,
	))
}

func (s *userStore) GetByUsername(ctx context.Context, username string) (*store.User, error) {
	return s.scanUser(s.db.sql.QueryRowContext(ctx,
		`SELECT id, username, email, password_hash, created_at FROM users WHERE username = ? COLLATE NOCASE`, username,
	))
}

func (s *userStore) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *userStore) scanUser(row *sql.Row) (*store.User, error) {
	var u store.User
	var created string
	if err := row.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &created); err != nil {
		return nil, mapErr(err)
	}
	u.CreatedAt = parseTime(created)
	return &u, nil
}

func (s *userStore) CreateSession(ctx context.Context, sess *store.Session) error {
	_, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, token_hash, expires_at) VALUES (?, ?, ?, ?)`,
		sess.ID, sess.UserID, sess.TokenHash, nowString(sess.ExpiresAt),
	)
	return mapErr(err)
}

func (s *userStore) GetSessionByTokenHash(ctx context.Context, hash string) (*store.Session, error) {
	var sess store.Session
	var exp string
	err := s.db.sql.QueryRowContext(ctx,
		`SELECT id, user_id, token_hash, expires_at FROM sessions WHERE token_hash = ?`, hash,
	).Scan(&sess.ID, &sess.UserID, &sess.TokenHash, &exp)
	if err != nil {
		return nil, mapErr(err)
	}
	sess.ExpiresAt = parseTime(exp)
	return &sess, nil
}

func (s *userStore) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.sql.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func (s *userStore) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	_, err := s.db.sql.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, nowString(now))
	return err
}

func (s *userStore) CreateSSHKey(ctx context.Context, k *store.SSHKey) error {
	_, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO ssh_keys (id, user_id, name, public_key, fingerprint, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		k.ID, k.UserID, k.Name, k.PublicKey, k.Fingerprint, nowString(k.CreatedAt),
	)
	return mapErr(err)
}

func (s *userStore) ListSSHKeys(ctx context.Context, userID string) ([]store.SSHKey, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT id, user_id, name, public_key, fingerprint, created_at FROM ssh_keys WHERE user_id = ? ORDER BY created_at`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.SSHKey
	for rows.Next() {
		var k store.SSHKey
		var created string
		if err := rows.Scan(&k.ID, &k.UserID, &k.Name, &k.PublicKey, &k.Fingerprint, &created); err != nil {
			return nil, err
		}
		k.CreatedAt = parseTime(created)
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *userStore) GetSSHKeyByFingerprint(ctx context.Context, fp string) (*store.SSHKey, error) {
	var k store.SSHKey
	var created string
	err := s.db.sql.QueryRowContext(ctx,
		`SELECT id, user_id, name, public_key, fingerprint, created_at FROM ssh_keys WHERE fingerprint = ?`, fp,
	).Scan(&k.ID, &k.UserID, &k.Name, &k.PublicKey, &k.Fingerprint, &created)
	if err != nil {
		return nil, mapErr(err)
	}
	k.CreatedAt = parseTime(created)
	return &k, nil
}

func (s *userStore) DeleteSSHKey(ctx context.Context, userID, id string) error {
	res, err := s.db.sql.ExecContext(ctx, `DELETE FROM ssh_keys WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *userStore) CreateToken(ctx context.Context, t *store.Token) error {
	_, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO tokens (id, user_id, name, token_hash, scopes, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.UserID, t.Name, t.TokenHash, t.Scopes, nullTime(t.ExpiresAt), nowString(t.CreatedAt),
	)
	return mapErr(err)
}

func (s *userStore) ListTokens(ctx context.Context, userID string) ([]store.Token, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT id, user_id, name, token_hash, scopes, expires_at, created_at FROM tokens WHERE user_id = ? ORDER BY created_at`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Token
	for rows.Next() {
		var t store.Token
		var exp sql.NullString
		var created string
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.TokenHash, &t.Scopes, &exp, &created); err != nil {
			return nil, err
		}
		t.ExpiresAt = scanNullTime(exp)
		t.CreatedAt = parseTime(created)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *userStore) GetTokenByHash(ctx context.Context, hash string) (*store.Token, error) {
	var t store.Token
	var exp sql.NullString
	var created string
	err := s.db.sql.QueryRowContext(ctx,
		`SELECT id, user_id, name, token_hash, scopes, expires_at, created_at FROM tokens WHERE token_hash = ?`, hash,
	).Scan(&t.ID, &t.UserID, &t.Name, &t.TokenHash, &t.Scopes, &exp, &created)
	if err != nil {
		return nil, mapErr(err)
	}
	t.ExpiresAt = scanNullTime(exp)
	t.CreatedAt = parseTime(created)
	if t.ExpiresAt != nil && t.ExpiresAt.Before(time.Now().UTC()) {
		return nil, store.ErrNotFound
	}
	return &t, nil
}

func (s *userStore) DeleteToken(ctx context.Context, userID, id string) error {
	res, err := s.db.sql.ExecContext(ctx, `DELETE FROM tokens WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

type repoStore struct{ db *DB }

const repoSelect = `
SELECT r.id, r.owner_type, r.owner_id,
       COALESCE(u.username, '') AS owner_name,
       r.name, r.is_private, r.default_branch,
       r.require_ci_pass, r.created_at
FROM repos r
LEFT JOIN users u ON r.owner_type = 'user' AND u.id = r.owner_id`

func (s *repoStore) scan(row scanner) (*store.Repo, error) {
	var r store.Repo
	var created string
	var priv, require int
	var ownerType string
	if err := row.Scan(&r.ID, &ownerType, &r.OwnerID, &r.OwnerName, &r.Name, &priv, &r.DefaultBranch, &require, &created); err != nil {
		return nil, mapErr(err)
	}
	r.OwnerType = store.OwnerType(ownerType)
	r.IsPrivate = priv != 0
	r.RequireCIPass = require != 0
	r.CreatedAt = parseTime(created)
	return &r, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func (s *repoStore) Create(ctx context.Context, r *store.Repo) error {
	_, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO repos (id, owner_type, owner_id, name, is_private, default_branch, require_ci_pass, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, string(r.OwnerType), r.OwnerID, r.Name, boolToInt(r.IsPrivate), r.DefaultBranch, boolToInt(r.RequireCIPass), nowString(r.CreatedAt),
	)
	return mapErr(err)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *repoStore) GetByID(ctx context.Context, id string) (*store.Repo, error) {
	row := s.db.sql.QueryRowContext(ctx, repoSelect+` WHERE r.id = ?`, id)
	r, err := s.scan(row)
	if err != nil {
		return nil, err
	}
	s.fillOrgName(ctx, r)
	return r, nil
}

func (s *repoStore) GetByOwnerName(ctx context.Context, ownerType store.OwnerType, ownerName, name string) (*store.Repo, error) {
	if ownerType == store.OwnerOrg {
		var orgID string
		err := s.db.sql.QueryRowContext(ctx, `SELECT id FROM orgs WHERE name = ? COLLATE NOCASE`, ownerName).Scan(&orgID)
		if err != nil {
			return nil, mapErr(err)
		}
		row := s.db.sql.QueryRowContext(ctx, repoSelect+` WHERE r.owner_type = 'org' AND r.owner_id = ? AND r.name = ? COLLATE NOCASE`, orgID, name)
		r, err := s.scan(row)
		if err != nil {
			return nil, err
		}
		r.OwnerName = ownerName
		return r, nil
	}
	row := s.db.sql.QueryRowContext(ctx, repoSelect+`
		WHERE r.owner_type = 'user' AND r.name = ? COLLATE NOCASE AND u.username = ? COLLATE NOCASE`,
		name, ownerName,
	)
	return s.scan(row)
}

func (s *repoStore) fillOrgName(ctx context.Context, r *store.Repo) {
	if r == nil || r.OwnerType != store.OwnerOrg || r.OwnerName != "" {
		return
	}
	var name string
	if err := s.db.sql.QueryRowContext(ctx, `SELECT name FROM orgs WHERE id = ?`, r.OwnerID).Scan(&name); err == nil {
		r.OwnerName = name
	}
}

func (s *repoStore) ListVisible(ctx context.Context, userID string) ([]store.Repo, error) {
	q := repoSelect + `
		WHERE r.is_private = 0
		   OR (r.owner_type = 'user' AND r.owner_id = ?)
		ORDER BY r.created_at DESC`
	rows, err := s.db.sql.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out, err := scanRepos(rows)
	if err != nil {
		return nil, err
	}
	return s.enrichOrgVisibility(ctx, userID, out)
}

func (s *repoStore) ListByOwner(ctx context.Context, ownerType store.OwnerType, ownerID string) ([]store.Repo, error) {
	rows, err := s.db.sql.QueryContext(ctx, repoSelect+` WHERE r.owner_type = ? AND r.owner_id = ? ORDER BY r.name`, string(ownerType), ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out, err := scanRepos(rows)
	if err != nil {
		return nil, err
	}
	for i := range out {
		s.fillOrgName(ctx, &out[i])
	}
	return out, nil
}

func (s *repoStore) enrichOrgVisibility(ctx context.Context, userID string, visible []store.Repo) ([]store.Repo, error) {
	rows, err := s.db.sql.QueryContext(ctx, repoSelect+` WHERE r.owner_type = 'org'`)
	if err != nil {
		return visible, nil
	}
	defer rows.Close()
	orgRepos, err := scanRepos(rows)
	if err != nil {
		return visible, nil
	}
	seen := map[string]struct{}{}
	for _, r := range visible {
		seen[r.ID] = struct{}{}
	}
	for i := range orgRepos {
		r := &orgRepos[i]
		s.fillOrgName(ctx, r)
		if _, ok := seen[r.ID]; ok {
			continue
		}
		perm, err := s.db.Orgs().BestPermission(ctx, userID, r)
		if err != nil || perm == "" {
			if !r.IsPrivate {
				visible = append(visible, *r)
			}
			continue
		}
		visible = append(visible, *r)
	}
	return visible, nil
}

func scanRepos(rows *sql.Rows) ([]store.Repo, error) {
	var out []store.Repo
	for rows.Next() {
		var r store.Repo
		var created string
		var priv, require int
		var ownerType string
		if err := rows.Scan(&r.ID, &ownerType, &r.OwnerID, &r.OwnerName, &r.Name, &priv, &r.DefaultBranch, &require, &created); err != nil {
			return nil, err
		}
		r.OwnerType = store.OwnerType(ownerType)
		r.IsPrivate = priv != 0
		r.RequireCIPass = require != 0
		r.CreatedAt = parseTime(created)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *repoStore) Delete(ctx context.Context, id string) error {
	res, err := s.db.sql.ExecContext(ctx, `DELETE FROM repos WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *repoStore) UpdateDefaultBranch(ctx context.Context, id, branch string) error {
	_, err := s.db.sql.ExecContext(ctx, `UPDATE repos SET default_branch = ? WHERE id = ?`, branch, id)
	return err
}

func (s *repoStore) UpdateRequireCIPass(ctx context.Context, id string, require bool) error {
	_, err := s.db.sql.ExecContext(ctx, `UPDATE repos SET require_ci_pass = ? WHERE id = ?`, boolToInt(require), id)
	return err
}

func (s *repoStore) NextNumber(ctx context.Context, repoID string) (int, error) {
	tx, err := s.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var n int
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(next_number, 1) FROM repos WHERE id = ?`, repoID).Scan(&n)
	if err != nil {
		return 0, mapErr(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE repos SET next_number = ? WHERE id = ?`, n+1, repoID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}

// Stubs used until Phase 2/3 migrations exist. They return clear errors so
// accidental calls fail loudly rather than panicking.

type prStore struct{ db *DB }

func (s *prStore) Create(ctx context.Context, pr *store.PullRequest) error {
	_, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO pull_requests (id, repo_id, number, title, description, source_branch, target_branch, author_id, state, merge_sha, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		pr.ID, pr.RepoID, pr.Number, pr.Title, pr.Description, pr.SourceBranch, pr.TargetBranch, pr.AuthorID, string(pr.State), pr.MergeSHA, nowString(pr.CreatedAt),
	)
	return mapErr(err)
}

func (s *prStore) Get(ctx context.Context, repoID string, number int) (*store.PullRequest, error) {
	return s.scanPR(s.db.sql.QueryRowContext(ctx, prSelect+` WHERE p.repo_id = ? AND p.number = ?`, repoID, number))
}

func (s *prStore) GetByID(ctx context.Context, id string) (*store.PullRequest, error) {
	return s.scanPR(s.db.sql.QueryRowContext(ctx, prSelect+` WHERE p.id = ?`, id))
}

const prSelect = `
SELECT p.id, p.repo_id, p.number, p.title, p.description, p.source_branch, p.target_branch,
       p.author_id, COALESCE(u.username, ''), p.state, COALESCE(p.merge_sha, ''), p.created_at
FROM pull_requests p
LEFT JOIN users u ON u.id = p.author_id`

func (s *prStore) scanPR(row *sql.Row) (*store.PullRequest, error) {
	var pr store.PullRequest
	var created, st string
	if err := row.Scan(&pr.ID, &pr.RepoID, &pr.Number, &pr.Title, &pr.Description, &pr.SourceBranch, &pr.TargetBranch,
		&pr.AuthorID, &pr.AuthorName, &st, &pr.MergeSHA, &created); err != nil {
		return nil, mapErr(err)
	}
	pr.State = store.PullRequestState(st)
	pr.CreatedAt = parseTime(created)
	return &pr, nil
}

func (s *prStore) List(ctx context.Context, repoID string) ([]store.PullRequest, error) {
	rows, err := s.db.sql.QueryContext(ctx, prSelect+` WHERE p.repo_id = ? ORDER BY p.number DESC`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.PullRequest
	for rows.Next() {
		var pr store.PullRequest
		var created, st string
		if err := rows.Scan(&pr.ID, &pr.RepoID, &pr.Number, &pr.Title, &pr.Description, &pr.SourceBranch, &pr.TargetBranch,
			&pr.AuthorID, &pr.AuthorName, &st, &pr.MergeSHA, &created); err != nil {
			return nil, err
		}
		pr.State = store.PullRequestState(st)
		pr.CreatedAt = parseTime(created)
		out = append(out, pr)
	}
	return out, rows.Err()
}

func (s *prStore) SetState(ctx context.Context, id string, state store.PullRequestState, mergeSHA string) error {
	res, err := s.db.sql.ExecContext(ctx, `UPDATE pull_requests SET state = ?, merge_sha = ? WHERE id = ?`, string(state), mergeSHA, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *prStore) CreateComment(ctx context.Context, c *store.Comment) error {
	var line any
	if c.Line != nil {
		line = *c.Line
	}
	_, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO pr_comments (id, pr_id, author_id, body, file_path, line, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.ParentID, c.AuthorID, c.Body, c.FilePath, line, nowString(c.CreatedAt),
	)
	return mapErr(err)
}

func (s *prStore) ListComments(ctx context.Context, prID string) ([]store.Comment, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT c.id, c.pr_id, c.author_id, COALESCE(u.username, ''), c.body, COALESCE(c.file_path, ''), c.line, c.created_at
		 FROM pr_comments c LEFT JOIN users u ON u.id = c.author_id
		 WHERE c.pr_id = ? ORDER BY c.created_at`, prID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Comment
	for rows.Next() {
		var c store.Comment
		var created string
		var line sql.NullInt64
		if err := rows.Scan(&c.ID, &c.ParentID, &c.AuthorID, &c.AuthorName, &c.Body, &c.FilePath, &line, &created); err != nil {
			return nil, err
		}
		if line.Valid {
			v := int(line.Int64)
			c.Line = &v
		}
		c.CreatedAt = parseTime(created)
		out = append(out, c)
	}
	return out, rows.Err()
}

type issueStore struct{ db *DB }

func (s *issueStore) Create(ctx context.Context, issue *store.Issue) error {
	_, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO issues (id, repo_id, number, title, description, author_id, state, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		issue.ID, issue.RepoID, issue.Number, issue.Title, issue.Description, issue.AuthorID, string(issue.State), nowString(issue.CreatedAt),
	)
	return mapErr(err)
}

const issueSelect = `
SELECT i.id, i.repo_id, i.number, i.title, i.description, i.author_id, COALESCE(u.username, ''), i.state, i.created_at
FROM issues i LEFT JOIN users u ON u.id = i.author_id`

func (s *issueStore) Get(ctx context.Context, repoID string, number int) (*store.Issue, error) {
	var issue store.Issue
	var created, st string
	err := s.db.sql.QueryRowContext(ctx, issueSelect+` WHERE i.repo_id = ? AND i.number = ?`, repoID, number).
		Scan(&issue.ID, &issue.RepoID, &issue.Number, &issue.Title, &issue.Description, &issue.AuthorID, &issue.AuthorName, &st, &created)
	if err != nil {
		return nil, mapErr(err)
	}
	issue.State = store.IssueState(st)
	issue.CreatedAt = parseTime(created)
	return &issue, nil
}

func (s *issueStore) List(ctx context.Context, repoID string) ([]store.Issue, error) {
	rows, err := s.db.sql.QueryContext(ctx, issueSelect+` WHERE i.repo_id = ? ORDER BY i.number DESC`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Issue
	for rows.Next() {
		var issue store.Issue
		var created, st string
		if err := rows.Scan(&issue.ID, &issue.RepoID, &issue.Number, &issue.Title, &issue.Description, &issue.AuthorID, &issue.AuthorName, &st, &created); err != nil {
			return nil, err
		}
		issue.State = store.IssueState(st)
		issue.CreatedAt = parseTime(created)
		out = append(out, issue)
	}
	return out, rows.Err()
}

func (s *issueStore) SetState(ctx context.Context, id string, state store.IssueState) error {
	res, err := s.db.sql.ExecContext(ctx, `UPDATE issues SET state = ? WHERE id = ?`, string(state), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *issueStore) CreateComment(ctx context.Context, c *store.Comment) error {
	_, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO issue_comments (id, issue_id, author_id, body, created_at) VALUES (?, ?, ?, ?, ?)`,
		c.ID, c.ParentID, c.AuthorID, c.Body, nowString(c.CreatedAt),
	)
	return mapErr(err)
}

func (s *issueStore) ListComments(ctx context.Context, issueID string) ([]store.Comment, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT c.id, c.issue_id, c.author_id, COALESCE(u.username, ''), c.body, c.created_at
		 FROM issue_comments c LEFT JOIN users u ON u.id = c.author_id
		 WHERE c.issue_id = ? ORDER BY c.created_at`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Comment
	for rows.Next() {
		var c store.Comment
		var created string
		if err := rows.Scan(&c.ID, &c.ParentID, &c.AuthorID, &c.AuthorName, &c.Body, &created); err != nil {
			return nil, err
		}
		c.CreatedAt = parseTime(created)
		out = append(out, c)
	}
	return out, rows.Err()
}

type orgStore struct{ db *DB }

func (s *orgStore) Create(ctx context.Context, o *store.Org) error {
	_, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO orgs (id, name, creator_user_id, created_at) VALUES (?, ?, ?, ?)`,
		o.ID, o.Name, o.CreatorUserID, nowString(o.CreatedAt),
	)
	return mapErr(err)
}

func (s *orgStore) GetByID(ctx context.Context, id string) (*store.Org, error) {
	var o store.Org
	var created string
	err := s.db.sql.QueryRowContext(ctx, `SELECT id, name, creator_user_id, created_at FROM orgs WHERE id = ?`, id).
		Scan(&o.ID, &o.Name, &o.CreatorUserID, &created)
	if err != nil {
		return nil, mapErr(err)
	}
	o.CreatedAt = parseTime(created)
	return &o, nil
}

func (s *orgStore) GetByName(ctx context.Context, name string) (*store.Org, error) {
	var o store.Org
	var created string
	err := s.db.sql.QueryRowContext(ctx, `SELECT id, name, creator_user_id, created_at FROM orgs WHERE name = ? COLLATE NOCASE`, name).
		Scan(&o.ID, &o.Name, &o.CreatorUserID, &created)
	if err != nil {
		return nil, mapErr(err)
	}
	o.CreatedAt = parseTime(created)
	return &o, nil
}

func (s *orgStore) ListForUser(ctx context.Context, userID string) ([]store.Org, error) {
	rows, err := s.db.sql.QueryContext(ctx, `
		SELECT DISTINCT o.id, o.name, o.creator_user_id, o.created_at
		FROM orgs o
		LEFT JOIN teams t ON t.org_id = o.id
		LEFT JOIN team_members tm ON tm.team_id = t.id
		WHERE o.creator_user_id = ? OR tm.user_id = ?
		ORDER BY o.name`, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Org
	for rows.Next() {
		var o store.Org
		var created string
		if err := rows.Scan(&o.ID, &o.Name, &o.CreatorUserID, &created); err != nil {
			return nil, err
		}
		o.CreatedAt = parseTime(created)
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *orgStore) CreateTeam(ctx context.Context, t *store.Team) error {
	_, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO teams (id, org_id, name, permission_level, created_at) VALUES (?, ?, ?, ?, ?)`,
		t.ID, t.OrgID, t.Name, string(t.PermissionLevel), nowString(t.CreatedAt),
	)
	return mapErr(err)
}

func (s *orgStore) GetTeam(ctx context.Context, id string) (*store.Team, error) {
	var t store.Team
	var created, perm string
	err := s.db.sql.QueryRowContext(ctx, `SELECT id, org_id, name, permission_level, created_at FROM teams WHERE id = ?`, id).
		Scan(&t.ID, &t.OrgID, &t.Name, &perm, &created)
	if err != nil {
		return nil, mapErr(err)
	}
	t.PermissionLevel = store.Permission(perm)
	t.CreatedAt = parseTime(created)
	return &t, nil
}

func (s *orgStore) ListTeams(ctx context.Context, orgID string) ([]store.Team, error) {
	rows, err := s.db.sql.QueryContext(ctx, `SELECT id, org_id, name, permission_level, created_at FROM teams WHERE org_id = ? ORDER BY name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Team
	for rows.Next() {
		var t store.Team
		var created, perm string
		if err := rows.Scan(&t.ID, &t.OrgID, &t.Name, &perm, &created); err != nil {
			return nil, err
		}
		t.PermissionLevel = store.Permission(perm)
		t.CreatedAt = parseTime(created)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *orgStore) AddTeamMember(ctx context.Context, teamID, userID string) error {
	_, err := s.db.sql.ExecContext(ctx, `INSERT INTO team_members (team_id, user_id) VALUES (?, ?)`, teamID, userID)
	return mapErr(err)
}

func (s *orgStore) RemoveTeamMember(ctx context.Context, teamID, userID string) error {
	_, err := s.db.sql.ExecContext(ctx, `DELETE FROM team_members WHERE team_id = ? AND user_id = ?`, teamID, userID)
	return err
}

func (s *orgStore) ListTeamMembers(ctx context.Context, teamID string) ([]store.User, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT u.id, u.username, u.email, u.password_hash, u.created_at
		 FROM team_members tm JOIN users u ON u.id = tm.user_id WHERE tm.team_id = ? ORDER BY u.username`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.User
	for rows.Next() {
		var u store.User
		var created string
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &created); err != nil {
			return nil, err
		}
		u.CreatedAt = parseTime(created)
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *orgStore) SetRepoPermission(ctx context.Context, teamID, repoID string, level store.Permission) error {
	_, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO repo_permissions (team_id, repo_id, level) VALUES (?, ?, ?)
		 ON CONFLICT(team_id, repo_id) DO UPDATE SET level = excluded.level`,
		teamID, repoID, string(level),
	)
	return mapErr(err)
}

func (s *orgStore) ListRepoPermissions(ctx context.Context, repoID string) ([]store.RepoPermission, error) {
	rows, err := s.db.sql.QueryContext(ctx, `SELECT team_id, repo_id, level FROM repo_permissions WHERE repo_id = ?`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.RepoPermission
	for rows.Next() {
		var p store.RepoPermission
		var level string
		if err := rows.Scan(&p.TeamID, &p.RepoID, &level); err != nil {
			return nil, err
		}
		p.Level = store.Permission(level)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *orgStore) BestPermission(ctx context.Context, userID string, repo *store.Repo) (store.Permission, error) {
	if repo.OwnerType == store.OwnerUser && repo.OwnerID == userID {
		return store.PermAdmin, nil
	}
	if repo.OwnerType == store.OwnerOrg {
		var creator string
		err := s.db.sql.QueryRowContext(ctx, `SELECT creator_user_id FROM orgs WHERE id = ?`, repo.OwnerID).Scan(&creator)
		if err == nil && creator == userID {
			return store.PermAdmin, nil
		}
		var level string
		err = s.db.sql.QueryRowContext(ctx, `
			SELECT rp.level FROM repo_permissions rp
			JOIN team_members tm ON tm.team_id = rp.team_id
			WHERE rp.repo_id = ? AND tm.user_id = ?
			ORDER BY CASE rp.level WHEN 'admin' THEN 3 WHEN 'write' THEN 2 WHEN 'read' THEN 1 ELSE 0 END DESC
			LIMIT 1`, repo.ID, userID).Scan(&level)
		if err == nil {
			return store.Permission(level), nil
		}
		if !errors.Is(err, sql.ErrNoRows) && err != nil {
			return "", err
		}
	}
	return "", nil
}

type ciStore struct{ db *DB }

func (s *ciStore) CreateRunner(ctx context.Context, r *store.CIRunner) error {
	_, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO ci_runners (id, name, token_hash, last_seen_at, status, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		r.ID, r.Name, r.TokenHash, nullTime(r.LastSeenAt), r.Status, nowString(r.CreatedAt),
	)
	return mapErr(err)
}

func (s *ciStore) GetRunnerByTokenHash(ctx context.Context, hash string) (*store.CIRunner, error) {
	return s.scanRunner(s.db.sql.QueryRowContext(ctx,
		`SELECT id, name, token_hash, last_seen_at, status, created_at FROM ci_runners WHERE token_hash = ?`, hash,
	))
}

func (s *ciStore) GetRunner(ctx context.Context, id string) (*store.CIRunner, error) {
	return s.scanRunner(s.db.sql.QueryRowContext(ctx,
		`SELECT id, name, token_hash, last_seen_at, status, created_at FROM ci_runners WHERE id = ?`, id,
	))
}

func (s *ciStore) scanRunner(row *sql.Row) (*store.CIRunner, error) {
	var r store.CIRunner
	var last, created sql.NullString
	var createdStr string
	err := row.Scan(&r.ID, &r.Name, &r.TokenHash, &last, &r.Status, &createdStr)
	if err != nil {
		return nil, mapErr(err)
	}
	r.LastSeenAt = scanNullTime(last)
	r.CreatedAt = parseTime(createdStr)
	_ = created
	return &r, nil
}

func (s *ciStore) ListRunners(ctx context.Context) ([]store.CIRunner, error) {
	rows, err := s.db.sql.QueryContext(ctx, `SELECT id, name, token_hash, last_seen_at, status, created_at FROM ci_runners ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.CIRunner
	for rows.Next() {
		var r store.CIRunner
		var last sql.NullString
		var created string
		if err := rows.Scan(&r.ID, &r.Name, &r.TokenHash, &last, &r.Status, &created); err != nil {
			return nil, err
		}
		r.LastSeenAt = scanNullTime(last)
		r.CreatedAt = parseTime(created)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *ciStore) TouchRunner(ctx context.Context, id string, now time.Time) error {
	_, err := s.db.sql.ExecContext(ctx, `UPDATE ci_runners SET last_seen_at = ?, status = 'online' WHERE id = ?`, nowString(now), id)
	return err
}

func (s *ciStore) CreateJob(ctx context.Context, j *store.CIJob) error {
	_, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO ci_jobs (id, repo_id, commit_sha, pr_id, name, status, runner_id, started_at, finished_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, j.RepoID, j.CommitSHA, j.PRID, j.Name, string(j.Status), j.RunnerID, nullTime(j.StartedAt), nullTime(j.FinishedAt), nowString(j.CreatedAt),
	)
	return mapErr(err)
}

const jobSelect = `SELECT j.id, j.repo_id, j.commit_sha, j.pr_id, j.name, j.status, j.runner_id, j.started_at, j.finished_at, j.created_at,
	COALESCE((SELECT GROUP_CONCAT(chunk, '') FROM ci_logs WHERE job_id = j.id), '') FROM ci_jobs j`

func (s *ciStore) scanJob(row scanner) (*store.CIJob, error) {
	var j store.CIJob
	var pr, runner sql.NullString
	var started, finished sql.NullString
	var created, status string
	if err := row.Scan(&j.ID, &j.RepoID, &j.CommitSHA, &pr, &j.Name, &status, &runner, &started, &finished, &created, &j.LogText); err != nil {
		return nil, mapErr(err)
	}
	j.Status = store.CIJobStatus(status)
	if pr.Valid {
		j.PRID = &pr.String
	}
	if runner.Valid {
		j.RunnerID = &runner.String
	}
	j.StartedAt = scanNullTime(started)
	j.FinishedAt = scanNullTime(finished)
	j.CreatedAt = parseTime(created)
	return &j, nil
}

func (s *ciStore) GetJob(ctx context.Context, id string) (*store.CIJob, error) {
	return s.scanJob(s.db.sql.QueryRowContext(ctx, jobSelect+` WHERE j.id = ?`, id))
}

func (s *ciStore) ListJobs(ctx context.Context, repoID string) ([]store.CIJob, error) {
	rows, err := s.db.sql.QueryContext(ctx, jobSelect+` WHERE j.repo_id = ? ORDER BY j.created_at DESC`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

func (s *ciStore) ListJobsByPR(ctx context.Context, prID string) ([]store.CIJob, error) {
	rows, err := s.db.sql.QueryContext(ctx, jobSelect+` WHERE j.pr_id = ? ORDER BY j.created_at DESC`, prID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

func scanJobs(rows *sql.Rows) ([]store.CIJob, error) {
	var out []store.CIJob
	for rows.Next() {
		var j store.CIJob
		var pr, runner sql.NullString
		var started, finished sql.NullString
		var created, status string
		if err := rows.Scan(&j.ID, &j.RepoID, &j.CommitSHA, &pr, &j.Name, &status, &runner, &started, &finished, &created, &j.LogText); err != nil {
			return nil, err
		}
		j.Status = store.CIJobStatus(status)
		if pr.Valid {
			j.PRID = &pr.String
		}
		if runner.Valid {
			j.RunnerID = &runner.String
		}
		j.StartedAt = scanNullTime(started)
		j.FinishedAt = scanNullTime(finished)
		j.CreatedAt = parseTime(created)
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *ciStore) ClaimQueuedJob(ctx context.Context, runnerID string, now time.Time) (*store.CIJob, error) {
	tx, err := s.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id FROM ci_jobs WHERE status = 'queued' ORDER BY created_at LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE ci_jobs SET status = 'running', runner_id = ?, started_at = ? WHERE id = ? AND status = 'queued'`,
		runnerID, nowString(now), id,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetJob(ctx, id)
}

func (s *ciStore) AppendLog(ctx context.Context, jobID, chunk string) error {
	_, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO ci_logs (id, job_id, chunk, created_at) VALUES (?, ?, ?, ?)`,
		fmt.Sprintf("%s-%d", jobID, time.Now().UnixNano()), jobID, chunk, nowString(time.Now()),
	)
	return err
}

func (s *ciStore) FinishJob(ctx context.Context, id string, status store.CIJobStatus, now time.Time) error {
	_, err := s.db.sql.ExecContext(ctx, `UPDATE ci_jobs SET status = ?, finished_at = ? WHERE id = ?`, string(status), nowString(now), id)
	return err
}

func (s *ciStore) LatestForSHA(ctx context.Context, repoID, sha string) ([]store.CIJob, error) {
	rows, err := s.db.sql.QueryContext(ctx, jobSelect+` WHERE j.repo_id = ? AND j.commit_sha = ? ORDER BY j.created_at DESC`, repoID, sha)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}
