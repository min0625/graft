#!/bin/bash

set -euo pipefail

# =============================================================================
# graft installer
# Usage: curl -fsSL https://raw.githubusercontent.com/min0625/graft/main/script/install.sh | bash
# =============================================================================

REPO="min0625/graft"
BINARY="graft"
INSTALL_DIR="${GRAFT_INSTALL_DIR:-$HOME/.local/bin}"

# --- colors (only when connected to a terminal) ------------------------------
if [[ -t 1 ]]; then
    BOLD=$'\033[1m'
    GREEN=$'\033[32m'
    YELLOW=$'\033[33m'
    RED=$'\033[31m'
    RESET=$'\033[0m'
else
    BOLD=""
    GREEN=""
    YELLOW=""
    RED=""
    RESET=""
fi

info()    { printf "${BOLD}%s${RESET}\n" "$*"; }
success() { printf "${GREEN}✔ %s${RESET}\n" "$*"; }
warn()    { printf "${YELLOW}⚠ %s${RESET}\n" "$*"; }
error()   {
    printf "${RED}✖ %s${RESET}\n" "$*" >&2
    exit 1
}

# --- detect OS ---------------------------------------------------------------
detect_os() {
    local os
    os="$(uname -s)"
    case "${os}" in
        Linux*) echo "linux" ;;
        Darwin*) echo "darwin" ;;
        *)    error "Unsupported OS: ${os}" ;;
    esac
}

# --- detect architecture -----------------------------------------------------
detect_arch() {
    local arch
    arch="$(uname -m)"
    case "${arch}" in
        x86_64 | amd64) echo "amd64" ;;
        arm64 | aarch64) echo "arm64" ;;
        *)            error "Unsupported architecture: ${arch}" ;;
    esac
}

# --- fetch latest version tag from GitHub ------------------------------------
fetch_latest_version() {
    if command -v curl > /dev/null 2>&1; then
        curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" |
            grep '"tag_name"' |
            sed 's/.*"tag_name": *"\(.*\)".*/\1/'
    elif command -v wget > /dev/null 2>&1; then
        wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" |
            grep '"tag_name"' |
            sed 's/.*"tag_name": *"\(.*\)".*/\1/'
    else
        error "curl or wget is required to install graft"
    fi
}

# --- download a URL to a file ------------------------------------------------
download() {
    local url="$1"
    local dest="$2"
    if command -v curl > /dev/null 2>&1; then
        curl -fsSL "${url}" -o "${dest}"
    else
        wget -qO "${dest}" "${url}"
    fi
}

# --- check if INSTALL_DIR is in PATH -----------------------------------------
check_path() {
    case ":${PATH}:" in
        *":${INSTALL_DIR}:"*) return 0 ;;
        *)                return 1 ;;
    esac
}

# --- print PATH setup hint ---------------------------------------------------
print_path_hint() {
    local shell_config
    case "${SHELL}" in
        */zsh) shell_config="~/.zshrc" ;;
        */bash) shell_config="~/.bashrc" ;;
        */fish) shell_config="~/.config/fish/config.fish" ;;
        *)  shell_config="your shell config file" ;;
    esac

    warn "${INSTALL_DIR} is not in your PATH"
    printf "\n"
    printf "  Add the following to %s:\n" "${shell_config}"
    printf "\n"
    case "${SHELL}" in
        */fish)
            printf "    fish_add_path %s\n" "${INSTALL_DIR}"
            ;;
        *)
            printf "    export PATH=\"%s:\$PATH\"\n" "${INSTALL_DIR}"
            ;;
    esac
    printf "\n"
    printf "  Then restart your shell or run:\n"
    printf "\n"
    printf "    source %s\n" "${shell_config}"
    printf "\n"
}

# =============================================================================
# main
# =============================================================================
main() {
    info "Installing graft..."
    printf "\n"

    local os arch version archive download_url checksum_url tmp_dir

    os="$(detect_os)"
    arch="$(detect_arch)"

    # allow pinning version via env: GRAFT_VERSION=v1.2.3
    version="${GRAFT_VERSION:-}"
    if [[ -z "${version}" ]]; then
        info "Fetching latest version..."
        version="$(fetch_latest_version)"
        [[ -z "${version}" ]] && error "Could not determine latest version. Set GRAFT_VERSION manually."
    fi

    # goreleaser archive name: graft_linux_amd64.tar.gz
    archive="${BINARY}_${os}_${arch}.tar.gz"
    download_url="https://github.com/${REPO}/releases/download/${version}/${archive}"
    checksum_url="https://github.com/${REPO}/releases/download/${version}/SHA256SUMS"

    tmp_dir="$(mktemp -d)"
    trap "rm -rf '${tmp_dir}' 2>/dev/null" EXIT

    info "Downloading ${BINARY} ${version} (${os}/${arch})..."
    download "${download_url}" "${tmp_dir}/${archive}" ||
        error "Download failed. Make sure ${version} exists: https://github.com/${REPO}/releases"

    # --- verify checksum (if sha256sum / shasum is available) ------------------
    if command -v sha256sum > /dev/null 2>&1 || command -v shasum > /dev/null 2>&1; then
        info "Verifying checksum..."
        if download "${checksum_url}" "${tmp_dir}/SHA256SUMS" 2> /dev/null; then
            local expected actual
            expected="$(grep "${archive}" "${tmp_dir}/SHA256SUMS" | awk '{print $1}')"
            if [[ -n "${expected}" ]]; then
                if command -v sha256sum > /dev/null 2>&1; then
                    actual="$(sha256sum "${tmp_dir}/${archive}" | awk '{print $1}')"
                else
                    actual="$(shasum -a 256 "${tmp_dir}/${archive}" | awk '{print $1}')"
                fi
                [[ "${expected}" == "${actual}" ]] || error "Checksum mismatch — download may be corrupted"
                success "Checksum verified"
            fi
        fi
    fi

    # --- extract ---------------------------------------------------------------
    tar -xzf "${tmp_dir}/${archive}" -C "${tmp_dir}"

    # --- install ---------------------------------------------------------------
    mkdir -p "${INSTALL_DIR}"
    mv "${tmp_dir}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
    chmod +x "${INSTALL_DIR}/${BINARY}"

    printf "\n"
    success "graft ${version} installed to ${INSTALL_DIR}/${BINARY}"
    printf "\n"

    # --- PATH hint -------------------------------------------------------------
    if check_path; then
        printf "Run ${BOLD}graft --help${RESET} to get started.\n"
    else
        print_path_hint
    fi
}

main "$@"
