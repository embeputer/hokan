# Hokan

Self-hosted Git forge in Go. Git hosting, a web UI, pull requests, issues, and CI — no required third-party services.

## Quickstart

Requirements: Go 1.22+, a `git` binary on `PATH`. Docker is required only for `hokan-runner`.

```bash
make build
export HOKAN_DATA_DIR=./data/repos
export HOKAN_DB_PATH=./data/hokan.db
./dist/hokan-server
```

The server listens on `:8080` (HTTP) and `:2222` (SSH) by default. Open http://localhost:8080, create the first user, then create a repository.

```bash
./dist/hokan --server http://localhost:8080 auth login
./dist/hokan repo create hello
git clone http://localhost:8080/<username>/hello.git
```

Push over SSH after adding a public key in **Settings → SSH keys**:

```bash
git clone ssh://git@localhost:2222/<username>/hello.git
```

Signup is open by default (`HOKAN_ALLOW_SIGNUP=true`). After you have users, set `HOKAN_ALLOW_SIGNUP=false` to disable public registration (the first user can still be created on an empty database).

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `HOKAN_DATA_DIR` | `./data/repos` | Bare Git repos |
| `HOKAN_DB_PATH` | `./data/hokan.db` | SQLite file |
| `HOKAN_HTTP_ADDR` | `:8080` | HTTP bind address |
| `HOKAN_SSH_ADDR` | `:2222` | SSH bind address |
| `HOKAN_BASE_URL` | `http://localhost:8080` | Clone URLs in the UI |
| `HOKAN_SSH_HOST_KEY` | `./data/ssh_host_key` | SSH host key (created on first run) |
| `HOKAN_ALLOW_SIGNUP` | `true` | Public signup |

The CLI uses `HOKAN_SERVER` and `HOKAN_TOKEN` (or `hokan auth login`, which stores a token in `~/.config/hokan/config.json`).

## Install

From a clone. Both scripts prompt; pass `--yes` plus flags for unattended runs.

**CLI** (any Linux or macOS machine):

```bash
./scripts/install-cli.sh
# or:
./scripts/install-cli.sh --yes --server https://git.example.com --bin-dir ~/.local/bin
```

**Server** (guided deploy: build, data dir, env, systemd):

```bash
./scripts/install-server.sh
# as root, system service:
sudo ./scripts/install-server.sh --yes --base-url https://git.example.com
```

`--help` on either script lists every flag. `--dry-run` prints the plan without writing files.

## CI runner

```bash
# as a logged-in user, create a runner token via POST /api/v1/ci/runners
export HOKAN_SERVER=http://localhost:8080
export HOKAN_RUNNER_TOKEN=hokr_...
./dist/hokan-runner
```

Jobs are defined in-repo:

```yaml
jobs:
  build:
    image: golang:1.22
    steps:
      - run: go test ./...
triggers: [push, pull_request]
```

See [docs/architecture.md](docs/architecture.md) for transport details and the CI trust model (push access means arbitrary code on the runner host).
