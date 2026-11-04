#!/bin/sh
# Print the steps a doc gives: the sh blocks between <!-- <name>:begin -->
# and <!-- <name>:end -->. CI runs them as printed, so the docs cannot drift
# from what is tested.
#
#   sh scripts/doc-steps.sh docs/operator-guide/docker.md compose
set -eu

doc=${1:?usage: doc-steps.sh <doc> <name>}
name=${2:?usage: doc-steps.sh <doc> <name>}

awk -v begin="<!-- $name:begin -->" -v end="<!-- $name:end -->" '
	$0 == begin { on = 1; next }
	$0 == end { on = 0; next }
	on && /^```/ { block = !block; next }
	on && block { print; n++ }
	END { if (!n) exit 3 }
' "$doc" || {
	echo "doc-steps: $doc has no sh blocks between <!-- $name:begin --> and <!-- $name:end -->" >&2
	exit 1
}
