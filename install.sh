#!/bin/sh
set -e

REPO="prastuvwxyz/memgraph"
BIN="memgraph"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

# Detect OS
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  linux)  OS="linux" ;;
  darwin) OS="darwin" ;;
  *) echo "Unsupported OS: $OS" && exit 1 ;;
esac

# Detect arch
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64 | amd64) ARCH="amd64" ;;
  aarch64 | arm64) ARCH="arm64" ;;
  *) echo "Unsupported arch: $ARCH" && exit 1 ;;
esac

# macOS ships a universal binary (works on both arm64 + amd64)
if [ "$OS" = "darwin" ]; then
  ARCH="all"
fi

# Get latest version tag
VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | cut -d '"' -f 4)"
if [ -z "$VERSION" ]; then
  echo "Failed to fetch latest version" && exit 1
fi

ARCHIVE="${BIN}_${VERSION}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}"

echo "Installing ${BIN} ${VERSION} (${OS}/${ARCH}) to ${INSTALL_DIR}..."

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

curl -fsSL "$URL" | tar xz -C "$TMP" "$BIN"

# Install (try without sudo first, fall back to sudo)
if [ -w "$INSTALL_DIR" ]; then
  mv "$TMP/$BIN" "$INSTALL_DIR/$BIN"
else
  sudo mv "$TMP/$BIN" "$INSTALL_DIR/$BIN"
fi

echo "Done. Run: ${BIN} --help"
