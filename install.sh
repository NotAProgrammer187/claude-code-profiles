#!/bin/sh
# install.sh - one-line installer for ccswitch on macOS and Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/NotAProgrammer187/claude-code-profiles/main/install.sh | sh
#
# Downloads the prebuilt binary for this platform from the latest GitHub
# release, verifies its SHA256 checksum, and installs it as ~/.local/bin/ccswitch
# (override with CCSWITCH_BIN_DIR). No Go, no git, no build step.

set -eu

repo="NotAProgrammer187/claude-code-profiles"
bin_dir="${CCSWITCH_BIN_DIR:-$HOME/.local/bin}"

info() { printf '%s\n' "$*"; }
die()  { printf 'install.sh: %s\n' "$*" >&2; exit 1; }

# --- Platform ------------------------------------------------------------

os=$(uname -s)
arch=$(uname -m)
case "$os" in
    Darwin)
        case "$arch" in
            arm64)         asset="ccswitch-darwin-arm64" ;;
            x86_64|amd64)  asset="ccswitch-darwin-amd64" ;;
            *) die "no prebuilt binary for macOS/$arch - build from source with 'go build ./cmd/ccswitch'" ;;
        esac ;;
    Linux)
        case "$arch" in
            x86_64|amd64)  asset="ccswitch-linux-amd64" ;;
            *) die "no prebuilt binary for Linux/$arch - build from source with 'go build ./cmd/ccswitch'" ;;
        esac ;;
    *) die "unsupported OS $os - on Windows, use install.ps1 instead" ;;
esac

command -v curl >/dev/null 2>&1 || die "curl is required"

# sha256sum on Linux, shasum on macOS.
if command -v sha256sum >/dev/null 2>&1; then
    sha() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
    sha() { shasum -a 256 "$1" | awk '{print $1}'; }
else
    die "need sha256sum or shasum to verify the download"
fi

# --- Download ------------------------------------------------------------

info "Finding the latest release..."
release_json=$(curl -fsSL -H "Accept: application/vnd.github+json" \
    "https://api.github.com/repos/$repo/releases/latest") ||
    die "could not reach GitHub releases for $repo"

tag=$(printf '%s' "$release_json" | grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*"\([^"]*\)"$/\1/')
[ -n "$tag" ] || die "could not read the latest release tag"

base="https://github.com/$repo/releases/download/$tag"
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

info "Downloading $asset ($tag)..."
curl -fsSL -o "$tmp_dir/$asset" "$base/$asset" || die "download failed for $asset"

# --- Checksum ------------------------------------------------------------

# Releases before the manifest existed have nothing to verify against, so a
# missing SHA256SUMS is a loud warning; once one is present, any mismatch
# stops the install.
if curl -fsSL -o "$tmp_dir/SHA256SUMS" "$base/SHA256SUMS" 2>/dev/null; then
    expected=$(awk -v name="$asset" '{ f=$2; sub(/^\*/, "", f); if (f == name) print tolower($1) }' \
        "$tmp_dir/SHA256SUMS" | head -1)
    [ -n "$expected" ] || die "SHA256SUMS in release $tag does not list $asset - refusing to install"
    actual=$(sha "$tmp_dir/$asset")
    [ "$actual" = "$expected" ] ||
        die "checksum mismatch for $asset (got $actual, want $expected) - refusing to install"
    info "SHA256 checksum verified."
else
    info "Warning: release $tag has no SHA256SUMS manifest - checksum not verified."
fi

# --- Install -------------------------------------------------------------

mkdir -p "$bin_dir"
install -m 0755 "$tmp_dir/$asset" "$bin_dir/ccswitch" 2>/dev/null ||
    { cp "$tmp_dir/$asset" "$bin_dir/ccswitch" && chmod 0755 "$bin_dir/ccswitch"; }
info "Installed to $bin_dir/ccswitch"

case ":$PATH:" in
    *":$bin_dir:"*) ;;
    *) info "Note: $bin_dir is not on your PATH - add this to your shell profile:"
       info "  export PATH=\"$bin_dir:\$PATH\"" ;;
esac

if ! command -v claude >/dev/null 2>&1; then
    info "Note: Claude Code (the 'claude' command) was not found on PATH."
    info "ccswitch launches Claude Code, so install it first:"
    info "  https://docs.claude.com/en/docs/claude-code/setup"
fi

info "Done. Run: ccswitch"
