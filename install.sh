#!/bin/sh
set -e

REPO="maniginam/waggle"
INSTALL_DIR="/usr/local/bin"
BINARY="waggle"

# Detect OS
OS="$(uname -s)"
case "$OS" in
  Darwin) OS="darwin" ;;
  Linux)  OS="linux" ;;
  *)
    echo "Error: unsupported OS: $OS"
    exit 1
    ;;
esac

# Detect architecture
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
  *)
    echo "Error: unsupported architecture: $ARCH"
    exit 1
    ;;
esac

URL="https://github.com/${REPO}/releases/latest/download/waggle-${OS}-${ARCH}"

echo "Downloading waggle for ${OS}/${ARCH}..."

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

if ! curl -fsSL -o "${TMPDIR}/${BINARY}" "$URL"; then
  echo "Error: failed to download from $URL"
  exit 1
fi

chmod +x "${TMPDIR}/${BINARY}"

# Install to /usr/local/bin if possible, otherwise ~/bin
if [ -w "$INSTALL_DIR" ] || [ "$(id -u)" = "0" ]; then
  mv "${TMPDIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
  echo "Installed to ${INSTALL_DIR}/${BINARY}"
else
  INSTALL_DIR="${HOME}/bin"
  mkdir -p "$INSTALL_DIR"
  mv "${TMPDIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
  echo "Installed to ${INSTALL_DIR}/${BINARY}"
  case ":$PATH:" in
    *":${INSTALL_DIR}:"*) ;;
    *) echo "Note: add ${INSTALL_DIR} to your PATH" ;;
  esac
fi

# Verify
if "${INSTALL_DIR}/${BINARY}" --version >/dev/null 2>&1; then
  echo "waggle $(${INSTALL_DIR}/${BINARY} --version) installed successfully"
else
  echo "Warning: waggle installed but --version check failed"
fi
