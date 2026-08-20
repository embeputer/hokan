# Shared helpers for Hokan install scripts. Sourced, not executed.

HOKAN_GO_MIN="${HOKAN_GO_MIN:-1.26}"
HOKAN_GO_BOOTSTRAP="${HOKAN_GO_BOOTSTRAP:-1.26.7}"
HOKAN_MODULE="${HOKAN_MODULE:-github.com/hokan/hokan}"

YES="${YES:-0}"
DRY_RUN="${DRY_RUN:-0}"

if [[ -t 1 ]]; then
	HOKAN_BOLD=$'\033[1m'
	HOKAN_DIM=$'\033[2m'
	HOKAN_RED=$'\033[31m'
	HOKAN_RESET=$'\033[0m'
else
	HOKAN_BOLD="" HOKAN_DIM="" HOKAN_RED="" HOKAN_RESET=""
fi

hokan_log() { printf '%s\n' "$*"; }
hokan_info() { printf '%s==>%s %s\n' "$HOKAN_BOLD" "$HOKAN_RESET" "$*"; }
hokan_warn() { printf '%snote:%s %s\n' "$HOKAN_DIM" "$HOKAN_RESET" "$*" >&2; }
hokan_err() { printf '%serror:%s %s\n' "$HOKAN_RED" "$HOKAN_RESET" "$*" >&2; }
hokan_die() { hokan_err "$*"; exit 1; }

hokan_run() {
	if [[ "$DRY_RUN" == 1 ]]; then
		printf 'dry-run: %s\n' "$*"
		return 0
	fi
	"$@"
}

hokan_write_file() {
	local path="$1" mode="${2:-644}"
	local dir content
	dir=$(dirname "$path")
	content=$(cat)
	if [[ "$DRY_RUN" == 1 ]]; then
		printf 'dry-run: write %s (mode %s)\n' "$path" "$mode"
		return 0
	fi
	mkdir -p "$dir"
	printf '%s' "$content" >"$path"
	chmod "$mode" "$path"
}

hokan_is_tty() { [[ -t 0 && -t 1 ]]; }

# hokan_ask VAR "prompt" "default"
hokan_ask() {
	local var="$1" prompt="$2" default="${3-}"
	local current reply
	eval "current=\${$var-}"
	if [[ -n "$current" ]]; then
		return 0
	fi
	if [[ "$YES" == 1 ]]; then
		printf -v "$var" '%s' "$default"
		return 0
	fi
	if ! hokan_is_tty; then
		hokan_die "missing $var (no tty). Pass --$(_hokan_flag_from_var "$var") or $var=... and --yes
  Example: $0 --yes --$(_hokan_flag_from_var "$var") '$default'"
	fi
	if [[ -n "$default" ]]; then
		read -r -p "$prompt [$default]: " reply || true
	else
		read -r -p "$prompt: " reply || true
	fi
	printf -v "$var" '%s' "${reply:-$default}"
}

# hokan_confirm "question" Y|N  -> returns 0 for yes
hokan_confirm() {
	local prompt="$1" default="${2:-Y}" reply
	if [[ "$YES" == 1 ]]; then
		[[ "$default" == [Yy] ]]
		return
	fi
	if ! hokan_is_tty; then
		[[ "$default" == [Yy] ]]
		return
	fi
	if [[ "$default" == [Yy] ]]; then
		read -r -p "$prompt [Y/n]: " reply || true
		reply=${reply:-Y}
	else
		read -r -p "$prompt [y/N]: " reply || true
		reply=${reply:-N}
	fi
	[[ "$reply" == [Yy] ]]
}

_hokan_flag_from_var() {
	printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | tr '_' '-'
}

hokan_need_cmd() {
	local c
	for c in "$@"; do
		command -v "$c" >/dev/null 2>&1 || hokan_die "need '$c' on PATH"
	done
}

hokan_detect_os_arch() {
	local u m
	u=$(uname -s | tr '[:upper:]' '[:lower:]')
	m=$(uname -m)
	case "$u" in
	linux) HOKAN_OS=linux ;;
	darwin) HOKAN_OS=darwin ;;
	*) hokan_die "unsupported OS: $u (need linux or macOS)" ;;
	esac
	case "$m" in
	x86_64 | amd64) HOKAN_ARCH=amd64 ;;
	aarch64 | arm64) HOKAN_ARCH=arm64 ;;
	*) hokan_die "unsupported arch: $m (need amd64 or arm64)" ;;
	esac
}

