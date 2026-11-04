#!/bin/sh
# Print the CHANGELOG.md section for a release tag, which becomes the
# release notes (SPEC §15, release checklist step 4).
#
#   sh scripts/release-notes.sh v0.1.0 [CHANGELOG.md]
#
# The section is the one headed "## [0.1.0]" (Keep a Changelog). It fails if
# the tag is not a semver v-tag, or the section is missing or empty.
set -eu

tag=${1:?usage: release-notes.sh <tag> [changelog]}
file=${2:-CHANGELOG.md}

if ! printf '%s\n' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'; then
	echo "release-notes: $tag is not a semver tag like v0.1.0 or v0.1.0-rc.1" >&2
	exit 1
fi
version=${tag#v}

notes=$(awk -v want="## [$version]" '
	index($0, want) == 1 { on = 1; found = 1; next }
	on && /^## / { exit }
	on && /^\[[^]]+\]: / { exit }
	on { print }
	END { if (!found) exit 3 }
' "$file") || {
	echo "release-notes: $file has no \"## [$version]\" section" >&2
	exit 1
}

# Trim the blank lines around the section.
notes=$(printf '%s\n' "$notes" | awk 'NF { found = 1 } found' | awk '
	{ lines[NR] = $0 }
	END { n = NR; while (n > 0 && lines[n] ~ /^[[:space:]]*$/) n--; for (i = 1; i <= n; i++) print lines[i] }
')
if [ -z "$notes" ]; then
	echo "release-notes: the \"## [$version]\" section of $file is empty" >&2
	exit 1
fi
printf '%s\n' "$notes"
