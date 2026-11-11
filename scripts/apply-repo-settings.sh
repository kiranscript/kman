#!/bin/sh
# Apply the repository settings the release pipeline relies on (SPEC §15).
# A maintainer runs this; CI never changes repo settings.
#
#   sh scripts/apply-repo-settings.sh            # show what it would do
#   sh scripts/apply-repo-settings.sh --apply    # do it
#
# It needs gh, logged in as a repo admin. It is safe to run again: each
# setting is set, and each ruleset is created or replaced by name.
#
#   1. Immutable releases: a published release's assets and tag never change,
#      and GitHub attests each release. Turn this on before the first tag.
#   2. "Require actions to be pinned to a full-length commit SHA".
#   3. The tag rulesets in .github/rulesets/: only admins create v* tags, and
#      nobody moves or deletes one. Rulesets on a private repo need GitHub
#      Pro, so on the free plan this step works only once the repo is public.
#   4. Let Actions open pull requests, for the weekly price-data job. Workflow
#      tokens stay read-only by default; that job asks for write itself.
#   5. Private vulnerability reporting, which SECURITY.md sends reporters to.
set -eu

repo=${KMAN_REPO:-kirank55/kman}
apply=false
case ${1:-} in
--apply) apply=true ;;
"") ;;
*)
	echo "usage: apply-repo-settings.sh [--apply]" >&2
	exit 2
	;;
esac

cd "$(dirname "$0")/.."
command -v gh >/dev/null 2>&1 || {
	echo "gh is required" >&2
	exit 1
}

run() {
	echo "+ $*"
	if $apply; then "$@"; fi
}

echo "== $repo: immutable releases"
echo "now: $(gh api "repos/$repo/immutable-releases" --jq .enabled 2>/dev/null || echo unknown)"
run gh api -X PUT "repos/$repo/immutable-releases" --silent

echo "== $repo: require SHA-pinned actions"
allowed=$(gh api "repos/$repo/actions/permissions" --jq .allowed_actions)
echo "now: sha_pinning_required=$(gh api "repos/$repo/actions/permissions" --jq .sha_pinning_required)"
run gh api -X PUT "repos/$repo/actions/permissions" --silent \
	-F enabled=true -f allowed_actions="$allowed" -F sha_pinning_required=true

echo "== $repo: Actions may open pull requests"
echo "now: can_approve_pull_request_reviews=$(gh api "repos/$repo/actions/permissions/workflow" --jq .can_approve_pull_request_reviews)"
run gh api -X PUT "repos/$repo/actions/permissions/workflow" --silent \
	-f default_workflow_permissions=read -F can_approve_pull_request_reviews=true

echo "== $repo: private vulnerability reporting"
now=$(gh api "repos/$repo/private-vulnerability-reporting" --jq .enabled 2>/dev/null) || now=unknown
case $now in true | false) ;; *) now=unknown ;; esac
echo "now: $now"
run gh api -X PUT "repos/$repo/private-vulnerability-reporting" --silent

for file in .github/rulesets/release-tags-create.json .github/rulesets/release-tags-locked.json; do
	name=$(sed -n 's/^  "name": "\(.*\)",$/\1/p' "$file")
	echo "== $repo: ruleset \"$name\" ($file)"
	id=$(gh api "repos/$repo/rulesets" --jq ".[] | select(.name == \"$name\") | .id" 2>/dev/null) || {
		echo "cannot read rulesets; on the free plan, make the repo public first" >&2
		$apply && exit 1
		continue
	}
	if [ -n "$id" ]; then
		run gh api -X PUT "repos/$repo/rulesets/$id" --input "$file" --silent
	else
		run gh api -X POST "repos/$repo/rulesets" --input "$file" --silent
	fi
done

$apply || echo "dry run: nothing changed; run again with --apply"
