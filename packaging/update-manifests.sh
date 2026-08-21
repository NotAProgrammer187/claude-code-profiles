#!/bin/sh
# Regenerates the package-manager manifests for one release, so the Scoop
# manifest and the Homebrew formula always describe the same binaries the
# GitHub Release carries. Run from the repo root. release.yml calls this after
# computing the asset hashes, and commits whatever changed:
#
#   packaging/update-manifests.sh <version> <sha-exe> <sha-darwin-arm64> <sha-darwin-amd64> <sha-linux-amd64>
#
# The version is bare (0.1.8); only tags and download URLs carry the leading v.
set -eu

if [ $# -ne 5 ]; then
    echo "usage: $0 <version> <sha-exe> <sha-darwin-arm64> <sha-darwin-amd64> <sha-linux-amd64>" >&2
    exit 2
fi

VERSION=$1
SHA_EXE=$2
SHA_DARWIN_ARM64=$3
SHA_DARWIN_AMD64=$4
SHA_LINUX_AMD64=$5
REPO="https://github.com/NotAProgrammer187/claude-code-profiles"

mkdir -p scoop Formula

# Scoop reads this manifest straight from the repo, so installs work without a
# bucket: scoop install <raw URL of this file>. The autoupdate block keeps any
# bucket that copies it in step with future releases.
cat > scoop/ccswitch.json <<EOF
{
    "version": "$VERSION",
    "description": "Switch between multiple Claude Code accounts without logging out",
    "homepage": "$REPO",
    "license": "MIT",
    "url": "$REPO/releases/download/v$VERSION/ccswitch.exe",
    "hash": "$SHA_EXE",
    "bin": "ccswitch.exe",
    "checkver": {
        "github": "$REPO"
    },
    "autoupdate": {
        "url": "$REPO/releases/download/v\$version/ccswitch.exe",
        "hash": {
            "url": "$REPO/releases/download/v\$version/SHA256SUMS"
        }
    }
}
EOF

# Homebrew reads Formula/ from any tapped repo, so this works without a
# separate homebrew-* tap repository:
#   brew tap notaprogrammer187/ccswitch $REPO
#   brew install notaprogrammer187/ccswitch/ccswitch
cat > Formula/ccswitch.rb <<EOF
class Ccswitch < Formula
  desc "Switch between multiple Claude Code accounts without logging out"
  homepage "$REPO"
  version "$VERSION"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "$REPO/releases/download/v$VERSION/ccswitch-darwin-arm64"
      sha256 "$SHA_DARWIN_ARM64"
    else
      url "$REPO/releases/download/v$VERSION/ccswitch-darwin-amd64"
      sha256 "$SHA_DARWIN_AMD64"
    end
  end

  on_linux do
    url "$REPO/releases/download/v$VERSION/ccswitch-linux-amd64"
    sha256 "$SHA_LINUX_AMD64"
  end

  def install
    bin.install Dir["ccswitch*"].first => "ccswitch"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/ccswitch version")
  end
end
EOF

echo "Wrote scoop/ccswitch.json and Formula/ccswitch.rb for v$VERSION"
