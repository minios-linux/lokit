#!/usr/bin/env bash
set -euo pipefail

APP="lokit"
REPO="minios-linux/lokit"

MUTED='\033[0;2m'
RED='\033[0;31m'
ORANGE='\033[38;5;214m'
NC='\033[0m'

requested_version="${VERSION:-${LOKIT_VERSION:-}}"
binary_path=""
no_modify_path=false
install_dir="${LOKIT_INSTALL_DIR:-$HOME/.lokit/bin}"

usage() {
  cat <<'EOF'
lokit Installer

Usage: install.sh [options]

Options:
  -h, --help              Display this help message
  -v, --version <version> Install a specific version (example: v0.7.0)
  -b, --binary <path>     Install from a local binary instead of downloading
      --install-dir <dir> Install directory (default: ~/.lokit/bin)
      --no-modify-path    Don't modify shell config files

Examples:
  curl -fsSL https://raw.githubusercontent.com/minios-linux/lokit/refs/heads/master/install.sh | bash
  curl -fsSL https://raw.githubusercontent.com/minios-linux/lokit/refs/heads/master/install.sh | bash -s -- --version v0.7.0
  ./install.sh --binary ./lokit
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help)
      usage
      exit 0
      ;;
    -v|--version)
      if [[ -n "${2:-}" ]]; then
        requested_version="$2"
        shift 2
      else
        echo -e "${RED}Error: --version requires a version argument${NC}" >&2
        exit 1
      fi
      ;;
    -b|--binary)
      if [[ -n "${2:-}" ]]; then
        binary_path="$2"
        shift 2
      else
        echo -e "${RED}Error: --binary requires a path argument${NC}" >&2
        exit 1
      fi
      ;;
    --install-dir)
      if [[ -n "${2:-}" ]]; then
        install_dir="$2"
        shift 2
      else
        echo -e "${RED}Error: --install-dir requires a path argument${NC}" >&2
        exit 1
      fi
      ;;
    --no-modify-path)
      no_modify_path=true
      shift
      ;;
    *)
      echo -e "${ORANGE}Warning: Unknown option '$1'${NC}" >&2
      shift
      ;;
  esac
done

mkdir -p "$install_dir"

print_message() {
  level="$1"
  message="$2"

  case "$level" in
    error)
      echo -e "${RED}${message}${NC}" >&2
      ;;
    warning)
      echo -e "${ORANGE}${message}${NC}" >&2
      ;;
    *)
      echo -e "${message}"
      ;;
  esac
}

if [[ -n "$binary_path" ]]; then
  if [[ ! -f "$binary_path" ]]; then
    print_message error "Error: Binary not found at $binary_path"
    exit 1
  fi
  specific_version="local"
else
  if ! command -v curl >/dev/null 2>&1; then
    print_message error "Error: curl is required but not installed"
    exit 1
  fi
  if ! command -v tar >/dev/null 2>&1; then
    print_message error "Error: tar is required but not installed"
    exit 1
  fi

  raw_os="$(uname -s)"
  os="$(echo "$raw_os" | tr '[:upper:]' '[:lower:]')"
  case "$raw_os" in
    Darwin*) os="darwin" ;;
    Linux*) os="linux" ;;
  esac

  arch="$(uname -m)"
  if [[ "$arch" == "aarch64" ]]; then
    arch="arm64"
  fi
  if [[ "$arch" == "x86_64" ]]; then
    arch="amd64"
  fi

  combo="$os-$arch"
  case "$combo" in
    linux-amd64|linux-arm64|darwin-amd64|darwin-arm64)
      ;;
    *)
      print_message error "Unsupported OS/Arch: $os/$arch"
      exit 1
      ;;
  esac

  if [[ -z "$requested_version" ]]; then
    url="https://api.github.com/repos/$REPO/releases/latest"
    tag_json="$(curl -fsSL "$url")"
    specific_version="$(printf '%s' "$tag_json" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)"
    if [[ -z "$specific_version" ]]; then
      print_message error "Failed to fetch latest version"
      exit 1
    fi
  else
    requested_version="${requested_version#v}"
    specific_version="v${requested_version}"
    http_status="$(curl -sI -o /dev/null -w "%{http_code}" "https://github.com/$REPO/releases/tag/${specific_version}")"
    if [[ "$http_status" == "404" ]]; then
      print_message error "Error: Release ${specific_version} not found"
      print_message info "${MUTED}Available releases: https://github.com/$REPO/releases${NC}"
      exit 1
    fi
  fi

  filename="$APP-${specific_version}-${os}-${arch}.tar.gz"
  download_url="https://github.com/$REPO/releases/download/${specific_version}/${filename}"
