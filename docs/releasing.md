# Releasing kman

For the maintainer. The checklist is in SPEC §15. This page covers the machinery and the one-time setup.

## One-time setup

Run `sh scripts/apply-repo-settings.sh` to see what it changes, then run it again with `--apply`. It turns on:

- **Immutable releases.** These must be on before the first tag. The post-release job's `gh release verify` fails without them.
- **Require SHA-pinned actions.** `tools/releasecheck` already fails CI on an unpinned action. This setting makes GitHub refuse to run one too.
- **The tag rulesets** in `.github/rulesets/`. Only admins may create a `v*` tag, and no one may move or delete one. Bypass actors skip every rule in a ruleset, so this takes two rulesets. On the free plan, rulesets need a public repo, so for `v0.1.0` the order is: make the repo public, apply the settings, push the tag.

## What a tag does

Pushing `vX.Y.Z` or `vX.Y.Z-rc.N` runs `.github/workflows/release.yml`:

1. **release.** Runs govulncheck and the tag's `CHANGELOG.md` section (the `## [X.Y.Z]` heading), which becomes the release notes. Then GoReleaser (`.goreleaser.yaml`) builds the five archives, the source archive, an SPDX 2.3 SBOM for each, and `checksums.txt` signed by cosign. It pushes the image `ghcr.io/kirank55/kman`, signed by digest, all into a draft release. The job attests the archives, the SBOMs and the image, attaches every attestation as `kman_<version>.intoto.jsonl`, and publishes the draft. `latest` (the image tag and the GitHub release) goes only to a tag without `-`.
2. **verify.** Runs the commands in `docs/verify.md` between `verify:begin` and `verify:end`, exactly as written (`scripts/verify-release.sh`).
3. **rebuild.** Rebuilds the tag from a fresh checkout and compares its archives with the published `checksums.txt` (`scripts/compare-checksums.sh`).
4. **install.** Runs `install.sh` from the tag on Linux and macOS with `KMAN_REQUIRE_SIGNATURE=1`, once with gh and once with cosign, plus a plain install of the latest stable release.

A pull request that touches the release config runs `.github/workflows/release-snapshot.yml`. It is the same GoReleaser run in snapshot mode, unsigned and unpublished, and it checks that the image runs as UID 65532.

## Building locally

```sh
goreleaser release --snapshot --clean --skip=sign,docker
```

You need the GoReleaser version pinned in `release.yml`, and Syft for the SBOMs (or add `sbom` to `--skip`). `dist/` is git-ignored. A build output inside the tree would mark the build as modified and change its bytes.

## Changing the pipeline

`tools/releasecheck` holds the release config to its rules: SHA pins, read-only defaults, harden-runner, the five targets, reproducibility, one signature, an SBOM and attestation for every archive, the image, and the order of draft and publish. If you change a rule on purpose, change its test in the same PR.
