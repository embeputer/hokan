# Hokan architecture

Hokan is a self-hosted Git forge. One operator runs a small set of binaries: `hokan-server`, the `hokan` CLI, and optionally `hokan-runner`. There is no required third-party SaaS.

## Binaries

- **hokan-server** — HTTP API, server-rendered web UI, smart Git HTTP, SSH Git server, job queue.
- **hokan** — CLI that talks only to `/api/v1`. It never opens the database or the repo disk.
- **hokan-runner** — polls the server for CI jobs and executes them in Docker.

## Storage

```
{HOKAN_DATA_DIR}/{owner}/{repo}.git   # bare Git repositories
{HOKAN_DB_PATH}                       # SQLite metadata
```

Metadata is accessed through `internal/store`. The only implementation in v1 is SQLite (`internal/store/sqlite`). Schema changes live in `migrations/*.sql` and are applied by goose in `cmd/hokan-server` **before** the store is opened. The `Store` interface has no `Migrate` method — migrations are an infra concern.

Postgres is a future backend behind the same interface; it is not implemented.

## Request paths

```
Browser  --html/htmx-->  internal/web  --> store + git disk
CLI      --JSON-------->  /api/v1       --> store + git disk
git HTTP --smart HTTP-->  internal/git/http.go  --> system git
git SSH  --ssh--------->  internal/git/ssh.go   --> system git
runner   --long-poll--->  /api/v1/ci/jobs/wait  --> Docker
```

Git pack protocol is not reimplemented. The server shells out to `git upload-pack` / `git receive-pack`.

Auth is resolved in one place (`internal/auth`) so HTTP and SSH cannot drift:

- Web UI: httpOnly session cookie
- API / CLI: `Authorization: Bearer` (session token or PAT)
- Git HTTP: Basic (password or PAT)
- Git SSH: public key fingerprint

`CanRead` / `CanWrite` / `CanAdmin` are shared. User-owned repos: the owner is admin. Public repos are readable by anyone. Org repos use team `repo_permissions` (read / write / admin); the org creator is admin.

## CI

On a successful `git-receive-pack`, and when a pull request is opened, the server reads `.hokan/ci.yml` from the new commit and enqueues jobs. Runners authenticate with a runner token and long-poll `GET /api/v1/ci/jobs/wait` (about 30 seconds, then retry). There is no SSE in v1.

### CI runner trust model

Anyone with **push access** to a repository can author `.hokan/ci.yml` and cause **arbitrary shell commands** to run inside a Docker container on the host that runs `hokan-runner`.

That is an accepted v1 trade-off for a single-operator, self-hosted forge. It is not a bug. Do not attach a runner to a Hokan instance whose writers you do not trust, and do not treat runner hosts as untrusted-multi-tenant infrastructure.

The server itself does not need Docker; only `hokan-runner` does.

## Pull request merge

v1 supports merge commits only, via a temporary `git worktree`. If `git merge` reports a conflict, the merge is aborted, the bare repository is left unchanged, and the API/UI returns a clear error. The pull request stays open.
