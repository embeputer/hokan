#!/usr/bin/env bash
# Guided install of the Hokan server (binary, data dir, systemd, health check).
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
. "$SCRIPT_DIR/lib.sh"

SOURCE=""
PREFIX=""
HTTP_ADDR=""
SSH_ADDR=""
BASE_URL=""
ALLOW_SIGNUP=""
UNIT=""
INSTALL_CLI=""
LINGER=""
RUN_USER=""

usage() {
	cat <<'EOF'
Install and start the Hokan Git forge on this machine.

Walks through source, listen addresses, public URL, data directory,
and a systemd unit. Safe to re-run (rebuilds the binary and restarts).

Usage:
  ./scripts/install-server.sh [options]

Options:
  --source PATH|URL     Checkout to build, or a git URL to clone
  --prefix DIR          Install prefix
                        default as root: /var/lib/hokan
                        default as user: ~/hokan
  --http-addr ADDR      HTTP bind (default: 127.0.0.1:8080)
  --ssh-addr ADDR       Git SSH bind (default: :2222)
  --base-url URL        Public URL used in clone links
  --allow-signup BOOL   Public registration (default: true)
  --unit user|system|none
                        systemd unit type (default: user, or system if root)
  --with-cli / --no-cli Also install the Hokan CLI (default: yes)
  --linger / --no-linger
                        For --unit user: enable lingering so the server
                        survives logout (needs sudo)
  --yes, -y             Accept defaults; do not prompt
  --dry-run             Print the plan; do not write files
  --help, -h            Show this help

Examples:
  ./scripts/install-server.sh
  sudo ./scripts/install-server.sh --yes --base-url https://git.example.com
  ./scripts/install-server.sh --unit user --http-addr 0.0.0.0:8080 --base-url http://tsuchi:8080
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
	--prefix)
		PREFIX=$2
		shift 2
		;;
	--prefix=*)
		PREFIX=${1#*=}
		shift
		;;
	--http-addr)
		HTTP_ADDR=$2
		shift 2
		;;
	--http-addr=*)
		HTTP_ADDR=${1#*=}
		shift
		;;
	--ssh-addr)
		SSH_ADDR=$2
		shift 2
		;;
	--ssh-addr=*)
		SSH_ADDR=${1#*=}
		shift
		;;
	--base-url)
		BASE_URL=$2
		shift 2
		;;
	--base-url=*)
		BASE_URL=${1#*=}
		shift
		;;
	--allow-signup)
		ALLOW_SIGNUP=$2
		shift 2
		;;
	--allow-signup=*)
		ALLOW_SIGNUP=${1#*=}
		shift
		;;
	--unit)
		UNIT=$2
		shift 2
		;;
	--unit=*)
		UNIT=${1#*=}
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
	--linger)
		LINGER=yes
		shift
		;;
	--no-linger)
		LINGER=no
		shift
		;;
	*)
		hokan_die "unknown flag: $1
  $0 --help"
		;;
	esac
done

hokan_detect_os_arch
hokan_info "Hokan server installer ($HOKAN_OS/$HOKAN_ARCH)"

as_root=0
if [[ "$(id -u)" == 0 ]]; then
	as_root=1
fi

if [[ "$as_root" == 1 ]]; then
	default_prefix=/var/lib/hokan
	default_unit=system
	RUN_USER=hokan
else
	default_prefix="$HOME/hokan"
	default_unit=user
	RUN_USER=$(id -un)
fi
if ! command -v systemctl >/dev/null 2>&1; then
	default_unit=none
fi

guess_base=http://127.0.0.1:8080
if command -v hostname >/dev/null 2>&1; then
	guess_base="http://$(hostname):8080"
fi

hokan_ask PREFIX "Install prefix (bin + data)" "$default_prefix"
hokan_ask HTTP_ADDR "HTTP listen address" "127.0.0.1:8080"
hokan_ask SSH_ADDR "Git SSH listen address" ":2222"
hokan_ask BASE_URL "Public base URL (clone links in the UI)" "$guess_base"
hokan_ask ALLOW_SIGNUP "Allow public signup? (true/false)" "true"
case "$ALLOW_SIGNUP" in
true | TRUE | yes | Y | y | 1) ALLOW_SIGNUP=true ;;
false | FALSE | no | N | n | 0) ALLOW_SIGNUP=false ;;
*) hokan_die "--allow-signup must be true or false (got $ALLOW_SIGNUP)" ;;
esac
hokan_ask UNIT "systemd unit (user / system / none)" "$default_unit"
hokan_ask INSTALL_CLI "Also install the CLI into $PREFIX/bin?" "yes"

