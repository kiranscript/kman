# Releasing kman

For the maintainer. The checklist is in SPEC §15. This page covers the machinery, the one-time setup, and the exact commands to cut `v0.1.0`.

## Cutting v0.1.0

Everything in the repository is ready: the `v0.1.0` fixture (`internal/store/fixtures/v0.1.0.db`), the final `## [0.1.0]` section of `CHANGELOG.md`, and the release pipeline, which was dry-run locally with GoReleaser (see "Building locally"). What is left needs your GitHub account: making the repository public, its settings, the Homebrew tap, and pushing the tag. Nothing here can be undone cheaply. A `v*` tag cannot be moved or deleted once the rulesets are on, and a published release is immutable, so a mistake after the push means a new tag (`v0.1.1`). Do the steps in order.

### 0. Decisions only you can make

- **Repository name.** The tree says `kirank55/kman` everywhere. If the repository is now (or will be) `kiranscript/kman`, or anything else, decide before the tag: the signing identity (`https://github.com/<owner>/<name>/.github/workflows/release.yml@refs/tags/<tag>`) is the repository the release ran in, and installers pin it. Step 1 does the rename.
- **A licence.** There is no `LICENSE` file. A public repository without one grants nobody any rights to the code. Archives pick up a `LICENSE*` file when one is committed, and the image has no licence label. Add one before step 3.
- **`last4`.** SPEC §1.3 and the stored credential `last4` disagree (`docs/security-review-v0.1.0.md`, "Open items decided"). Amend the spec, or take `last4` out of schema `0001`, before the tag: the fixture and the schema are frozen by it.
- **Date.** The changelog heading says `## [0.1.0] - 2026-11-11`. Change the date if you tag on another day (the section is the release notes either way). If you do, nothing else changes.

### 1. Name the repository, in one command

Skip this if the name stays `kirank55/kman`. Otherwise rename or transfer the repository on GitHub, then:

```sh
export REPO=kiranscript/kman                    # the new owner/name, used by every command below
git remote set-url origin "https://github.com/$REPO.git"
sh scripts/set-repo.sh "$REPO"                  # rewrites every place listed below
git diff --stat                                 # read it
go build ./... && go test ./... && go -C tools test ./...
git commit -am "Rename the repository to $REPO"       # then a pull request into main, like any change
```

`scripts/set-repo.sh` reads the current name from the module path in `go.mod` and replaces it, and the owner in `<owner>/homebrew-tap` and `<owner>/tap/kman`, in every tracked text file. `tools/releasecheck` runs it on a copy of the tree, builds the copy, and fails if the old owner is still named anywhere, and if the list below is missing a file. GoReleaser takes the repository from the `origin` remote, so `.goreleaser.yaml` names neither owner nor name. The places that name the repository:

- Go: `go.mod`, `tools/go.mod`, `gates/testdata/planted/go.mod` and every `.go` file (the module path in imports, plus `e2e` and `tools/releasecheck` tests that pin URLs and the signing identity).
- Release machinery: `.github/workflows/release.yml` (the image, the tap checkout, the identity in the header), `.github/workflows/release-snapshot.yml`, `scripts/apply-repo-settings.sh` (the default `KMAN_REPO`), `scripts/homebrew-formula.sh`, `install.sh` (the repository it downloads from and the identity it pins), `packaging/kman.service`, `examples/compose/compose.yaml`.
- Docs: `README.md`, `SECURITY.md`, `SPEC.md`, `AGENTS.md`, `CHANGELOG.md`, `docs/verify.md`, `docs/releasing.md`, `docs/quickstart.md`, `docs/v0.1.0-scope.md`, `docs/operator-guide/install.md`, `docs/operator-guide/docker.md`, `docs/operator-guide/systemd.md`.
- Not in the tree, and the script cannot touch: the GitHub repository name itself, the `homebrew-tap` repository (step 5), the Actions secrets, the local `git remote`. GitHub redirects the old name after a rename, but `go install`, the signing identity and `ghcr.io/<owner>/kman` do not follow a redirect, so do not rely on it.
- Not changed on purpose: `maintainer: kirank55 <kirank55@users.noreply.github.com>` in `.goreleaser.yaml` is a person's noreply address, not the repository. Edit it by hand if the person changes.

Then merge the work onto `main`: PR #57 (`m1-contain` into `main`) and this ticket's branch. CI has not run on the branch yet, so expect to fix what it finds: every job, including `systemd-creds`, `sdk-e2e` and the release snapshot, must be green on `main`. SPEC §15 step 1.

### 2. Last checks on `main`

```sh
git switch main && git pull --ff-only
sh scripts/check-fixture.sh v0.1.0         # the fixture is committed
sh scripts/release-notes.sh v0.1.0         # the notes the release will carry
go test ./... && go -C tools test ./...
go build -o /tmp/kman ./cmd/kman
sh scripts/record-network.sh /tmp/kman && sh scripts/record-network.sh /tmp/kman --control   # Linux, no root, once per release
```

