#!/bin/sh
# Fail unless a release tag has its store fixture (SPEC §14 and §15, release
# checklist step 3): internal/store/fixtures/<tag>.db, a SQLite database from
# the deterministic generator, committed before the tag is pushed.
#
#   sh scripts/check-fixture.sh v0.1.0 [fixtures-dir]
#
# Make one with: go run ./internal/store/fixturegen --version v0.1.0
# The release job runs this before it builds anything.
set -eu

tag=${1:?usage: check-fixture.sh <tag> [fixtures-dir]}
dir=${2:-internal/store/fixtures}

if ! printf '%s\n' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$'; then
	echo "check-fixture: $tag is not a release tag like v0.1.0 or v0.1.0-rc.1" >&2
	exit 1
fi

file=$dir/$tag.db
if [ ! -s "$file" ]; then
	echo "check-fixture: $file is missing or empty." >&2
	echo "check-fixture: run 'go run ./internal/store/fixturegen --version $tag', commit it, and tag that commit." >&2
	exit 1
fi
if [ "$(head -c 15 "$file")" != "SQLite format 3" ]; then
	echo "check-fixture: $file is not a SQLite database" >&2
	exit 1
fi
echo "check-fixture: $file is committed"
