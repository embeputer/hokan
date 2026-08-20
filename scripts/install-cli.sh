#!/usr/bin/env bash
# Install the Hokan CLI on this machine (laptop, CI, or server).
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
. "$SCRIPT_DIR/lib.sh"

BIN_DIR=""
SOURCE=""
SERVER=""
ALIAS=hkn

usage() {
	cat <<'EOF'
Install the Hokan CLI (talks only to a Hokan HTTP API).

Usage:
  ./scripts/install-cli.sh [options]

Options:
  --source PATH|URL   Hokan git checkout, or a git URL to clone
  --bin-dir DIR       Install directory (default: ~/.local/bin)
  --server URL        Save this as the default Hokan server
  --alias NAME        Extra symlink name (default: hkn; empty to skip)
  --yes, -y           Accept defaults; do not prompt
  --dry-run           Print the plan; do not write files
  --help, -h          Show this help

Examples:
  ./scripts/install-cli.sh
  ./scripts/install-cli.sh --yes --bin-dir ~/.local/bin --server https://git.example.com
  ./scripts/install-cli.sh --source https://github.com/hokan/hokan.git --yes
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
	--source)
		SOURCE=$2
		shift 2
		;;
	--source=*)
		SOURCE=${1#*=}
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
	--server)
		SERVER=$2
		shift 2
		;;
	--server=*)
		SERVER=${1#*=}
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
hokan_info "Hokan CLI installer ($HOKAN_OS/$HOKAN_ARCH)"

default_bin="$HOME/.local/bin"
if [[ "$(id -u)" == 0 ]]; then
	default_bin=/usr/local/bin
fi
hokan_ask BIN_DIR "Install directory" "$default_bin"
hokan_ask SERVER "Default Hokan server URL (empty to skip)" ""
if [[ "$YES" != 1 ]] && hokan_is_tty; then
	if ! hokan_confirm "Also install '$ALIAS' as a short name?" Y; then
		ALIAS=""
	fi
fi

hokan_resolve_src "$SCRIPT_DIR"
hokan_need_cmd git
hokan_ensure_go

build_out="${TMPDIR:-/tmp}/hokan-cli-$$"
hokan_log ""
hokan_info "Plan"
hokan_log "  source:  $HOKAN_SRC"
hokan_log "  build:   $HOKAN_SRC/cmd/hokan"
hokan_log "  install: $BIN_DIR/hokan"
if [[ -n "$ALIAS" ]]; then
	hokan_log "  alias:   $BIN_DIR/$ALIAS -> hokan"
fi
if [[ -n "$SERVER" ]]; then
	hokan_log "  server:  $SERVER (saved in ~/.config/hokan/config.json)"
fi
hokan_log ""

if ! hokan_confirm "Proceed?" Y; then
	hokan_die "aborted"
fi

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

if [[ -n "$SERVER" ]]; then
	cfg="${XDG_CONFIG_HOME:-$HOME/.config}/hokan/config.json"
	if [[ -f "$cfg" && "$DRY_RUN" != 1 ]]; then
		hokan_warn "leaving existing $cfg in place"
	else
		hokan_write_file "$cfg" 600 <<EOF
{"server":"${SERVER//\"/\\\"}","token":""}
EOF
	fi
fi

hokan_maybe_path_hint "$BIN_DIR"
hokan_info "CLI installed: $BIN_DIR/hokan"
hokan_log ""
hokan_log "Next:"
if [[ -n "$SERVER" ]]; then
	hokan_log "  hokan auth login"
else
	hokan_log "  hokan --server https://your-hokan.example auth login"
	hokan_log "  # or: export HOKAN_SERVER=https://your-hokan.example"
fi
hokan_log "  hokan repo list"