The live suite (SPEC §15 step 2) does not exist in the repository yet, so there is nothing to run. The 7 medium and 7 low findings in `docs/security-review-v0.1.0.md` have no issues filed yet.

### 3. Make the repository public

```sh
gh repo edit "$REPO" --visibility public --accept-visibility-change-consequences
```

This must come before step 4: on the free plan rulesets need a public repository. Check what is about to become public first (`git log`, the issues, the Actions logs).

### 4. Apply the repository settings

```sh
KMAN_REPO="$REPO" sh scripts/apply-repo-settings.sh            # shows what it will do
KMAN_REPO="$REPO" sh scripts/apply-repo-settings.sh --apply
```

This turns on immutable releases, SHA-pinned actions, Actions opening pull requests, private vulnerability reporting (`SECURITY.md` sends reporters there), and the two tag rulesets in `.github/rulesets/`. Immutable releases must be on before the tag: the post-release job's `gh release verify` fails without them. Re-run it without `--apply`: each `now:` line should read `true` or the ruleset name.

### 5. The Homebrew tap and its deploy key

The `homebrew` job pushes the formula to a tap. Nothing in this repository creates it:

```sh
OWNER=${REPO%%/*}
gh repo create "$OWNER/homebrew-tap" --public --add-readme --description "Homebrew tap for kman"
ssh-keygen -t ed25519 -N "" -C "kman release" -f homebrew-tap-deploy
gh repo deploy-key add homebrew-tap-deploy.pub -R "$OWNER/homebrew-tap" --title "kman release" --allow-write
gh secret set HOMEBREW_TAP_DEPLOY_KEY -R "$REPO" < homebrew-tap-deploy
rm -f homebrew-tap-deploy homebrew-tap-deploy.pub
```

`--add-readme` gives the tap its first commit; the job checks it out, and an empty repository has nothing to check out. A deploy key reaches one repository, unlike a personal access token, and does not expire. Without the tap or the secret, the `homebrew` job fails and `brew` with it; the release itself is already published by then.

### 6. Optional: let price-data pull requests run CI

Pull requests the weekly price-data job opens use the default token, so CI does not run on them. To change that, make a fine-grained token for this repository with Contents and Pull requests write, then:

```sh
gh secret set PRICE_DATA_TOKEN -R "$REPO"
```

Skip it and close and reopen such a pull request to run CI.

### 7. Push the tag

Only you can: the `v*` ruleset lets admins create tags and no one move or delete one.

```sh
git switch main && git pull --ff-only
git status --short                  # must print nothing
git tag -a v0.1.0 -m "kman v0.1.0"
git push origin v0.1.0
gh run watch -R "$REPO" "$(gh run list -R "$REPO" -w Release -L 1 --json databaseId --jq '.[0].databaseId')"
```

`release.yml` runs: the release job (the fixture check, then notes, govulncheck, GoReleaser, attestations, publishing), then `verify`, `rebuild`, `install`, `homebrew` and `brew` (see "What a tag does"). The `v0.1.0` fixture check fails the job first if the tag's commit has no fixture.

### 8. Verify, and close out

- The post-release jobs must all be green: `gh run list -R "$REPO" -w Release -L 1`. This is the "post-release verification job is green" criterion.
- Run `docs/verify.md` yourself once, from a different machine if you can, against `v0.1.0`.
- `curl -fsSL https://raw.githubusercontent.com/$REPO/main/install.sh | sh` and `brew install ${REPO%%/*}/tap/kman` now work. Until the tag existed, the install URLs in the docs returned 404.
- SPEC §15 step 7: publish the benchmarks at the tag to `docs/benchmarks/v0.1.0.md` (the CI `bench` job runs `go test -bench . -benchmem -count 10 ./...`). Nothing publishes them yet.
- Close #55 and #27 (M1 done criteria: "`v0.1.0` is published, the post-release verification job is green, and the repo is public").

If the release job fails before it publishes, the draft release can be deleted, but the tag cannot be moved: fix the cause and tag `v0.1.1`. If `homebrew` or `brew` fails (a missing tap or key), fix that and re-run only that job; the release is already published.

## One-time setup

The settings and the tap are steps 4 and 5 above. In summary, `scripts/apply-repo-settings.sh` turns on:

- **Immutable releases.** These must be on before the first tag.
- **Require SHA-pinned actions.** `tools/releasecheck` already fails CI on an unpinned action. This setting makes GitHub refuse to run one too.
- **The tag rulesets** in `.github/rulesets/`. Only admins may create a `v*` tag, and no one may move or delete one. Bypass actors skip every rule in a ruleset, so this takes two rulesets. On the free plan, rulesets need a public repo.

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
