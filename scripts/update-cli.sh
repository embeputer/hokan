#!/usr/bin/env bash
# Update an existing Hokan CLI: git pull, rebuild. Leaves ~/.config/hokan alone.
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
. "$SCRIPT_DIR/lib.sh"

SOURCE=""
BIN_DIR=""
NO_PULL=0
ALIAS=hkn

usage() {
	cat <<'EOF'
Update an existing Hokan CLI install.

Pulls new commits (if the source is a git checkout), rebuilds hokan, and
replaces the binary. Does not change ~/.config/hokan/config.json.

Usage:
  ./scripts/update-cli.sh [options]

Options:
  --bin-dir DIR       Install directory (default: existing hokan on PATH,
                      else ~/.local/bin)
  --source PATH       Git checkout to pull/build (default: this repo)
  --alias NAME        Extra symlink (default: hkn; empty to skip)
  --no-pull           Rebuild the current tree; do not git pull
  --yes, -y           Do not prompt
  --dry-run           Print the plan; do not write files
  --help, -h          Show this help

Examples:
  ./scripts/update-cli.sh
  ./scripts/update-cli.sh --yes --bin-dir ~/.local/bin
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
	--bin-dir)
		BIN_DIR=$2
		shift 2
		;;
	--bin-dir=*)
		BIN_DIR=${1#*=}
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
	--alias)
		ALIAS=$2
		shift 2
		;;
	--alias=*)
		ALIAS=${1#*=}
		shift
		;;
	*)
		hokan_die "unknown flag: $1
  $0 --help"
		;;
	esac
done

hokan_detect_os_arch
hokan_info "Hokan CLI updater ($HOKAN_OS/$HOKAN_ARCH)"

if [[ -z "$BIN_DIR" ]]; then
	if command -v hokan >/dev/null 2>&1; then
		BIN_DIR=$(dirname "$(command -v hokan)")
	elif [[ -x "$HOME/.local/bin/hokan" ]]; then
		BIN_DIR="$HOME/.local/bin"
	elif [[ -x /usr/local/bin/hokan ]]; then
		BIN_DIR=/usr/local/bin
	else
		BIN_DIR="$HOME/.local/bin"
	fi
fi

hokan_resolve_src "$SCRIPT_DIR"
hokan_need_cmd git
hokan_ensure_go

hokan_log ""
hokan_info "Plan"
hokan_log "  source:  $HOKAN_SRC"
hokan_log "  install: $BIN_DIR/hokan"
hokan_log "  pull:    $([[ "$NO_PULL" == 1 ]] && echo no || echo yes)"
hokan_log "  config:  ~/.config/hokan/config.json (unchanged)"
hokan_log ""

if ! hokan_confirm "Proceed with update?" Y; then
	hokan_die "aborted"
fi

if [[ "$NO_PULL" != 1 ]]; then
	hokan_git_pull "$HOKAN_SRC"
fi

build_out="${TMPDIR:-/tmp}/hokan-cli-$$"
hokan_build ./cmd/hokan "$build_out"
hokan_install_bin "$build_out" "$BIN_DIR/hokan"
if [[ "$DRY_RUN" != 1 ]]; then
	rm -f "$build_out"
fi
if [[ -n "$ALIAS" ]]; then
	if [[ "$DRY_RUN" == 1 ]]; then
		hokan_log "dry-run: ln -sfn hokan $BIN_DIR/$ALIAS"
	else
		ln -sfn hokan "$BIN_DIR/$ALIAS"
	fi
fi

hokan_maybe_path_hint "$BIN_DIR"
hokan_info "CLI updated: $BIN_DIR/hokan"