fi

check_existing_version() {
  if command -v "$APP" >/dev/null 2>&1; then
    installed="$("$APP" version 2>/dev/null || true)"
    if [[ -n "$installed" ]] && [[ "$installed" == *"$specific_version"* ]]; then
      print_message info "${MUTED}Version ${NC}${specific_version}${MUTED} already installed${NC}"
      exit 0
    fi
  fi
}

install_from_binary() {
  print_message info "${MUTED}Installing ${NC}${APP} ${MUTED}from: ${NC}${binary_path}"
  cp "$binary_path" "$install_dir/$APP"
  chmod 755 "$install_dir/$APP"
}

download_and_install() {
  print_message info "${MUTED}Installing ${NC}${APP} ${MUTED}version: ${NC}${specific_version}"
  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "$tmp_dir"' RETURN

  curl -# -L -o "$tmp_dir/$filename" "$download_url"
  tar -xzf "$tmp_dir/$filename" -C "$tmp_dir"

  bin_name="$APP-${specific_version}-${os}-${arch}"
  if [[ ! -f "$tmp_dir/$bin_name" ]]; then
    print_message error "Archive did not contain expected binary: $bin_name"
    exit 1
  fi

  mv "$tmp_dir/$bin_name" "$install_dir/$APP"
  chmod 755 "$install_dir/$APP"
}

if [[ -n "$binary_path" ]]; then
  install_from_binary
else
  check_existing_version
  download_and_install
fi

add_to_path() {
  config_file="$1"
  line="$2"

  if [[ -f "$config_file" ]] && grep -Fxq "$line" "$config_file"; then
    print_message info "${MUTED}PATH already configured in ${NC}${config_file}"
    return
  fi

  if [[ -e "$config_file" && ! -w "$config_file" ]]; then
    print_message warning "Cannot write to ${config_file}. Add manually:"
    print_message info "  ${line}"
    return
  fi

  mkdir -p "$(dirname "$config_file")"
  {
    echo
    echo "# lokit"
    echo "$line"
  } >> "$config_file"
  print_message info "${MUTED}Added ${NC}${APP}${MUTED} to PATH in ${NC}${config_file}"
}

if [[ "$no_modify_path" != true ]]; then
  current_shell="$(basename "${SHELL:-bash}")"
  case "$current_shell" in
    fish)
      config_candidates="${XDG_CONFIG_HOME:-$HOME/.config}/fish/config.fish"
      path_line="fish_add_path $install_dir"
      ;;
    zsh)
      config_candidates="${ZDOTDIR:-$HOME}/.zshrc ${ZDOTDIR:-$HOME}/.zshenv"
      path_line="export PATH=$install_dir:\$PATH"
      ;;
    bash)
      config_candidates="$HOME/.bashrc $HOME/.bash_profile $HOME/.profile"
      path_line="export PATH=$install_dir:\$PATH"
      ;;
    *)
      config_candidates="$HOME/.profile"
      path_line="export PATH=$install_dir:\$PATH"
      ;;
  esac

  if [[ ":$PATH:" != *":$install_dir:"* ]]; then
    config_file=""
    for file in $config_candidates; do
      if [[ -f "$file" ]]; then
        config_file="$file"
        break
      fi
    done
    if [[ -z "$config_file" ]]; then
      config_file="${config_candidates%% *}"
    fi
    add_to_path "$config_file" "$path_line"
  fi
fi

if [[ -n "${GITHUB_ACTIONS-}" && "${GITHUB_ACTIONS}" == "true" ]]; then
  echo "$install_dir" >> "$GITHUB_PATH"
  print_message info "Added $install_dir to \$GITHUB_PATH"
fi

echo
print_message info "${MUTED}${APP} installed at ${NC}${install_dir}/${APP}"
print_message info "${MUTED}Run:${NC} ${APP} version"
