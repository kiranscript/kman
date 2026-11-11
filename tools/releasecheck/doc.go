// Package releasecheck holds the release pipeline's policy gate (SPEC §15).
// Its tests read the committed release configuration (the workflows, the
// GoReleaser config, the Dockerfile, Dependabot and the tag ruleset) and fail
// when a rule from issue #52 is broken, so a change that weakens the pipeline
// fails CI before it reaches a tag.
package releasecheck
