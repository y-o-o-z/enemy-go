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
#   SKIP_IP_CHECK if "1", skip the up-front IP discovery step (default: 0)
#   REQUIRE_IP if "v4" / "v6" / "any" / "none", abort installer when no usable
#              address of that family is found (default: any — needs at least one
#              globally-routable v4 OR v6 to bother installing the tool at all)
#   INSTALL_OIDENTD if "1", install + enable oidentd so each clone can register
#              its own ident (default: 0 — installer only suggests it)

set -euo pipefail

REPO_URL="${REPO_URL:-https://github.com/y-o-o-z/enemy-go.git}"
BRANCH="${BRANCH:-main}"
PREFIX="${PREFIX:-/usr/local}"
GO_VERSION="${GO_VERSION:-1.23.4}"
KEEP_SRC="${KEEP_SRC:-0}"
SKIP_IP_CHECK="${SKIP_IP_CHECK:-0}"
REQUIRE_IP="${REQUIRE_IP:-any}"
INSTALL_OIDENTD="${INSTALL_OIDENTD:-0}"
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

### --- network discovery --------------------------------------------------
#
# discover_ips writes one IP per line to stdout. The first column is the
# family ("v4" / "v6"), the second column is the address, the third (best
# effort) is the interface name. Filters out anything that cannot reach
# the public Internet: loopback, link-local (169.254/16, fe80::/10),
# multicast, unspecified, RFC1918 private v4, IPv6 ULAs (fc00::/7).
#
# Detection order:
#   1) `ip -o addr`   (iproute2, modern Linux — preferred)
#   2) `ifconfig -a`  (BusyBox / older Linux / macOS / BSD)
#   3) `/proc/net/{fib_trie,if_inet6}` last-resort scrape (Linux only)

_ip_is_public_v4() {
    # arg: "a.b.c.d"
    local ip="$1"
    case "$ip" in
        ""|0.0.0.0|255.255.255.255) return 1 ;;
        127.*) return 1 ;;                    # loopback
        169.254.*) return 1 ;;                # link-local
        10.*) return 1 ;;                     # RFC1918
        192.168.*) return 1 ;;                # RFC1918
        100.64.*|100.65.*|100.66.*|100.67.*|100.68.*|100.69.*|100.7[0-9].*|100.8[0-9].*|100.9[0-9].*|100.10[0-9].*|100.11[0-9].*|100.12[0-7].*) return 1 ;; # CGNAT 100.64/10
        172.1[6-9].*|172.2[0-9].*|172.3[01].*) return 1 ;;  # 172.16/12
        22[4-9].*|23[0-9].*|24[0-9].*|25[0-5].*) return 1 ;; # multicast/reserved
    esac
    # plain v4 dotted-quad sanity
    [[ "$ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1
    return 0
}

_ip_is_public_v6() {
    local ip="${1,,}"
    case "$ip" in
        ""|::|::1) return 1 ;;
        fe[89ab]*:*|fe[89ab][0-9a-f]:*) return 1 ;;          # fe80::/10 link-local
        f[cd][0-9a-f][0-9a-f]:*) return 1 ;;                 # fc00::/7 ULA
        ff*:*) return 1 ;;                                   # multicast
        ::ffff:*) return 1 ;;                                # IPv4-mapped
    esac
    [[ "$ip" == *:* ]] || return 1
    return 0
}

