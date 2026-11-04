#!/bin/sh
# Install kman from a GitHub release (SPEC §15).
#
#   curl -fsSL https://raw.githubusercontent.com/kirank55/kman/main/install.sh | sh
#
# It always checks the archive's SHA-256 against the release's checksums.txt,
# then verifies the release's signature with cosign or, failing that, gh,
# against the exact identity of release.yml at the tag. Without either it
# installs with a loud warning, unless KMAN_REQUIRE_SIGNATURE=1, which makes
# it refuse.
#
# Environment:
#   KMAN_VERSION            a tag such as v0.1.0 (default: the latest stable release)
#   KMAN_INSTALL_DIR        where kman goes (default: /usr/local/bin if writable,
#                           else ~/.local/bin)
#   KMAN_REQUIRE_SIGNATURE  1 refuses to install unless a signature verified
#   KMAN_RELEASES_URL       the releases base URL, for a mirror or a test
#                           (default: https://github.com/kirank55/kman/releases);
#                           the signature identity stays pinned to kirank55/kman
#
# On Windows, download the zip from the release page instead.
set -eu

repo=kirank55/kman
releases=${KMAN_RELEASES_URL:-https://github.com/$repo/releases}
issuer=https://token.actions.githubusercontent.com

# HTTPS only, redirects included, unless KMAN_RELEASES_URL is plain http.
proto='=https'
case $releases in http://*) proto='=http,https' ;; esac

say() { printf 'kman install: %s\n' "$*" >&2; }
die() {
	say "error: $*"
	exit 1
}

need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required"; }
need curl
need tar
need uname
need mktemp

case $(uname -s) in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) die "unsupported OS $(uname -s); on Windows, download the zip from $releases" ;;
esac
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) die "unsupported architecture $(uname -m)" ;;
esac

tag=${KMAN_VERSION:-}
if [ -z "$tag" ]; then
	# /releases/latest redirects to the latest stable release's page.
	latest=$(curl -fsSLI --proto "$proto" --proto-redir "$proto" -o /dev/null -w '%{url_effective}' "$releases/latest") ||
		die "could not find the latest release at $releases/latest"
	tag=${latest##*/}
fi
printf '%s\n' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' ||
	die "\"$tag\" is not a release tag like v0.1.0"
version=${tag#v}
identity="https://github.com/$repo/.github/workflows/release.yml@refs/tags/$tag"

archive="kman_${version}_${os}_${arch}.tar.gz"
bundle="kman_${version}.intoto.jsonl"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

fetch() {
	curl -fsSL --proto "$proto" --proto-redir "$proto" -o "$tmp/$1" "$releases/download/$tag/$1" ||
		die "could not download $1 for $tag"
}

say "installing kman $tag ($os/$arch)"
fetch "$archive"
fetch checksums.txt

# 1. The checksum, always.
want=$(awk -v f="$archive" '$2 == f || $2 == "*" f { print $1; exit }' "$tmp/checksums.txt")
[ -n "$want" ] || die "checksums.txt lists no checksum for $archive"
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/$archive" | awk '{ print $1 }')
elif command -v shasum >/dev/null 2>&1; then
	got=$(shasum -a 256 "$tmp/$archive" | awk '{ print $1 }')
else
	die "sha256sum or shasum is required to check the download"
fi
[ "$got" = "$want" ] || die "checksum mismatch for $archive: got $got, want $want"
say "checksum OK"

# 2. The signature, with the first verifier present. A failed check is final.
has_gh_attestation() {
	command -v gh >/dev/null 2>&1 && gh attestation verify --help >/dev/null 2>&1
}
verified=
if command -v cosign >/dev/null 2>&1; then
	fetch checksums.txt.sigstore.json
	cosign verify-blob "$tmp/checksums.txt" \
		--bundle "$tmp/checksums.txt.sigstore.json" \
		--certificate-identity "$identity" \
		--certificate-oidc-issuer "$issuer" >&2 ||
		die "cosign could not verify checksums.txt as signed by $identity"
	verified=cosign
elif has_gh_attestation; then
	fetch "$bundle"
	gh attestation verify "$tmp/$archive" \
		--repo "$repo" \
		--bundle "$tmp/$bundle" \
		--cert-identity "$identity" \
		--cert-oidc-issuer "$issuer" \
		--deny-self-hosted-runners >&2 ||
		die "gh could not verify $archive as built by $identity"
	verified=gh
fi

if [ -n "$verified" ]; then
	say "signature OK ($verified, $identity)"
elif [ "${KMAN_REQUIRE_SIGNATURE:-}" = 1 ]; then
	die "KMAN_REQUIRE_SIGNATURE=1, but neither cosign nor gh (with 'gh attestation') is installed"
else
	cat >&2 <<EOF

  ##################################################################
  WARNING: the kman release signature was NOT verified.
  Neither cosign nor gh (2.49 or later, with 'gh attestation') is
  installed, so only the checksum was checked, and the checksum
  comes from the same release. Install cosign or gh and run this
  again, or set KMAN_REQUIRE_SIGNATURE=1 to refuse unverified installs.
  ##################################################################

EOF
fi

# 3. Install.
tar -xzf "$tmp/$archive" -C "$tmp" kman || die "could not unpack $archive"
dir=${KMAN_INSTALL_DIR:-}
if [ -z "$dir" ]; then
	if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
		dir=/usr/local/bin
	else
		dir=$HOME/.local/bin
	fi
fi
mkdir -p "$dir" || die "could not create $dir"
chmod 755 "$tmp/kman"
mv -f "$tmp/kman" "$dir/kman" || die "could not write $dir/kman"
say "installed $dir/kman"

case ":$PATH:" in
*":$dir:"*) ;;
*) say "$dir is not on your PATH; add it, for example: export PATH=\"$dir:\$PATH\"" ;;
esac
