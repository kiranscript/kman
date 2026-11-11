#!/bin/sh
# Move kman to another GitHub owner or repository name, in every place the
# tree names it: the Go module paths, URLs, the image and Homebrew tap names,
# and the signing identity. Run it before the tag, never after: a release
# signs as the repository it was built in.
#
#   sh scripts/set-repo.sh kiranscript/kman
#
# The current name is the module path in go.mod. The tap's owner moves with
# the repository's, its name (homebrew-tap) does not. docs/releasing.md lists
# the files this touches, and what it cannot (GitHub itself).
set -eu

new=${1:-}
if ! printf '%s\n' "$new" | grep -Eq '^[a-z0-9]([a-z0-9-]*[a-z0-9])?/[a-z0-9][a-z0-9._-]*$' || [ "${new#*/..}" != "$new" ]; then
	echo "usage: set-repo.sh <owner/name>, lowercase, such as kiranscript/kman (GHCR needs lowercase)" >&2
	exit 1
fi

cd "$(git rev-parse --show-toplevel)"
old=$(sed -n 's#^module github\.com/\([^/ ]*/[^/ ]*\)$#\1#p' go.mod)
if [ -z "$old" ]; then
	echo "set-repo: go.mod has no module github.com/<owner>/<name> line" >&2
	exit 1
fi
old_owner=${old%%/*}
new_owner=${new%%/*}

# Every tracked text file that names the old owner. kirank55/keman, the
# earlier project, is a different repository, and no rule below matches it.
files=$(git grep -lI -F -e "$old" -e "$old_owner/homebrew-tap" -e "$old_owner/tap/" || true)
for f in $files; do
	tmp=$(mktemp)
	sed -e "s#$old#$new#g" \
		-e "s#$old_owner/homebrew-tap#$new_owner/homebrew-tap#g" \
		-e "s#$old_owner/tap/#$new_owner/tap/#g" "$f" >"$tmp"
	if cmp -s "$tmp" "$f"; then
		rm -f "$tmp"
	else
		cat "$tmp" >"$f" # keeps the file's mode
		rm -f "$tmp"
		echo "set-repo: $f"
	fi
done
echo "set-repo: $old -> $new. Review with git diff, then run go test ./... and go -C tools test ./..."