# Find a Hokan source tree, or clone SOURCE if it looks like a git URL.
# Optional arg: directory of the calling install script (scripts/).
hokan_resolve_src() {
	local scripts="${1-}" root
	if [[ -n "${SOURCE-}" && -f "${SOURCE}/go.mod" ]]; then
		HOKAN_SRC=$(cd "$SOURCE" && pwd)
		return 0
	fi
	if [[ -n "${SOURCE-}" && "$SOURCE" == *:* ]]; then
		hokan_need_cmd git
		HOKAN_SRC="${PREFIX:-$HOME/hokan}/src"
		if [[ "$DRY_RUN" == 1 ]]; then
			hokan_log "dry-run: git clone $SOURCE $HOKAN_SRC"
			return 0
		fi
		mkdir -p "$(dirname "$HOKAN_SRC")"
		if [[ -d "$HOKAN_SRC/.git" ]]; then
			hokan_info "updating $HOKAN_SRC"
			git -C "$HOKAN_SRC" fetch --depth 1
			git -C "$HOKAN_SRC" reset --hard FETCH_HEAD
		else
			hokan_info "cloning $SOURCE"
			git clone --depth 1 "$SOURCE" "$HOKAN_SRC"
		fi
		return 0
	fi
	if [[ -n "$scripts" ]]; then
		root=$(cd "$scripts/.." && pwd)
		if [[ -f "$root/go.mod" ]] && grep -q "$HOKAN_MODULE" "$root/go.mod"; then
			HOKAN_SRC=$root
			return 0
		fi
	fi
	if [[ -f "$PWD/go.mod" ]] && grep -q "$HOKAN_MODULE" "$PWD/go.mod"; then
		HOKAN_SRC=$PWD
		return 0
	fi
	hokan_die "could not find Hokan source.
  Run this from a clone, or pass --source /path/to/hokan
  Example: git clone https://github.com/hokan/hokan.git && ./hokan/scripts/install-server.sh"
}

hokan_go_version_ok() {
	command -v go >/dev/null 2>&1 || return 1
	local v
	v=$(go env GOVERSION 2>/dev/null | sed 's/^go//')
	[[ -n "$v" ]] || return 1
	printf '%s\n%s\n' "$HOKAN_GO_MIN" "$v" | sort -V | head -n1 | grep -qx "$HOKAN_GO_MIN"
}

hokan_ensure_go() {
	if hokan_go_version_ok; then
		hokan_info "Go $(go env GOVERSION) (need ${HOKAN_GO_MIN}+)"
		return 0
	fi
	if command -v go >/dev/null 2>&1; then
		hokan_warn "Go $(go env GOVERSION 2>/dev/null || echo unknown) is older than $HOKAN_GO_MIN"
	else
		hokan_warn "Go not found on PATH"
	fi
	local dest="${HOKAN_GO_DIR:-$HOME/.local/go}"
	if ! hokan_confirm "Install Go ${HOKAN_GO_BOOTSTRAP} to $dest?" Y; then
		hokan_die "install Go ${HOKAN_GO_MIN}+ and re-run
  https://go.dev/dl/"
	fi
	hokan_need_cmd curl tar
	hokan_detect_os_arch
	local url tarball
	url="https://go.dev/dl/go${HOKAN_GO_BOOTSTRAP}.${HOKAN_OS}-${HOKAN_ARCH}.tar.gz"
	tarball="${TMPDIR:-/tmp}/go${HOKAN_GO_BOOTSTRAP}.tar.gz"
	hokan_info "downloading $url"
	if [[ "$DRY_RUN" == 1 ]]; then
		hokan_log "dry-run: curl -L $url"
		return 0
	fi
	curl -fsSL "$url" -o "$tarball"
	mkdir -p "$(dirname "$dest")"
	rm -rf "$dest"
	tar -C "$(dirname "$dest")" -xzf "$tarball"
	if [[ "$(basename "$dest")" != go ]]; then
		rm -rf "$dest"
		mv "$(dirname "$dest")/go" "$dest"
	fi
	export PATH="$dest/bin:$PATH"
	hokan_go_version_ok || hokan_die "Go install did not produce ${HOKAN_GO_MIN}+"
	hokan_info "Go $(go env GOVERSION) installed at $dest"
}

hokan_build() {
	local pkg="$1" out="$2"
	hokan_info "building $pkg"
	if [[ "$DRY_RUN" == 1 ]]; then
		hokan_log "dry-run: go build -o $out $pkg"
		return 0
	fi
	mkdir -p "$(dirname "$out")"
	(
		cd "$HOKAN_SRC"
		CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$out" "$pkg"
	)
}

