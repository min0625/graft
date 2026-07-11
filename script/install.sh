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
        *) error "Unsupported OS: ${os}" ;;
    esac
}

# --- detect architecture -----------------------------------------------------
detect_arch() {
    local arch
    arch="$(uname -m)"
    case "${arch}" in
        x86_64 | amd64) echo "amd64" ;;
        arm64 | aarch64) echo "arm64" ;;
        *) error "Unsupported architecture: ${arch}" ;;
    esac
}

# --- fetch latest version tag from GitHub ------------------------------------
fetch_latest_version() {
    curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" |
        grep -o '"tag_name": *"[^"]*"' |
        grep -o '"[^"]*"$' |
        tr -d '"'
}

# --- check if INSTALL_DIR is in PATH -----------------------------------------
check_path() {
    case ":${PATH}:" in
        *":${INSTALL_DIR}:"*) return 0 ;;
        *) return 1 ;;
    esac
}

# --- print PATH setup hint ---------------------------------------------------
print_path_hint() {
    local shell_config
    # $SHELL is the user's login shell — the right target for a persistent PATH
    # hint (the script itself runs under bash via the curl|bash pipe, so $0 would
    # be misleading). ${SHELL:-} keeps set -u from aborting in minimal
    # environments (e.g. containers) where SHELL is unset.
    # shellcheck disable=SC2088  # intentional: ~ is displayed as a hint to the user, not expanded
    case "${SHELL:-}" in
        */zsh) shell_config='~/.zshrc' ;;
        # macOS terminals start bash as a login shell, which reads ~/.bash_profile;
        # Linux interactive shells read ~/.bashrc.
        */bash)
            if [[ "$(uname -s)" == Darwin ]]; then
                shell_config='~/.bash_profile'
            else
                shell_config='~/.bashrc'
            fi
            ;;
        */fish) shell_config='~/.config/fish/config.fish' ;;
        *)  shell_config="your shell config file" ;;
    esac

    warn "${INSTALL_DIR} is not in your PATH"
    printf "\n"
    case "${SHELL:-}" in
        */fish)
            printf "  Run the following to add it to your PATH:\n"
            printf "\n"
            printf "    fish_add_path %s\n" "${INSTALL_DIR}"
            printf "\n"
            printf "  Or open a new terminal.\n"
            printf "\n"
            ;;
        *)
            printf "  Add the following to %s:\n" "${shell_config}"
            printf "\n"
            printf "    export PATH=\"%s:\$PATH\"\n" "${INSTALL_DIR}"
            printf "\n"
            printf "  Then restart your shell or run:\n"
            printf "\n"
            printf "    source %s\n" "${shell_config}"
            printf "\n"
            ;;
    esac
}

# =============================================================================
# main
# =============================================================================
main() {
    info "Installing graft..."
    printf "\n"

    local os arch version archive download_url checksum_url tmp_dir

    command -v curl > /dev/null 2>&1 || error "curl is required but not found. Please install curl."

    os="$(detect_os)"
    arch="$(detect_arch)"

    # allow pinning version via env: GRAFT_VERSION=v0.0.1
    version="${GRAFT_VERSION:-}"
    if [[ -z "${version}" ]]; then
        info "Fetching latest version..."
        # A failed fetch (404 / API rate-limit) makes the command substitution
        # exit non-zero; the `if` context suppresses set -e so we reach the
        # friendly hint below instead of aborting with a raw curl error.
        if ! version="$(fetch_latest_version)"; then
            version=""
        fi
        [[ -z "${version}" ]] && error "Could not determine latest version. Set GRAFT_VERSION manually."
    fi

    # goreleaser archive name: graft_linux_amd64.tar.gz
    archive="${BINARY}_${os}_${arch}.tar.gz"
    download_url="https://github.com/${REPO}/releases/download/${version}/${archive}"
    checksum_url="https://github.com/${REPO}/releases/download/${version}/SHA256SUMS"

    tmp_dir="$(mktemp -d)"
    # shellcheck disable=SC2064  # intentional: expand tmp_dir now; it's local to main() and out of scope at EXIT
    trap "rm -rf '${tmp_dir}' 2>/dev/null" EXIT

    info "Downloading ${BINARY} ${version} (${os}/${arch})..."
    curl -fsSL "${download_url}" -o "${tmp_dir}/${archive}" ||
        error "Download failed. Make sure ${version} exists: https://github.com/${REPO}/releases"

    # --- verify checksum (if sha256sum / shasum is available) ------------------
    local checksum_bin=""
    if command -v sha256sum > /dev/null 2>&1; then
        checksum_bin="sha256sum"
    elif command -v shasum > /dev/null 2>&1; then
        checksum_bin="shasum"
    fi
    if [[ -n "${checksum_bin}" ]]; then
        info "Verifying checksum..."
        if curl -fsSL "${checksum_url}" -o "${tmp_dir}/SHA256SUMS" 2> /dev/null; then
            local expected actual
            # Match the filename field exactly ($2) so a longer entry that merely
            # contains ${archive} as a substring (e.g. an .sbom artifact) can't match.
            expected="$(awk -v f="${archive}" '$2 == f {print $1}' "${tmp_dir}/SHA256SUMS")"
            if [[ -n "${expected}" ]]; then
                if [[ "${checksum_bin}" == "sha256sum" ]]; then
                    actual="$(sha256sum "${tmp_dir}/${archive}" | awk '{print $1}')"
                else
                    actual="$(shasum -a 256 "${tmp_dir}/${archive}" | awk '{print $1}')"
                fi
                [[ "${expected}" == "${actual}" ]] || error "Checksum mismatch — download may be corrupted"
                success "Checksum verified"
            else
                warn "Could not verify checksum: no entry for ${archive} in SHA256SUMS"
            fi
        else
            warn "Could not verify checksum: failed to download SHA256SUMS"
        fi
    else
        warn "No sha256sum or shasum found — skipping checksum verification"
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
        printf "Run %sgraft --help%s to get started.\n" "${BOLD}" "${RESET}"
    else
        print_path_hint
    fi
}

main "$@"
