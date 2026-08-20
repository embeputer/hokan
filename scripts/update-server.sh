#!/usr/bin/env bash
# Update an existing Hokan server: git pull, rebuild, restart. Leaves data and env alone.
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
. "$SCRIPT_DIR/lib.sh"

SOURCE=""
PREFIX=""
NO_PULL=0
INSTALL_CLI=""

usage() {
	cat <<'EOF'
Update an existing Hokan server install.

Pulls new commits (if the source is a git checkout), rebuilds hokan-server,
replaces the binary, and restarts systemd. Does not touch hokan.env, repos,
or the database (the server applies migrations on start).

Usage:
  ./scripts/update-server.sh [options]

Options:
  --prefix DIR        Existing install prefix (looks for DIR/hokan.env)
  --source PATH       Git checkout to pull/build (default: this repo)
  --no-pull           Rebuild the current tree; do not git pull
  --with-cli          Also rebuild the CLI into PREFIX/bin
  --no-cli            Do not touch the CLI
  --yes, -y           Do not prompt
  --dry-run           Print the plan; do not write files
  --help, -h          Show this help

Examples:
  ./scripts/update-server.sh
  ./scripts/update-server.sh --prefix ~/hokan --yes
  sudo ./scripts/update-server.sh --prefix /var/lib/hokan --yes
EOF
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	-h | --help)
		usage
		exit 0
		;;
	-y | --yes)
		YES=1
		shift
		;;
	--dry-run)
		DRY_RUN=1
		shift
		;;
	--no-pull)
		NO_PULL=1
		shift
		;;
	--prefix)
		PREFIX=$2
		shift 2
		;;
	--prefix=*)
		PREFIX=${1#*=}
		shift
		;;
	--source)
		SOURCE=$2
		shift 2
		;;
	--source=*)
		SOURCE=${1#*=}
		shift
		;;
	--with-cli)
		INSTALL_CLI=yes
		shift
		;;
	--no-cli)
		INSTALL_CLI=no
		shift
		;;
	*)
		hokan_die "unknown flag: $1
  $0 --help"
		;;
	esac
done

hokan_detect_os_arch
hokan_info "Hokan server updater ($HOKAN_OS/$HOKAN_ARCH)"

hokan_resolve_src "$SCRIPT_DIR"
hokan_find_server_prefix
hokan_detect_unit

BIN_DIR="$PREFIX/bin"
ENV_FILE="$PREFIX/hokan.env"
[[ -x "$BIN_DIR/hokan-server" || -e "$BIN_DIR/hokan-server" ]] || hokan_die "missing $BIN_DIR/hokan-server"

if [[ -z "$INSTALL_CLI" ]]; then
	if [[ -e "$BIN_DIR/hokan" ]]; then
		INSTALL_CLI=yes
	else
		INSTALL_CLI=no
	fi
fi

if [[ ! -w "$BIN_DIR" && "$DRY_RUN" != 1 ]]; then
	hokan_die "cannot write $BIN_DIR (run as the install user, or sudo)"
fi

hokan_need_cmd git
hokan_ensure_go

hokan_log ""
hokan_info "Plan"
hokan_log "  source:  $HOKAN_SRC"
hokan_log "  prefix:  $PREFIX"
hokan_log "  env:     $ENV_FILE (unchanged)"
hokan_log "  unit:    $UNIT"
hokan_log "  pull:    $([[ "$NO_PULL" == 1 ]] && echo no || echo yes)"
hokan_log "  cli:     $INSTALL_CLI"
hokan_log ""

if ! hokan_confirm "Proceed with update?" Y; then
	hokan_die "aborted"
fi

if [[ "$NO_PULL" != 1 ]]; then
	hokan_git_pull "$HOKAN_SRC"
fi

build_server="${TMPDIR:-/tmp}/hokan-server-$$"
hokan_build ./cmd/hokan-server "$build_server"
hokan_install_bin "$build_server" "$BIN_DIR/hokan-server"
if [[ "$DRY_RUN" != 1 ]]; then
	rm -f "$build_server"
fi

if [[ "$INSTALL_CLI" == [Yy]* ]]; then
	build_cli="${TMPDIR:-/tmp}/hokan-cli-$$"
	hokan_build ./cmd/hokan "$build_cli"
	hokan_install_bin "$build_cli" "$BIN_DIR/hokan"
	if [[ "$DRY_RUN" != 1 ]]; then
		rm -f "$build_cli"
		ln -sfn hokan "$BIN_DIR/hkn"
	fi
fi

hokan_restart_server

http_addr=$(hokan_env_get HOKAN_HTTP_ADDR "$ENV_FILE")
base_url=$(hokan_env_get HOKAN_BASE_URL "$ENV_FILE")
if [[ -n "$http_addr" && "$UNIT" != none ]]; then
	hokan_wait_healthz "$(hokan_http_health_url "$http_addr")"
fi

hokan_info "Hokan server updated"
[[ -n "$base_url" ]] && hokan_log "  ui:     $base_url"
hokan_log "  binary: $BIN_DIR/hokan-server"
hokan_log "  env:    $ENV_FILE (unchanged)"
if [[ "$UNIT" == none ]]; then
	hokan_log "  restart the running process to pick up the new binary"
fi