case "$UNIT" in
user | system | none) ;;
*) hokan_die "--unit must be user, system, or none" ;;
esac
if [[ "$UNIT" == system && "$as_root" != 1 ]]; then
	hokan_die "--unit system needs to run as root (sudo $0 --unit system ...)"
fi
if [[ "$UNIT" == user && "$as_root" == 1 ]]; then
	hokan_die "--unit user cannot run as root; drop sudo or use --unit system"
fi
if [[ "$HOKAN_OS" != linux && "$UNIT" != none ]]; then
	hokan_warn "systemd is a Linux thing; using --unit none on $HOKAN_OS"
	UNIT=none
fi

if [[ "$UNIT" == user ]]; then
	hokan_ask LINGER "Keep running after logout? (enable-linger, needs sudo)" "yes"
fi

BIN_DIR="$PREFIX/bin"
DATA_DIR="$PREFIX/data"
ENV_FILE="$PREFIX/hokan.env"
HOST_KEY="$DATA_DIR/ssh_host_key"
DB_PATH="$DATA_DIR/hokan.db"
REPOS_DIR="$DATA_DIR/repos"

hokan_resolve_src "$SCRIPT_DIR"
hokan_need_cmd git
if [[ "$UNIT" != none ]]; then
	hokan_need_cmd systemctl
fi
hokan_ensure_go

hokan_log ""
hokan_info "Plan"
hokan_log "  source:     $HOKAN_SRC"
hokan_log "  prefix:     $PREFIX"
hokan_log "  run as:     $RUN_USER"
hokan_log "  http:       $HTTP_ADDR"
hokan_log "  git ssh:    $SSH_ADDR"
hokan_log "  base url:   $BASE_URL"
hokan_log "  signup:     $ALLOW_SIGNUP"
hokan_log "  data:       $REPOS_DIR"
hokan_log "  env:        $ENV_FILE"
hokan_log "  unit:       $UNIT"
if [[ "$UNIT" == user ]]; then
	hokan_log "  linger:     $LINGER"
fi
hokan_log "  cli:        $INSTALL_CLI"
hokan_log ""
hokan_warn "Git SSH is Hokan's own server on $SSH_ADDR (not system sshd on :22)."
hokan_log ""

if [[ -f "$ENV_FILE" ]]; then
	hokan_warn "$ENV_FILE already exists"
	hokan_log "  Upgrades that keep your data/config: ./scripts/update-server.sh --prefix $PREFIX"
	if ! hokan_confirm "Overwrite env/unit files and continue as a reinstall?" N; then
		hokan_die "aborted. Use ./scripts/update-server.sh --prefix $PREFIX"
	fi
fi

if [[ "$DRY_RUN" != 1 ]]; then
	mkdir -p "$BIN_DIR" "$REPOS_DIR"
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

hokan_write_file "$ENV_FILE" 600 <<EOF
HOKAN_DATA_DIR=$REPOS_DIR
HOKAN_DB_PATH=$DB_PATH
HOKAN_HTTP_ADDR=$HTTP_ADDR
HOKAN_SSH_ADDR=$SSH_ADDR
HOKAN_BASE_URL=$BASE_URL
HOKAN_SSH_HOST_KEY=$HOST_KEY
HOKAN_ALLOW_SIGNUP=$ALLOW_SIGNUP
EOF

ensure_system_user() {
	if id "$RUN_USER" >/dev/null 2>&1; then
		return 0
	fi
	hokan_info "creating system user $RUN_USER"
	local nologin=/usr/sbin/nologin
	[[ -x /sbin/nologin ]] && nologin=/sbin/nologin
	if command -v useradd >/dev/null 2>&1; then
		hokan_run useradd --system --home "$PREFIX" --shell "$nologin" "$RUN_USER"
	else
		hokan_die "need useradd to create $RUN_USER"
	fi
}

