#!/bin/sh
# Run the documented verify commands against a published release (SPEC §15,
# release checklist step 6). The commands are the sh blocks between
# <!-- verify:begin --> and <!-- verify:end --> in docs/verify.md, so what
# operators are told to run is exactly what the post-release job checks.
#
#   sh scripts/verify-release.sh v0.1.0 [docs/verify.md]
#
# The blocks see TAG (v0.1.0) and V (0.1.0) and run in a new empty directory.
set -eu

tag=${1:?usage: verify-release.sh <tag> [doc]}
doc=${2:-docs/verify.md}
case $doc in /*) ;; *) doc=$(pwd)/$doc ;; esac

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT INT TERM

{
	echo 'set -eux'
	awk '
		/<!-- verify:begin -->/ { on = 1; next }
		/<!-- verify:end -->/ { on = 0; next }
		on && /^```/ { block = !block; next }
		on && block { print; n++ }
		END { if (!n) exit 3 }
	' "$doc" || {
		echo "verify-release: $doc has no commands between verify:begin and verify:end" >&2
		exit 1
	}
} > "$work/verify.sh"

cd "$work"
TAG=$tag V=${tag#v} sh "$work/verify.sh"