discover_ips() {
    local out=""
    if have ip; then
        # `ip -o addr show scope global` already filters out link-local on Linux.
        out="$(ip -o addr show scope global 2>/dev/null \
            | awk '{
                fam = ($3 == "inet") ? "v4" : ($3 == "inet6") ? "v6" : "";
                if (fam == "") next;
                ifc = $2;
                split($4, a, "/");
                printf "%s %s %s\n", fam, a[1], ifc;
              }')"
    fi
    if [[ -z "$out" ]] && have ifconfig; then
        out="$(ifconfig -a 2>/dev/null \
            | awk '
                /^[a-zA-Z0-9_:.-]+:?[ \t]/ { sub(/:$/,"",$1); ifc=$1 }
                /[ \t]+inet[ \t]/  { print "v4 " $2 " " ifc }
                /[ \t]+inet6[ \t]/ {
                    addr=$2; sub(/%.*/,"",addr); sub(/\/.*$/,"",addr);
                    print "v6 " addr " " ifc;
                }')"
    fi
    if [[ -z "$out" ]] && [[ -r /proc/net/if_inet6 ]]; then
        # fallback: hex-decode /proc/net/if_inet6 for IPv6
        out="$(awk '{
                a=$1;
                printf "v6 %s:%s:%s:%s:%s:%s:%s:%s %s\n",
                    substr(a,1,4), substr(a,5,4), substr(a,9,4), substr(a,13,4),
                    substr(a,17,4), substr(a,21,4), substr(a,25,4), substr(a,29,4),
                    $6;
              }' /proc/net/if_inet6 2>/dev/null)"
    fi
    [[ -n "$out" ]] || return 0

    # Filter and dedupe.
    printf "%s\n" "$out" | awk '
        { key=$1" "$2; if (!(key in seen)) { seen[key]=1; print } }
    ' | while read -r fam ip ifc; do
        if [[ "$fam" == "v4" ]] && _ip_is_public_v4 "$ip"; then
            printf "v4 %s %s\n" "$ip" "${ifc:-?}"
        elif [[ "$fam" == "v6" ]] && _ip_is_public_v6 "$ip"; then
            printf "v6 %s %s\n" "$ip" "${ifc:-?}"
        fi
    done
}

verify_ips() {
    if [[ "$SKIP_IP_CHECK" == "1" ]]; then
        log "SKIP_IP_CHECK=1 — pomijam wykrywanie adresów"
        return 0
    fi
    log "wykrywanie globalnie-routowalnych adresów IP..."
    local found
    found="$(discover_ips || true)"

    local v4_list v6_list v4_n v6_n
    v4_list="$(printf "%s\n" "$found" | awk '$1=="v4"')"
    v6_list="$(printf "%s\n" "$found" | awk '$1=="v6"')"
    v4_n="$(printf "%s" "$v4_list" | grep -c '^v4 ' || true)"
    v6_n="$(printf "%s" "$v6_list" | grep -c '^v6 ' || true)"

    printf "\n  ${c_bold}IPv4${c_reset} (publiczne): %s\n" "$v4_n"
    if [[ -n "$v4_list" ]]; then
        while read -r _ ip ifc; do
            [[ -n "$ip" ]] || continue
            printf "    ${c_green}%-39s${c_reset}  %s\n" "$ip" "$ifc"
        done <<< "$v4_list"
    else
        printf "    ${c_yellow}(brak)${c_reset}\n"
    fi

    printf "  ${c_bold}IPv6${c_reset} (publiczne): %s\n" "$v6_n"
    if [[ -n "$v6_list" ]]; then
        while read -r _ ip ifc; do
            [[ -n "$ip" ]] || continue
            printf "    ${c_green}%-39s${c_reset}  %s\n" "$ip" "$ifc"
        done <<< "$v6_list"
    else
        printf "    ${c_yellow}(brak)${c_reset}\n"
    fi
    printf "\n"

    case "$REQUIRE_IP" in
        none)
            ;;
        v4)
            [[ "$v4_n" -gt 0 ]] || die "REQUIRE_IP=v4 ale nie znaleziono żadnego publicznego IPv4"
            ;;
        v6)
            [[ "$v6_n" -gt 0 ]] || die "REQUIRE_IP=v6 ale nie znaleziono żadnego publicznego IPv6"
            ;;
        any|*)
            if [[ "$v4_n" -eq 0 && "$v6_n" -eq 0 ]]; then
                die "nie znaleziono żadnego globalnie-routowalnego IP (v4 ani v6) — \
enemy nie ma do czego się binda. Sprawdź \`ip addr\` lub ustaw SKIP_IP_CHECK=1 jeśli wiesz co robisz."
            fi
            ;;
    esac
    ok "wykryto $v4_n adres(ów) IPv4 i $v6_n adres(ów) IPv6"
}

