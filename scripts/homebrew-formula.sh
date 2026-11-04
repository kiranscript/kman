#!/bin/sh
# Print the Homebrew formula for a release (SPEC §3: a best-effort formula
# in kirank55/homebrew-tap). It installs the release archive for the
# machine, pinned by the SHA-256 in the release's checksums.txt, so a
# formula Homebrew accepts is one cosign already covers.
#
#   sh scripts/homebrew-formula.sh v0.1.0 checksums.txt > Formula/kman.rb
#
# release.yml runs it once the verify job has checked the signature. A
# formula, not a cask: Homebrew does not quarantine what a formula
# installs, and kman is not notarized.
set -eu

tag=${1:?usage: homebrew-formula.sh <tag> <checksums.txt>}
checksums=${2:?usage: homebrew-formula.sh <tag> <checksums.txt>}

if ! printf '%s\n' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'; then
	echo "homebrew-formula: $tag is not a semver tag like v0.1.0" >&2
	exit 1
fi
version=${tag#v}

# archive prints the url and sha256 lines for one target's archive.
archive() {
	name="kman_${version}_$1.tar.gz"
	sum=$(awk -v name="$name" '$2 == name { print $1 }' "$checksums")
	if ! printf '%s\n' "$sum" | grep -Eqx '[0-9a-f]{64}'; then
		echo "homebrew-formula: $checksums has no SHA-256 for $name" >&2
		exit 1
	fi
	printf '      url "https://github.com/kirank55/kman/releases/download/%s/%s"\n' "$tag" "$name"
	printf '      sha256 "%s"\n' "$sum"
}

darwin_arm64=$(archive darwin_arm64)
darwin_amd64=$(archive darwin_amd64)
linux_arm64=$(archive linux_arm64)
linux_amd64=$(archive linux_amd64)

cat <<FORMULA
# Written by kirank55/kman's release workflow for $tag, from the release's
# signed checksums.txt. The next release replaces it; do not edit.
class Kman < Formula
  desc "Key manager and proxy for LLM API keys"
  homepage "https://github.com/kirank55/kman"
  version "$version"

  on_macos do
    on_arm do
$darwin_arm64
    end
    on_intel do
$darwin_amd64
    end
  end

  on_linux do
    on_arm do
$linux_arm64
    end
    on_intel do
$linux_amd64
    end
  end

  def install
    bin.install "kman"
  end

  test do
    assert_equal "kman v#{version}", shell_output("#{bin}/kman version").strip
  end
end
FORMULA
