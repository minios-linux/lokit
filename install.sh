#!/usr/bin/env bash
set -euo pipefail

APP="lokit"
REPO="minios-linux/lokit"
INSTALL_DIR="${LOKIT_INSTALL_DIR:-$HOME/.local/bin}"
REQUESTED_VERSION="${LOKIT_VERSION:-}"
NO_MODIFY_PATH=false

usage() {
  cat <<'EOF'
lokit Installer

Usage: install.sh [options]

Options:
  -h, --help              Display this help message
  -v, --version <version> Install specific version (example: v0.6.0)
      --install-dir <dir> Install destination (default: ~/.local/bin)
      --no-modify-path    Do not print PATH update hints

Examples:
  curl -fsSL https://raw.githubusercontent.com/minios-linux/lokit/master/install.sh | bash
  curl -fsSL https://raw.githubusercontent.com/minios-linux/lokit/master/install.sh | bash -s -- --version v0.6.0
  curl -fsSL https://raw.githubusercontent.com/minios-linux/lokit/master/install.sh | LOKIT_INSTALL_DIR=$HOME/bin bash
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help)
      usage
      exit 0
      ;;
    -v|--version)
      if [[ -z "${2:-}" ]]; then
        echo "Error: --version requires an argument" >&2
        exit 1
      fi
      REQUESTED_VERSION="$2"
      shift 2
      ;;
    --install-dir)
      if [[ -z "${2:-}" ]]; then
        echo "Error: --install-dir requires an argument" >&2
        exit 1
      fi
      INSTALL_DIR="$2"
      shift 2
      ;;
    --no-modify-path)
      NO_MODIFY_PATH=true
      shift
      ;;
    *)
      echo "Warning: unknown option '$1'" >&2
      shift
      ;;
  esac
done

if ! command -v curl >/dev/null 2>&1; then
  echo "Error: curl is required" >&2
  exit 1
fi
if ! command -v tar >/dev/null 2>&1; then
  echo "Error: tar is required" >&2
  exit 1
fi

raw_os="$(uname -s)"
os="$(echo "$raw_os" | tr '[:upper:]' '[:lower:]')"
case "$raw_os" in
  Darwin*) os="darwin" ;;
  Linux*) os="linux" ;;
esac

arch_raw="$(uname -m)"
case "$arch_raw" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *)
    echo "Unsupported architecture: $arch_raw (supported: amd64, arm64)" >&2
    exit 1
    ;;
esac

case "$os-$arch" in
  linux-amd64|linux-arm64|darwin-amd64|darwin-arm64) ;;
  *)
    echo "Unsupported platform: $os-$arch" >&2
    exit 1
    ;;
esac

if [[ -z "$REQUESTED_VERSION" ]]; then
  tag="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)"
  if [[ -z "$tag" ]]; then
    echo "Failed to resolve latest release tag" >&2
    exit 1
  fi
else
  tag="$REQUESTED_VERSION"
  [[ "$tag" == v* ]] || tag="v$tag"

  http_status="$(curl -fsSLI -o /dev/null -w '%{http_code}' "https://github.com/$REPO/releases/tag/$tag")"
  if [[ "$http_status" == "404" ]]; then
    echo "Release $tag not found" >&2
    exit 1
  fi
fi

asset="$APP-$tag-$os-$arch.tar.gz"
url="https://github.com/$REPO/releases/download/$tag/$asset"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

echo "Installing $APP $tag for $os/$arch"
echo "Downloading $asset..."
curl -fL "$url" -o "$tmp_dir/$asset"

tar -xzf "$tmp_dir/$asset" -C "$tmp_dir"
bin_path="$tmp_dir/$APP-$tag-$os-$arch"

if [[ ! -f "$bin_path" ]]; then
  echo "Archive did not contain expected binary: $APP-$tag-$os-$arch" >&2
  exit 1
fi

mkdir -p "$INSTALL_DIR"
install -m 0755 "$bin_path" "$INSTALL_DIR/$APP"

echo "$APP installed to $INSTALL_DIR/$APP"

if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]] && [[ "$NO_MODIFY_PATH" != true ]]; then
  echo
  echo "Add to PATH:"
  echo "  export PATH=$INSTALL_DIR:\$PATH"
fi

echo
"$INSTALL_DIR/$APP" version || true