### --- oidentd integration -----------------------------------------------
#
# enemy-go can register a per-(local-IP, server) ident with a running
# oidentd, which lets one user open many more concurrent clones to IRCnet
# (the network's per-(ident@host) limit becomes per-ident-per-host
# instead of just per-host). We probe + optionally install oidentd here.

probe_oidentd() {
    if have ss; then
        ss -lnt 2>/dev/null | awk '{print $4}' | grep -qE '(:113|:113$)' && return 0
    fi
    if have netstat; then
        netstat -lnt 2>/dev/null | awk '{print $4}' | grep -qE '(:113|:113$)' && return 0
    fi
    if have pgrep; then
        pgrep -x oidentd >/dev/null 2>&1 && return 0
    fi
    return 1
}

install_oidentd() {
    local pm; pm="$(detect_pkg_mgr)"
    log "instaluję oidentd (via $pm)"
    case "$pm" in
        apt)    sudo apt-get update -qq && sudo apt-get install -y -qq oidentd ;;
        dnf)    sudo dnf install -y -q oidentd ;;
        yum)    sudo yum install -y -q oidentd ;;
        pacman) sudo pacman -S --noconfirm oidentd ;;
        apk)    sudo apk add --no-progress oidentd ;;
        brew)   brew install oidentd ;;
        *)      warn "no supported package manager — install oidentd manually"; return 1 ;;
    esac
    if have systemctl; then
        sudo systemctl enable --now oidentd 2>/dev/null \
            || sudo systemctl enable --now oidentd.socket 2>/dev/null \
            || warn "couldn't enable oidentd via systemctl — start it manually"
    fi
}

verify_oidentd() {
    if probe_oidentd; then
        ok "oidentd jest aktywny (port 113 nasłuchuje) — enemy automatycznie zarejestruje per-klon ident"
        return 0
    fi
    if [[ "$INSTALL_OIDENTD" == "1" ]]; then
        install_oidentd
        if probe_oidentd; then
            ok "oidentd zainstalowany i uruchomiony"
        else
            warn "oidentd zainstalowany, ale nie nasłuchuje na :113 — sprawdź konfigurację"
        fi
        return 0
    fi
    warn "oidentd nieaktywny — klony użyją statycznego identa (jeden per-klon, ale bez per-(IP,server) rotacji)."
    warn "  Aby włączyć ten trick (więcej klonów z 1 usera): doinstaluj oidentd, np."
    case "$(detect_pkg_mgr)" in
        apt)    printf "    sudo apt-get install -y oidentd && sudo systemctl enable --now oidentd\n" ;;
        dnf|yum)printf "    sudo %s install -y oidentd && sudo systemctl enable --now oidentd\n" "$(detect_pkg_mgr)" ;;
        pacman) printf "    sudo pacman -S oidentd && sudo systemctl enable --now oidentd\n" ;;
        apk)    printf "    sudo apk add oidentd && sudo rc-update add oidentd && sudo rc-service oidentd start\n" ;;
        brew)   printf "    brew install oidentd && brew services start oidentd\n" ;;
        *)      printf "    (zainstaluj pakiet 'oidentd' z menedżera systemu)\n" ;;
    esac
    printf "  Albo odpal installer z INSTALL_OIDENTD=1, np.:\n"
    printf "    INSTALL_OIDENTD=1 curl -fsSL https://raw.githubusercontent.com/y-o-o-z/enemy-go/main/install.sh | bash\n"
}

main() {
    printf "${c_bold}${c_cyan}\n  enemy-go installer${c_reset}\n"
    printf "  repo:   %s\n  branch: %s\n  prefix: %s\n\n" "$REPO_URL" "$BRANCH" "$PREFIX"

    if [[ "$(uname -s)" != "Linux" && "$(uname -s)" != "Darwin" ]]; then
        die "this installer supports Linux and macOS only"
    fi

    verify_ips
    verify_oidentd
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
