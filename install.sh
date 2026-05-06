#!/usr/bin/env bash
# enemy-go installer — one-shot fetch + build + install.
#
# Usage (typical):
#     curl -fsSL https://raw.githubusercontent.com/y-o-o-z/enemy-go/main/install.sh | bash
#
# Or with explicit options:
#     curl -fsSL https://raw.githubusercontent.com/y-o-o-z/enemy-go/main/install.sh -o install.sh
#     chmod +x install.sh
#     ./install.sh                   # install to /usr/local/bin (asks for sudo)
#     PREFIX="$HOME/.local" ./install.sh   # install to ~/.local/bin (no sudo)
#     REPO_URL=https://github.com/y-o-o-z/enemy-go.git BRANCH=main ./install.sh
#
# Environment knobs:
#   REPO_URL   git URL to clone     (default: https://github.com/y-o-o-z/enemy-go.git)
#   BRANCH     branch to check out  (default: main)
#   PREFIX     install root         (default: /usr/local; binary goes to $PREFIX/bin)
#   GO_VERSION go toolchain version to bootstrap if Go is missing (default: 1.23.4)
#   KEEP_SRC   if "1", keep ./enemy-go source tree after install (default: discard)

set -euo pipefail

REPO_URL="${REPO_URL:-https://github.com/y-o-o-z/enemy-go.git}"
BRANCH="${BRANCH:-main}"
PREFIX="${PREFIX:-/usr/local}"
GO_VERSION="${GO_VERSION:-1.23.4}"
KEEP_SRC="${KEEP_SRC:-0}"
BIN_NAME="enemy"

c_cyan='\033[36m'; c_green='\033[32m'; c_yellow='\033[33m'; c_red='\033[31m'; c_bold='\033[1m'; c_reset='\033[0m'
log()  { printf "${c_cyan}[*]${c_reset} %s\n" "$*"; }
ok()   { printf "${c_green}[+]${c_reset} %s\n" "$*"; }
warn() { printf "${c_yellow}[!]${c_reset} %s\n" "$*" >&2; }
die()  { printf "${c_red}[x]${c_reset} %s\n" "$*" >&2; exit 1; }

require_cmd() { command -v "$1" >/dev/null 2>&1 || die "missing required tool: $1"; }
have()        { command -v "$1" >/dev/null 2>&1; }

detect_pkg_mgr() {
    if   have apt-get; then echo "apt"
    elif have dnf;     then echo "dnf"
    elif have yum;     then echo "yum"
    elif have pacman;  then echo "pacman"
    elif have apk;     then echo "apk"
    elif have brew;    then echo "brew"
    else echo "unknown"
    fi
}

ensure_base_tools() {
    local pm; pm="$(detect_pkg_mgr)"
    local need=()
    have git  || need+=("git")
    have curl || need+=("curl")
    have tar  || need+=("tar")
    if [[ ${#need[@]} -eq 0 ]]; then return 0; fi
    log "installing base tools: ${need[*]} (via $pm)"
    case "$pm" in
        apt)    sudo apt-get update -qq && sudo apt-get install -y -qq "${need[@]}" ;;
        dnf)    sudo dnf install -y -q "${need[@]}" ;;
        yum)    sudo yum install -y -q "${need[@]}" ;;
        pacman) sudo pacman -S --noconfirm "${need[@]}" ;;
        apk)    sudo apk add --no-progress "${need[@]}" ;;
        brew)   brew install "${need[@]}" ;;
        *)      die "no supported package manager — please install: ${need[*]}" ;;
    esac
}

install_go() {
    local arch goarch goos tarball url tmp
    goos="$(uname -s | tr '[:upper:]' '[:lower:]')"
    arch="$(uname -m)"
    case "$arch" in
        x86_64|amd64) goarch="amd64" ;;
        aarch64|arm64) goarch="arm64" ;;
        armv7l|armv6l) goarch="armv6l" ;;
        i386|i686)    goarch="386" ;;
        *)            die "unsupported architecture: $arch" ;;
    esac
    case "$goos" in
        linux|darwin) ;;
        *) die "unsupported OS for go bootstrap: $goos — install Go manually." ;;
    esac
    tarball="go${GO_VERSION}.${goos}-${goarch}.tar.gz"
    url="https://go.dev/dl/${tarball}"
    tmp="$(mktemp -d)"
    log "downloading $url"
    curl -fsSL "$url" -o "$tmp/$tarball" || die "go download failed"
    log "extracting Go $GO_VERSION to /usr/local"
    sudo rm -rf /usr/local/go
    sudo tar -C /usr/local -xzf "$tmp/$tarball"
    rm -rf "$tmp"
    export PATH="/usr/local/go/bin:$PATH"
    have go || die "go install verification failed"
    ok "Go $(go version | awk '{print $3}') ready"
}

ensure_go() {
    if have go; then
        local cur; cur="$(go version | awk '{print $3}' | sed 's/^go//')"
        log "found Go $cur"
        return 0
    fi
    warn "Go not found — bootstrapping Go $GO_VERSION"
    install_go
}

build_and_install() {
    local workdir; workdir="$(mktemp -d)"
    log "cloning $REPO_URL (branch $BRANCH) → $workdir"
    git clone --depth=1 --branch "$BRANCH" "$REPO_URL" "$workdir/enemy-go" \
        || die "git clone failed"

    pushd "$workdir/enemy-go" >/dev/null
    log "running go build"
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$BIN_NAME" ./...
    popd >/dev/null

    local target_dir="$PREFIX/bin"
    log "installing binary → $target_dir/$BIN_NAME"
    if [[ -w "$target_dir" ]] || [[ "$(id -u)" -eq 0 ]]; then
        install -m 0755 "$workdir/enemy-go/$BIN_NAME" "$target_dir/$BIN_NAME"
    else
        sudo install -m 0755 "$workdir/enemy-go/$BIN_NAME" "$target_dir/$BIN_NAME"
    fi

    if [[ "$KEEP_SRC" == "1" ]]; then
        local keep="$PWD/enemy-go-src"
        rm -rf "$keep"
        mv "$workdir/enemy-go" "$keep"
        log "source kept at $keep"
    fi
    rm -rf "$workdir"
}

main() {
    printf "${c_bold}${c_cyan}\n  enemy-go installer${c_reset}\n"
    printf "  repo:   %s\n  branch: %s\n  prefix: %s\n\n" "$REPO_URL" "$BRANCH" "$PREFIX"

    if [[ "$(uname -s)" != "Linux" && "$(uname -s)" != "Darwin" ]]; then
        die "this installer supports Linux and macOS only"
    fi

    ensure_base_tools
    ensure_go
    build_and_install

    if ! have "$BIN_NAME" && [[ ! -x "$PREFIX/bin/$BIN_NAME" ]]; then
        die "binary not found after install"
    fi

    ok "$BIN_NAME installed at $PREFIX/bin/$BIN_NAME"
    "$PREFIX/bin/$BIN_NAME" -version || true
    printf "\n${c_bold}next:${c_reset} run \`%s\` (no args) for the interactive setup wizard.\n" "$BIN_NAME"
}

main "$@"
