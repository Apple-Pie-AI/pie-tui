#!/usr/bin/env bash
set -euo pipefail

REPO="Apple-Pie-AI/pie-tui"
INSTALL_DIR="$HOME/.local/bin"

# ── platform detection ────────────────────────────────────────────────────────

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
  x86_64)        ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Error: unsupported architecture: $ARCH"; exit 1 ;;
esac

case "$OS" in
  darwin|linux) ;;
  *) echo "Error: unsupported OS: $OS (Windows is not supported)"; exit 1 ;;
esac

# ── latest release ────────────────────────────────────────────────────────────

echo "Fetching latest release…"
# PIE_VERSION pins a specific release (e.g. a beta prerelease, which GitHub's
# "latest" deliberately never resolves to). Unset = the newest stable release.
if [ -n "${PIE_VERSION:-}" ]; then
  LATEST="$PIE_VERSION"
else
  LATEST=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
    | grep '"tag_name"' \
    | sed -E 's/.*"([^"]+)".*/\1/')
fi

if [ -z "$LATEST" ]; then
  echo "Error: could not determine latest release."
  echo "Check: https://github.com/$REPO/releases"
  exit 1
fi

# ── download ──────────────────────────────────────────────────────────────────

BINARY="pie_${OS}_${ARCH}"
URL="https://github.com/$REPO/releases/download/$LATEST/$BINARY"

mkdir -p "$INSTALL_DIR"

echo "Downloading Apple Pie $LATEST ($OS/$ARCH)…"
# Download to a temp file and rename into place. Never write over the existing
# binary in place: the macOS kernel caches code-signing info per inode, and an
# in-place overwrite leaves a stale cache that kills the new binary on launch
# ("zsh: killed"). A rename swaps in a fresh inode atomically, and also keeps
# a running pie usable during the swap.
TMP="$INSTALL_DIR/.pie.download.$$"
trap 'rm -f "$TMP"' EXIT
curl -fsSL "$URL" -o "$TMP"
chmod +x "$TMP"

# macOS marks curl-downloaded binaries as quarantined; remove the flag so
# Gatekeeper doesn't block the first launch.
if [ "$OS" = "darwin" ]; then
  xattr -d com.apple.quarantine "$TMP" 2>/dev/null || true
fi

mv -f "$TMP" "$INSTALL_DIR/pie"
trap - EXIT

echo "✓ Installed → $INSTALL_DIR/pie"

# ── PATH ──────────────────────────────────────────────────────────────────────

if ! echo ":${PATH}:" | grep -q ":$INSTALL_DIR:"; then
  if [ -f "$HOME/.zshrc" ];          then RC="$HOME/.zshrc"
  elif [ -f "$HOME/.bashrc" ];       then RC="$HOME/.bashrc"
  elif [ -f "$HOME/.bash_profile" ]; then RC="$HOME/.bash_profile"
  else RC="$HOME/.profile"; fi

  echo ""
  echo "  $INSTALL_DIR is not in your PATH. Add it:"
  echo "    echo 'export PATH=\"\$HOME/.local/bin:\$PATH\"' >> $RC && source $RC"
fi

# ── prerequisites ─────────────────────────────────────────────────────────────

echo ""
MISSING=0

if ! command -v claude &>/dev/null; then
  echo "⚠  Claude Code not found."
  echo "   Install it at https://claude.ai/download - required to run Apple Pie."
  MISSING=1
fi

if ! command -v gh &>/dev/null; then
  echo "⚠  GitHub CLI (gh) not found."
  echo "   Install it at https://cli.github.com - required to open pull requests."
  MISSING=1
elif ! gh auth status &>/dev/null 2>&1; then
  echo "⚠  gh is installed but not authenticated."
  echo "   Run: gh auth login"
  MISSING=1
fi

if ! command -v git &>/dev/null; then
  echo "⚠  git not found. Install it from https://git-scm.com/"
  MISSING=1
fi

# ── next step ─────────────────────────────────────────────────────────────────

echo ""
if [ "$MISSING" -eq 0 ]; then
  echo "All prerequisites found. Run:"
else
  echo "Fix the warnings above, then run:"
fi
echo "  pie init"
echo ""
