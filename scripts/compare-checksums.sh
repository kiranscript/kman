#!/bin/sh
# Compare a rebuild's checksums with the published checksums.txt (SPEC §15,
# release checklist step 6). Every file the rebuild made must be in the
# published file with the same SHA-256. SBOMs are not rebuilt: Syft stamps
# them with a time and a random namespace.
#
#   sh scripts/compare-checksums.sh <rebuilt checksums.txt> <published checksums.txt>
set -eu

rebuilt=${1:?usage: compare-checksums.sh <rebuilt> <published>}
published=${2:?usage: compare-checksums.sh <rebuilt> <published>}

[ -s "$rebuilt" ] || {
	echo "compare-checksums: $rebuilt is empty; nothing was rebuilt" >&2
	exit 1
}
differs=$(grep -vxFf "$published" "$rebuilt" || true)
if [ -n "$differs" ]; then
	echo "compare-checksums: the rebuild differs from the release:" >&2
	printf '%s\n' "$differs" >&2
	exit 1
fi
echo "compare-checksums: $(grep -c . "$rebuilt") rebuilt files match the release"