case "$UNIT" in
system)
	ensure_system_user
	if [[ "$DRY_RUN" != 1 ]]; then
		chown -R "$RUN_USER:$RUN_USER" "$PREFIX"
	else
		hokan_log "dry-run: chown -R $RUN_USER $PREFIX"
	fi
	hokan_write_file /etc/systemd/system/hokan.service 644 <<EOF
[Unit]
Description=Hokan Git forge
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$RUN_USER
Group=$RUN_USER
WorkingDirectory=$PREFIX
EnvironmentFile=$ENV_FILE
ExecStart=$BIN_DIR/hokan-server
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
	hokan_run systemctl daemon-reload
	hokan_run systemctl enable --now hokan.service
	;;
user)
	unit_dir="$HOME/.config/systemd/user"
	if [[ "$DRY_RUN" != 1 ]]; then
		mkdir -p "$unit_dir"
	fi
	hokan_write_file "$unit_dir/hokan.service" 644 <<EOF
[Unit]
Description=Hokan Git forge
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=$PREFIX
EnvironmentFile=$ENV_FILE
ExecStart=$BIN_DIR/hokan-server
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
EOF
	hokan_run systemctl --user daemon-reload
	hokan_run systemctl --user enable --now hokan.service
	if [[ "$LINGER" == [Yy]* ]]; then
		if loginctl show-user "$RUN_USER" -p Linger 2>/dev/null | grep -qx Linger=yes; then
			hokan_info "linger already enabled"
		elif [[ "$DRY_RUN" == 1 ]]; then
			hokan_log "dry-run: loginctl enable-linger $RUN_USER"
		elif sudo -n loginctl enable-linger "$RUN_USER" 2>/dev/null; then
			hokan_info "linger enabled"
		else
			hokan_warn "loginctl enable-linger needs sudo so the server survives logout"
			hokan_log "  sudo loginctl enable-linger $RUN_USER"
			if [[ "$YES" != 1 ]] && hokan_is_tty && hokan_confirm "Run that sudo command now?" Y; then
				sudo loginctl enable-linger "$RUN_USER"
			fi
		fi
	else
		hokan_warn "without linger, logging out stops the server"
	fi
	;;
none)
	hokan_info "no systemd unit; start with:"
	hokan_log "  set -a; . $ENV_FILE; set +a"
	hokan_log "  $BIN_DIR/hokan-server"
	if [[ "$DRY_RUN" != 1 ]]; then
		hokan_warn "leaving the process to you (--unit none)"
	fi
	;;
esac

health=$(hokan_http_health_url "$HTTP_ADDR")
if [[ "$UNIT" != none ]]; then
	hokan_wait_healthz "$health"
fi

hokan_maybe_path_hint "$BIN_DIR"

hokan_info "Hokan server installed"
hokan_log "  ui:      $BASE_URL"
hokan_log "  health:  $health"
hokan_log "  env:     $ENV_FILE"
hokan_log "  binary:  $BIN_DIR/hokan-server"
hokan_log ""
hokan_log "Next:"
hokan_log "  Open $BASE_URL and create the first user"
hokan_log "  After you have users, set HOKAN_ALLOW_SIGNUP=false in $ENV_FILE and restart"
if [[ "$UNIT" == user ]]; then
	hokan_log "  Restart: systemctl --user restart hokan"
elif [[ "$UNIT" == system ]]; then
	hokan_log "  Restart: sudo systemctl restart hokan"
fi
if [[ "$INSTALL_CLI" == [Yy]* ]]; then
	hokan_log "  Login: $BIN_DIR/hokan --server $BASE_URL auth login"
fi
hokan_log "  Later: ./scripts/update-server.sh --prefix $PREFIX"
hokan_log ""
hokan_log "Uninstall:"
if [[ "$UNIT" == user ]]; then
	hokan_log "  systemctl --user disable --now hokan"
	hokan_log "  rm -f $HOME/.config/systemd/user/hokan.service"
elif [[ "$UNIT" == system ]]; then
	hokan_log "  sudo systemctl disable --now hokan"
	hokan_log "  sudo rm -f /etc/systemd/system/hokan.service"
fi
hokan_log "  rm -rf $PREFIX"