hokan_install_bin() {
	local src="$1" dest="$2"
	if [[ "$DRY_RUN" == 1 ]]; then
		hokan_log "dry-run: install $src -> $dest"
		return 0
	fi
	mkdir -p "$(dirname "$dest")"
	if command -v install >/dev/null 2>&1; then
		install -m 755 "$src" "$dest"
	else
		cp "$src" "$dest"
		chmod 755 "$dest"
	fi
}

hokan_path_contains() {
	case ":$PATH:" in
	*":$1:"*) return 0 ;;
	*) return 1 ;;
	esac
}

hokan_maybe_path_hint() {
	local dir="$1"
	hokan_path_contains "$dir" && return 0
	hokan_warn "$dir is not on PATH"
	hokan_log "  export PATH=\"$dir:\$PATH\""
}

hokan_http_health_url() {
	local addr="$1" host port
	case "$addr" in
	:*) host=127.0.0.1; port=${addr#:} ;;
	127.0.0.1:* | localhost:* | \[::1\]:*)
		host=${addr%%:*}
		port=${addr##*:}
		;;
	0.0.0.0:* | \[::\]:*)
		port=${addr##*:}
		host=127.0.0.1
		;;
	*)
		host=${addr%%:*}
		port=${addr##*:}
		;;
	esac
	printf 'http://%s:%s/healthz' "$host" "$port"
}

hokan_wait_healthz() {
	local url="$1" i
	[[ "$DRY_RUN" == 1 ]] && return 0
	command -v curl >/dev/null 2>&1 || return 0
	for i in 1 2 3 4 5 6 7 8 9 10; do
		if curl -fsS -o /dev/null "$url"; then
			hokan_info "health check ok ($url)"
			return 0
		fi
		sleep 0.5
	done
	hokan_warn "server started but $url did not return 200 yet"
}

# Fast-forward the source checkout. Skips if not a git repo or if the tree is dirty.
hokan_git_pull() {
	local dir="${1:-$HOKAN_SRC}"
	if [[ ! -d "$dir/.git" ]]; then
		hokan_warn "$dir is not a git checkout; building the tree as-is"
		return 0
	fi
	if [[ -n "$(git -C "$dir" status --porcelain)" ]]; then
		hokan_warn "uncommitted changes in $dir; skipping git pull"
		git -C "$dir" status -sb
		return 0
	fi
	local before after
	before=$(git -C "$dir" rev-parse --short HEAD)
	hokan_info "git pull --ff-only ($dir @ $before)"
	if [[ "$DRY_RUN" == 1 ]]; then
		hokan_log "dry-run: git -C $dir pull --ff-only"
		return 0
	fi
	git -C "$dir" pull --ff-only
	after=$(git -C "$dir" rev-parse --short HEAD)
	if [[ "$before" == "$after" ]]; then
		hokan_info "already up to date ($after)"
	else
		hokan_info "updated $before -> $after"
	fi
}

# Locate an existing server prefix (env file + binary).
hokan_find_server_prefix() {
	local c
	if [[ -n "${PREFIX-}" ]]; then
		[[ -f "$PREFIX/hokan.env" ]] || hokan_die "no $PREFIX/hokan.env (not an installed server)
  Example: $0 --prefix $HOME/hokan"
		PREFIX=$(cd "$PREFIX" && pwd)
		return 0
	fi
	for c in "$HOME/hokan" /var/lib/hokan "${HOKAN_SRC-}"; do
		[[ -n "$c" ]] || continue
		if [[ -f "$c/hokan.env" && -e "$c/bin/hokan-server" ]]; then
			PREFIX=$(cd "$c" && pwd)
			hokan_info "found install at $PREFIX"
			return 0
		fi
	done
	hokan_die "no existing Hokan server install.
  Run ./scripts/install-server.sh first, or pass --prefix DIR
  Example: $0 --prefix $HOME/hokan"
}

hokan_detect_unit() {
	if [[ -f /etc/systemd/system/hokan.service ]]; then
		UNIT=system
	elif [[ -f "${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/hokan.service" ]]; then
		UNIT=user
	else
		UNIT=none
	fi
}

hokan_restart_server() {
	case "${UNIT:-none}" in
	system)
		if [[ "$(id -u)" == 0 ]]; then
			hokan_run systemctl restart hokan.service
		else
			hokan_run sudo systemctl restart hokan.service
		fi
		;;
	user)
		hokan_run systemctl --user restart hokan.service
		;;
	none)
		hokan_warn "no systemd unit; restart hokan-server yourself"
		;;
	esac
}

hokan_env_get() {
	local key="$1" file="$2"
	awk -F= -v k="$key" '$1==k {sub(/^[^=]+=/,""); print; exit}' "$file"
}
