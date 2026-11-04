# Releasing kman

For the maintainer. The checklist is in SPEC §15. This page covers the machinery and the one-time setup.

## One-time setup

Run `sh scripts/apply-repo-settings.sh` to see what it changes, then run it again with `--apply`. It turns on:

- **Immutable releases.** These must be on before the first tag. The post-release job's `gh release verify` fails without them.
- **Require SHA-pinned actions.** `tools/releasecheck` already fails CI on an unpinned action. This setting makes GitHub refuse to run one too.
- **The tag rulesets** in `.github/rulesets/`. Only admins may create a `v*` tag, and no one may move or delete one. Bypass actors skip every rule in a ruleset, so this takes two rulesets. On the free plan, rulesets need a public repo, so for `v0.1.0` the order is: make the repo public, apply the settings, push the tag.

The Homebrew formula needs a tap and a key to push to it. Nothing in this repo creates them; do this once before the first stable tag:

1. **Create `kirank55/homebrew-tap`**, public, with a README so it has a first commit (the release job checks it out, and an empty repo has nothing to check out). The formula goes in `Formula/kman.rb`, so users run `brew install kirank55/tap/kman`.
2. **Make a deploy key for the tap alone:** `ssh-keygen -t ed25519 -N "" -C "kman release" -f homebrew-tap-deploy`. Add `homebrew-tap-deploy.pub` to `kirank55/homebrew-tap` under Settings → Deploy keys, with **Allow write access**.
3. **Store the private key** as the `HOMEBREW_TAP_DEPLOY_KEY` Actions secret of `kirank55/kman`: `gh secret set HOMEBREW_TAP_DEPLOY_KEY -R kirank55/kman < homebrew-tap-deploy`. Then delete both key files.

A deploy key reaches one repository, unlike a personal access token, and it does not expire. Without the tap or the secret, the `homebrew` job fails and `brew` with it; the release itself is already published by then.

## What a tag does

Pushing `vX.Y.Z` or `vX.Y.Z-rc.N` runs `.github/workflows/release.yml`:

1. **release.** Runs govulncheck and the tag's `CHANGELOG.md` section (the `## [X.Y.Z]` heading), which becomes the release notes. Then GoReleaser (`.goreleaser.yaml`) builds the five archives and the source archive with an SPDX 2.3 SBOM for each, the `.deb` and `.rpm` for amd64 and arm64 (with `packaging/kman.service`), and `checksums.txt` signed by cosign. It pushes the image `ghcr.io/kirank55/kman`, signed by digest, all into a draft release. The job attests the archives, the SBOMs and the image, attaches every attestation as `kman_<version>.intoto.jsonl`, and publishes the draft. `latest` (the image tag and the GitHub release) goes only to a tag without `-`.
2. **verify.** Runs the commands in `docs/verify.md` between `verify:begin` and `verify:end`, exactly as written (`scripts/verify-release.sh`).
3. **rebuild.** Rebuilds the tag from a fresh checkout and compares its archives and packages with the published `checksums.txt` (`scripts/compare-checksums.sh`).
4. **install.** Runs `install.sh` from the tag on Linux and macOS with `KMAN_REQUIRE_SIGNATURE=1`, once with gh and once with cosign, plus a plain install of the latest stable release.
5. **homebrew.** For a stable tag, once **verify** has passed: renders `Formula/kman.rb` from the published `checksums.txt` (`scripts/homebrew-formula.sh`) and pushes it to `kirank55/homebrew-tap` with the deploy key.
6. **brew.** On macOS, `brew install kirank55/tap/kman`, checks that nothing it installed carries `com.apple.quarantine`, and runs the formula's test.

A pull request that touches the release config runs `.github/workflows/release-snapshot.yml`. It is the same GoReleaser run in snapshot mode, unsigned and unpublished, and it checks that the image runs as UID 65532. It also runs the documented setup steps against what it built: the Compose steps in `docs/operator-guide/docker.md` with the snapshot image, and the systemd steps in `docs/operator-guide/systemd.md` with the `.deb` in a container booted with systemd. It installs the `.rpm` on Fedora and renders the formula from the snapshot's checksums.

## Building locally

```sh
goreleaser release --snapshot --clean --skip=sign,docker
```

You need the GoReleaser version pinned in `release.yml`, and Syft for the SBOMs (or add `sbom` to `--skip`). The packages build without Docker. `dist/` is git-ignored. A build output inside the tree would mark the build as modified and change its bytes.

## Changing the pipeline

`tools/releasecheck` holds the release config to its rules: SHA pins, read-only defaults, harden-runner, the five targets, reproducibility, one signature, an SBOM and attestation for every archive, the image, and the order of draft and publish. If you change a rule on purpose, change its test in the same PR.
