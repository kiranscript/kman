# Supply-chain practice for a signed Go single-binary release

Ticket: kirank55/kman#21. Question: what is current best practice (late 2026) for releasing a security-sensitive, self-hosted Go single binary from GitHub Actions, and what does an operator actually do to verify it?

- **Date of research:** 2026-11-03.
- **Reference audited:** `kirank55/keman` at commit `3dd4a09` (`.github/workflows/release.yml`, `Dockerfile`, `scripts/install.sh`), plus its published `v0.1.0-beta` release and GHCR image.
- **Sources:** primary sources only, unless a claim is marked **[secondary]** (it rests on a search summary) or **[unverified]** (I could not confirm it in a primary source). Tool versions were read from each project's GitHub releases on the research date.
- **What was run:** I downloaded keman's release bundle, SBOM and checksum, and parsed them as data. I decoded the Fulcio certificate with `openssl`, listed GHCR tags through the anonymous registry API, and ran a Go reproducibility experiment on a toy module (Go 1.27.3). I did not run `cosign` or any keman code. The signature was inspected, not cryptographically verified.

## Answer in brief

- **Do both.** Use keyless cosign signing for operators who don't use GitHub's tools, and GitHub artifact attestations for SLSA provenance. Both are free for a public repo, both use Sigstore's public-good instance, and both are verified against the same workflow identity.
- **SLSA level.** Artifact attestations made in the release workflow reach **SLSA v1.0 Build L2**. Moving the build into a reusable workflow, and having verifiers pin it with `--signer-workflow`, reaches **Build L3**. A cosign signature on its own is not provenance, so it does not claim a Build level.
- **Tooling.** Use GoReleaser (OSS, v2.19) for builds, archives, checksums, SBOMs, cosign signing, the multi-arch image, the Homebrew cask and nfpm packages. Run `actions/attest@v4` after it, on `checksums.txt` and `digests.txt`. This is exactly GoReleaser's own `example-supply-chain` setup.
- **Hygiene.** Pin every action by full SHA (or adopt GitHub's new `actions.lock`). Use Dependabot for `gomod`, `github-actions` and `docker`. Pin the exact Go toolchain. Run `govulncheck` in CI. Turn on immutable releases. Run OpenSSF Scorecard.
- **Correction to the post-mortem (#2).** keman's signed release *did* ship. `v0.1.0-beta` (2026-10-29) carries cosign bundles and SBOMs, and the GHCR image is cosign-signed. The post-mortem's "Scaffolded only. Nothing published." relied on a stale line in `SECURITY.md`. What was missing was verification by anyone, plus the gaps listed below.

## 1. What keman actually shipped

Evidence comes from `gh release view v0.1.0-beta -R kirank55/keman`, the downloaded `keman_linux_amd64.bundle`, and the GHCR registry API.

| Item | Finding |
| --- | --- |
| Release | `v0.1.0-beta`, marked prerelease, published 2026-10-29T10:16:30Z. It was made by `release` workflow run `40856113998`, which succeeded. |
| Assets | One binary per target (5 targets) with `.sha256`, `.spdx.json` and `.bundle` beside it, plus `checksums.txt` and `install.sh`. |
| Bundle format | The **legacy cosign bundle** (`base64Signature`, `cert`, `rekorBundle`), not the Sigstore protobuf bundle (`*.sigstore.json`). This is because `cosign-installer@v3` installed cosign 2.x. |
| Transparency log | Rekor `hashedrekord` entry, logIndex `3564697507`, integrated 2026-10-29T10:16:12Z. The digest matches `keman_linux_amd64.sha256` (`9311fe2f…`). |
| Signing identity (cert SAN) | `https://github.com/kirank55/keman/.github/workflows/release.yml@refs/tags/v0.1.0-beta`, issuer `https://token.actions.githubusercontent.com`. Fulcio extensions record commit `df388c1`, trigger `push`, runner `github-hosted`, and the run URL. |
| SBOM | SPDX-2.3 JSON, made by `syft-1.42.3`, listing 40 packages. |
| Image | `ghcr.io/kirank55/keman` has the tags `0.1.0-beta`, `beta`, `latest`, plus the cosign signature tag `sha256-432f02e8….sig` (the legacy tag-based scheme). The index also holds BuildKit `attestation-manifest` entries (buildx default provenance), which are unsigned **[unverified: Docker's page does not say]**. |
| Docs | `SECURITY.md:48` still says "No signed release has been published yet", and the launch checklist item for `cosign verify-blob` is unchecked (`docs/launch/README.md:38`). |

### Gaps in keman's pipeline that kman should not repeat

1. **Actions pinned by moving tag** (`actions/checkout@v4`, `sigstore/cosign-installer@v3`, `anchore/sbom-action@v0`, …). GitHub's guidance is to pin to a full commit SHA or a lockfile (section 5).
2. **Go `1.22.x` pinned in both the workflow and the Dockerfile.** Go supports a major release only "until there are two newer major releases" (https://go.dev/doc/devel/release). The supported versions today are 1.27.3 and 1.26.10 (https://go.dev/dl/?mode=json).
3. **Write permissions at the top level for every job** (`contents`, `id-token`, `packages`). Scorecard's Token-Permissions check wants read-only at the top level and writes granted per job.
4. **No provenance.** There are signatures but no SLSA provenance, so there's no record of how the binary was built.
5. **Each binary is signed, but `checksums.txt` is not.** This means 5 bundles, and nothing signs the file an installer would naturally check.
6. **The image rebuilds the binary inside Docker,** so the binary in the image is not the released, signed binary. GoReleaser's `dockers_v2` copies the binary it already built.
7. **The `latest` image tag goes on a prerelease** (`type=raw,value=latest` with no condition).
8. **`install.sh` weaknesses.**
   - It checks the signature only if `cosign` happens to be installed, and skips the check silently otherwise.
   - Its identity is a regexp, `release\.yml@`, that accepts any ref, not just `refs/tags/<version>`.
   - The `.sha256` comes from the same release as the binary, so it guards against corruption, not tampering.
9. **The Dockerfile's base images are tags** (`golang:1.22-bookworm`, `distroless/static-debian12:nonroot`), not digests. Its license label says Apache-2.0, which is right for keman but wrong for kman (MIT).

## 2. Signing and provenance

### GitHub artifact attestations

- The action is `actions/attest@v4`. "As of version 4, `actions/attest-build-provenance` is simply a wrapper on top of `actions/attest`", and "new implementations should use `actions/attest` instead" (https://github.com/actions/attest-build-provenance).
- With no predicate input it makes SLSA build provenance. With `sbom-path` it makes an SBOM attestation (SPDX or CycloneDX JSON, up to 16 MB). `subject-checksums` takes a shasum file, and it accepts up to 1024 subjects. `push-to-registry: true` stores the attestation alongside an OCI image (https://github.com/actions/attest).
- **Signing.** For public repos, "the public-good instance of Sigstore will be used." Private and internal repos use GitHub's private instance and need GitHub Enterprise Cloud. The feature is not available on GHES (https://github.com/actions/attest).
- **Permissions:** `id-token: write`, `contents: read`, `attestations: write`, plus `packages: write` for images (https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations/using-artifact-attestations-to-establish-provenance-for-builds).
- **SLSA level.** "Artifact attestations by themselves reach SLSA v1.0 Build Level 2." For L3 the build must "take place in a reusable workflow", which can "provide isolation between the build process and the calling workflow" (https://docs.github.com/en/actions/concepts/security/artifact-attestations).
  - Both the caller and the reusable workflow need the permissions above. Verifiers then use `--signer-workflow <owner>/<repo>/.github/workflows/<file>.yml` (https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/increase-security-rating).
  - **[unverified]** GitHub's wording assumes a reusable workflow shared across an organization's repos. For a solo repo the same maintainer controls both workflows, so whether an in-repo reusable workflow fully meets L3's isolation intent is a judgement call.
- **Limits.** GitHub says attestations do not prove an artifact is secure, and "provide no security benefit unless someone verifies them" (same concepts page).
- **SLSA spec status.** The v1.0 levels page is now marked "Retired", and v1.2 is current. v1.2 adds a Source track (https://slsa.dev/spec/v1.2/). **[unverified]** I did not confirm whether the Build-track L1–L3 wording changed in v1.2. The L1–L3 requirements below are quoted from v1.0 (https://slsa.dev/spec/v1.0/levels):
  - L1: "Provenance exists".
  - L2: "Build platform runs on dedicated infrastructure, not an individual's workstation", and the provenance is signed.
  - L3: runs are isolated, and signing material is not "accessible to the user-defined build steps".
- **slsa-github-generator is deprecated.** "This project is no longer actively maintained … we suggest GitHub artifact attestations." Its last release was v2.1.0 (Feb 2025) (https://github.com/slsa-framework/slsa-github-generator). Don't build on `builder_go_slsa3.yml`.

### Keyless cosign

- The latest release is cosign v3.1.4 (2026-10-20). `sigstore/cosign-installer` v4.1.2 defaults to v3.1.3 (from its `action.yml`).
- Cosign v3 turns these on by default: "the standardized bundle format", trusted-root and signing-config files, and "container signatures stored as an OCI Image 1.1 referring artifact". `--bundle` became required for `sign-blob` (cosign v3.0.1 release notes).
- v3.1.1 deprecates some verification-material flags, which are to be removed in v4.
- GoReleaser's migration post changes signing to `--bundle=${artifact}.sigstore.json` (https://goreleaser.com/blog/cosign-v3/).
- `verify-blob` takes either an exact `--certificate-identity` or `--certificate-identity-regexp`. It also takes GitHub-claim flags: `--certificate-github-workflow-repository`, `-ref`, `-sha`, `-trigger` and `-name`. It works offline with `--trusted-root` (cosign `doc/cosign_verify-blob.md`).

### Comparison for the operator

| | `cosign verify-blob` / `cosign verify` | `gh attestation verify` |
| --- | --- | --- |
| What it proves | A signature by the workflow identity in the cert. The cert's Fulcio extensions also carry repo, commit, ref, trigger and runner type (seen in keman's cert). | Signed SLSA provenance (in-toto statement) for the digest. Enforces predicate `https://slsa.dev/provenance/v1` by default. |
| Tool needed | `cosign` | `gh` (current v2.103.3; Ubuntu's apt `gh` 2.45 on this machine has no `attestation` command) |
| Where the evidence lives | A `.sigstore.json` attached to the release, or a referrer next to the image | The GitHub attestations API (or `--bundle`, or `--bundle-from-oci`) |
| Identity pinning | The operator must type the exact identity and issuer. Typos or loose regexps weaken it. | `-R owner/repo` is the minimum. `--signer-workflow`, `--source-ref`, `--cert-identity` and `--deny-self-hosted-runners` tighten it. |
| Offline | `--bundle` + `--trusted-root` | `gh attestation download` + `gh attestation trusted-root` + `--bundle --custom-trusted-root`. GitHub says to regenerate the trusted root when importing new material, since keys rotate "a few times a year" (https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/verify-attestations-offline). |
| Auth | None | The REST endpoint answered unauthenticated for a public repo (`GET /repos/cli/cli/attestations/sha256:4c80…` returned 200 with 2 attestations, tested). **[unverified]** whether the `gh` CLI itself requires `gh auth login`. |

(Flag list from https://cli.github.com/manual/gh_attestation_verify.)

**Release-level attestation.** With **immutable releases** enabled, GitHub locks assets and the tag after publishing. It also "automatically generates a release attestation" covering the tag, commit and assets (https://docs.github.com/en/code-security/supply-chain-security/understanding-your-software-supply-chain/immutable-releases).

- Operators check with `gh release verify <tag>` and `gh release verify-asset <tag> <file>` (https://docs.github.com/en/code-security/supply-chain-security/understanding-your-software-supply-chain/verifying-the-integrity-of-a-release).
- Assets must be uploaded to a draft before it is published.
- GoReleaser moved its own repo to immutable releases in April 2026 (https://goreleaser.com/blog/immutable-releases/).

## 3. SBOM

- **Both formats are formal standards.**
  - CycloneDX is ECMA-424; its 2nd edition (December 2025) "defines the CycloneDX v1.7" spec (https://ecma-international.org/publications-and-standards/standards/ecma-424/).
  - SPDX is ISO/IEC 5962:2021 **[secondary: search summary; that ISO edition corresponds to SPDX 2.2.1, also not confirmed at source]**.
- **Tool support is equal.** GitHub attestations accept either (`sbom-path` takes SPDX or CycloneDX JSON). Syft writes both. keman's SBOM was SPDX 2.3, and GitHub's own docs use `--predicate-type https://spdx.dev/Document/v2.3` as the example.
- **Choice:** stay on **SPDX 2.3 JSON** from Syft, for continuity and because it's GitHub's worked example. CycloneDX is an equally valid choice if VEX matters later. Don't ship both.
- **Attach *and* attest.**
  - Attaching makes the SBOM readable without tooling. Scorecard's SBOM check gives full points only for "an SBOM file matching established standards" published as a release artifact (https://github.com/ossf/scorecard/blob/main/docs/checks.md).
  - Name it `<artifact>.spdx.json`, the OpenSSF SBOM Everywhere naming convention (https://github.com/ossf/SBOM-everywhere/blob/main/reference/sbom_naming.md). Caddy's `.sbom` extension would not match it.
  - Attesting binds the SBOM to the artifact digest under the workflow identity: `gh attestation verify --predicate-type https://spdx.dev/Document/v2.3`.
  - Signing the checksum file covers the attached SBOMs too, because they are listed in it (GoReleaser's example notes this).
- **Scan archives, not just binaries.** Syft reads a Go binary's embedded build info. GoReleaser's `sboms: - artifacts: archive` covers each archive, and `artifacts: source` covers the source tarball.

## 4. Reproducible builds

What Go gives for free:

- **Go 1.21+ toolchains are "perfectly reproducible"** and are rebuilt nightly by `gorebuild`. For your own program, "Disabling `cgo` removes the host C toolchain as a relevant input, and `-trimpath` removes the current directory" (https://go.dev/blog/rebuild). keman used pure-Go SQLite with `CGO_ENABLED=0`, and if kman keeps that, the condition holds. ADR 0003's possible future "CGO build using the Security framework" would break this for macOS.
- **`-buildvcs=auto` (the default)** stamps `vcs.revision`, `vcs.time` and `vcs.modified` when the main module is in a repo (`go help build`).
- **Toolchain pinning.** The `go.mod` `toolchain` line is "a suggested toolchain". `GOTOOLCHAIN` can force one, and downloaded toolchains are checked against the checksum database (https://go.dev/doc/toolchain). Pin the exact patch version, e.g. `toolchain go1.27.3`, and use `actions/setup-go` with `go-version-file: go.mod`.

Experiment (Go 1.27.3, toy module, `CGO_ENABLED=0 -ldflags="-s -w -X main.Version=v1.0.0"`, the same git commit checked out at two paths):

| Flags | `/tmp/…/a` | `/tmp/…/b/deeper` |
| --- | --- | --- |
| none | `158c4851…` | `c29ceb1e…` (differs) |
| `-trimpath` | `7660ef9a…` | `7660ef9a…` (identical) |

- `go version -m` showed `vcs.modified=true` for the second build in each tree. The only change was the untracked `out.bin` left by the first build, which flipped the flag and changed the bytes.
- **Lesson:** build outputs must be git-ignored or written outside the tree. Otherwise a stray file makes the build irreproducible and lies about the source.

How projects demonstrate reproducibility:

- GoReleaser's recipe uses `-trimpath`, `mod_timestamp: "{{ .CommitTimestamp }}"`, `-X main.date={{ .CommitDate }}` instead of build time, and a pinned Go version (https://goreleaser.com/blog/reproducible-builds/; same settings in `goreleaser/example-supply-chain`).
- `gomod.proxy: true` builds from `proxy.golang.org`, "making the builds verifiable". But "VCS Info will not be embedded in the binary" (https://goreleaser.com/customization/builds/verifiable_builds/). **Recommendation:** leave `gomod.proxy` off and keep VCS stamping. Provenance already binds the commit.
- Checking it: a CI job rebuilds the tag from a fresh checkout and compares SHA-256 against the published `checksums.txt`. An operator does the same by hand: `git checkout vX && go build <same flags>`, using the toolchain from `go version -m`.
- Peers break this in small ways. step-ca stamps `-X main.BuildTime={{.Date}}` (wall-clock time), which makes every build unique.

## 5. Dependency and CI hygiene

- **Pin actions.**
  - "Pin references to actions to a commit SHA" (https://docs.github.com/en/actions/reference/security/secure-use).
  - GitHub now also offers **workflow dependency lockfiles**: `.github/workflows/actions.lock`, generated by the `gh actions-lock` extension. They record "the exact commit for every action your workflows depend on, including transitive dependencies", check repo identity (`owner_id`/`repo_id`) at run time, and satisfy the "Require actions to be pinned to a full-length commit SHA" policy. GitHub calls this "the strongest protection available, and supersedes SHA pinning" (https://docs.github.com/en/actions/concepts/security/workflow-dependency-lockfiles). It does not cover reusable-workflow calls (`jobs.<id>.uses`), which still need SHA pins.
  - **[unverified]** The page did not state preview/GA status or plan availability. Treat it as new: SHA-pin now, and adopt the lockfile once its status is clear.
- **Dependabot.**
  - Ecosystems `gomod`, `github-actions` and `docker`.
  - Dependabot updates SHA-pinned actions, but "will not create alerts for actions pinned to SHA values" (secure-use page). Use version updates, not just alerts.
  - A `cooldown` option delays updates by N days (https://docs.github.com/en/code-security/dependabot/working-with-dependabot/dependabot-options-reference). This is worth setting given recent action-compromise patterns.
  - Renovate is the usual alternative. I did not research it, because Dependabot covers kman's three ecosystems natively.
- **govulncheck.**
  - Source mode reports only vulnerabilities in code that is called.
  - Binary mode "uses the binary's symbol information". "For binaries where the symbol information cannot be extracted, govulncheck reports vulnerabilities for all modules" (https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck). **[unverified]** whether this fallback covers `-s -w` stripped binaries; it's inferred.
  - It exits non-zero on findings, except with `-json`, `-format sarif` or `-format openvex`.
  - Run it in source mode on every PR and on a schedule, and as a release gate before tagging.
- **OpenSSF Scorecard** has 20 checks (https://github.com/ossf/scorecard/blob/main/docs/checks.md). The ones relevant here:
  - **Signed-Releases** looks for `*.sig`, `*.sigstore.json`, `*.intoto.jsonl`, etc. among release assets. It gives 8 for signatures and 10 only if each release has `*.intoto.jsonl` provenance. Attestations stored only in GitHub's API are not counted. **Attaching the attestation bundle as `kman_<ver>.intoto.jsonl` earns the 10 and gives operators an offline copy.**
  - **Pinned-Dependencies** treats a tag as pinned if it is "backed by a published immutable release".
  - **Token-Permissions**, **Dangerous-Workflow**, **SBOM**, **Fuzzing** (Go native fuzzing counts) and **Vulnerabilities** (OSV).
  - Run `ossf/scorecard-action` on a schedule and publish the badge.
- **Runner hardening.** Caddy runs `step-security/harden-runner` (egress auditing) in its release job. It's optional, but worth considering for a secret-holding product.

## 6. GoReleaser vs a hand-written workflow

kman's outputs: 5-target static binaries, checksums, SBOMs, signatures, provenance, a multi-arch GHCR image, a Homebrew cask, `.deb`/`.rpm`, release notes. GoReleaser OSS (v2.19.0, 2026-10-22) covers all of them except provenance, which `actions/attest` adds.

| Need | GoReleaser | keman's hand-written workflow |
| --- | --- | --- |
| Builds, archives, checksums | `builds`, `archives`, `checksum` | matrix + `sha256sum` |
| SBOM | `sboms` (Syft) per archive and source | Syft per binary |
| Signing | `signs` (cosign `--bundle`, `artifacts: checksum`) | cosign per binary, legacy bundle |
| Image | `dockers_v2`: one `buildx` run builds and pushes the multi-platform manifest and SBOM, reusing the built binary. The old `dockers` + `docker_manifests` are deprecated. | Separate rebuild in Docker |
| Image signing | `docker_signs` | `cosign sign` |
| Homebrew | `homebrew_casks`. `brews` (formulas) is deprecated "since v2.10 (soft) / since v2.16". | none |
| deb/rpm | `nfpms` (optionally GPG-signed via `signature.key_file`) | none |
| Provenance | `actions/attest@v4` on `dist/checksums.txt` and `dist/digests.txt` (https://goreleaser.com/customization/publish/attestations/) | none |

Sources: https://goreleaser.com/resources/deprecations/ (`dockers`, `brews`), and https://github.com/goreleaser/example-supply-chain. The example's `release.yml` SHA-pins every action, installs cosign and Syft, runs `goreleaser release --clean`, then runs `attest-build-provenance` twice.

**Recommendation:** use GoReleaser and pin `goreleaser-action` and the GoReleaser version.

- A hand-written workflow got 3 of the 8 outputs into keman, with the 9 gaps in section 1.
- What GoReleaser costs: one config file to learn, and builds happen in one job rather than a matrix (fine: pure-Go cross-compiles in seconds).
- Images are built in the publish phase, so `goreleaser build`/`--skip=publish` won't build them (deprecations page). A pre-release CI dry run should use `goreleaser release --snapshot --clean`.

## 7. Distribution channels and how operators verify each

Throughout, the identity for kman is `https://github.com/kirank55/kman/.github/workflows/release.yml@refs/tags/<tag>`, and the issuer is `https://token.actions.githubusercontent.com`.

| Channel | What ships | Operator verification |
| --- | --- | --- |
| **GitHub Releases** | Archives, `checksums.txt`, `checksums.txt.sigstore.json`, `*.spdx.json`, `*.intoto.jsonl`. Immutable release. | `gh attestation verify kman_<v>_linux_amd64.tar.gz -R kirank55/kman --signer-workflow kirank55/kman/.github/workflows/release.yml --source-ref refs/tags/<v>`. Or `gh release verify-asset <v> <file>`. Or, without gh: `cosign verify-blob checksums.txt --bundle checksums.txt.sigstore.json --certificate-identity <identity> --certificate-oidc-issuer <issuer>`, then `sha256sum -c checksums.txt --ignore-missing`. |
| **GHCR image** | Multi-arch index built from the released binaries, base image pinned by digest, signed by digest with cosign v3 (OCI 1.1 referrer). Provenance attested with `push-to-registry: true`. Never tag a prerelease `latest`. | `cosign verify ghcr.io/kirank55/kman@sha256:… --certificate-identity <identity> --certificate-oidc-issuer <issuer>`, or `gh attestation verify oci://ghcr.io/kirank55/kman:<v> -R kirank55/kman`. Deploy by digest. **[unverified]** GHCR's support for the OCI 1.1 referrers API was not confirmed. `actions/attest` documents `push-to-registry` generically. |
| **Homebrew tap** | `homebrew_casks` in `kirank55/homebrew-tap`, holding the URL and sha256 of the release archive. | Brew checks the cask's sha256 when downloading. Trust rests on the tap repo plus the release. `HOMEBREW_VERIFY_ATTESTATIONS` verifies provenance only "for bottles from `homebrew/core` or supported third-party taps" (Homebrew `docs/Manpage.md`), not for a cask like this. **macOS:** GoReleaser says "Casks are supposed to be signed, even if they are coming from a tap" and warns that the `xattr` quarantine-removal hook "circumvents macOS security protections" (https://goreleaser.com/customization/publish/homebrew_casks/). An unsigned, unnotarized binary is a known friction point; decide on Apple notarization separately. |
| **`install.sh`** | A script served from the repo; the binary comes from the release. | Always check SHA-256 against `checksums.txt`, then check `checksums.txt`'s signature with whichever verifier is present (`gh attestation verify` or `cosign verify-blob`). Use the **exact** identity for `refs/tags/$version`, not a regexp. Say loudly when no verifier is present, and offer `KMAN_REQUIRE_SIGNATURE=1` to fail closed. Prefer `/releases/latest` once stable releases exist. The honest limit: `curl … \| sh` trusts GitHub's TLS and the repo for the script itself. A checksum from the same release only proves integrity. |
| **deb/rpm (nfpm)** | Attach `.deb`/`.rpm` to the release. They are covered by the signed checksums and the attestation. | Same as GitHub Releases. Note that apt does not verify signatures on individual `.deb` files: it checks the signed `Release` file and then "can also check its checksum against the content of the Packages file". Embedded package signatures need `debsig-verify` (https://wiki.debian.org/SecureApt). A real apt/yum repo needs a long-lived GPG key, which is the one secret keyless signing avoids. **Defer hosted repos.** |

## 8. Peers

| Project | Where binaries ship | Signing / provenance | Notes |
| --- | --- | --- | --- |
| **Caddy** v2.11.7 (2026-10-03) | GitHub Releases | GoReleaser + keyless cosign on every asset. v2.11.7 has legacy `.sig` + `.pem`, and current `main` `.goreleaser.yml` uses `--bundle=${artifact}.sigstore.json`, `artifacts: all`. Per-platform SBOM (`.sbom`), `.deb` via nfpm, source tarball, `-trimpath -mod=readonly`. | Release workflow SHA-pins actions and uses `step-security/harden-runner`. No `actions/attest` in the workflow (grep). |
| **step-ca** v0.30.2 (2026-03-23) | GitHub Releases + Google Artifact Registry packages | GoReleaser via the shared reusable workflow `smallstep/workflows/.github/workflows/goreleaser.yml@main`. cosign `*.sigstore.json` on every asset (including `checksums.txt`). nfpm deb/rpm signed with a GPG key held in secrets. | The reusable workflow is called by `@main`, not by SHA. `BuildTime={{.Date}}` defeats reproducibility. |
| **Vault** v2.2.1 | releases.hashicorp.com (no GitHub assets) | "only the SHA256SUM file is signed by the PGP key". Authenticode on Windows, Apple code signing on macOS, GPG-signed apt/yum repos. No Sigstore mentioned (https://www.hashicorp.com/en/trust/security). | Classic model: `gpg --verify` then `shasum -c`. |
| **Teleport** v18.10.0 | cdn.teleport.dev (no GitHub assets) | `.sha256` per download. Apple/Authenticode signing, GPG-signed repos. Images signed with **key-based** cosign (`cosign verify --key teleport-oci-key.pub`) (https://goteleport.com/docs/reference/public-certificates/). | "The Teleport package in Homebrew is not maintained or signed by Teleport." The Teleport downloads/checksum lines are **[secondary]** (search summary), not fetched. |
| **Tailscale** v1.104.5 | pkgs.tailscale.com (no GitHub assets) | Tarballs have `.sha256` and `.sig`, signed by its own Ed25519 `distsign` scheme: offline root keys baked into the client sign rotating signing keys (`clientupdate/distsign/distsign.go`). | `install.sh` installs a GPG-signed apt/yum repo, fetching the keyring from the same origin. |
| **GitHub CLI** v2.103.3 | GitHub Releases | Publishes artifact attestations. The public API returned 2 attestations for the linux_amd64 tarball. | Shows the attestations-on-GitHub pattern in production. |

**Pattern:** older vendors with their own CDNs (HashiCorp, Teleport, Tailscale) use long-lived GPG/Ed25519 keys and signed package repos. GitHub-native Go projects (Caddy, step-ca, GoReleaser) use keyless cosign bundles, and the newest add GitHub attestations. Neither Caddy nor step-ca attaches a provenance file (`*.intoto.jsonl`) to its latest release (checked asset lists), and the other three don't use GitHub Releases for binaries.

## 9. Recommended baseline for kman v1

1. **GoReleaser OSS**, pinned, one `release.yml` on `v*` tags.
   - Five targets, `CGO_ENABLED=0 -trimpath`, `mod_timestamp`/`CommitDate` from the commit, `dist/` git-ignored.
   - `toolchain go1.27.x` pinned in `go.mod`.
2. **Permissions:** `contents: read` at the top level. Grant `contents/id-token/attestations/packages: write` only to the release job.
3. **Signing:** cosign v3 `sign-blob --bundle checksums.txt.sigstore.json` on `checksums.txt` (one signature covers every asset, SBOMs included).
4. **Provenance:** `actions/attest@v4` with `subject-checksums: dist/checksums.txt` and `dist/digests.txt`.
   - Also attach the provenance bundle as `kman_<v>.intoto.jsonl`.
   - SLSA Build L2 now. L3 later by moving the build into a reusable workflow and documenting `--signer-workflow`.
5. **SBOM:** Syft SPDX 2.3 JSON per archive, named `*.spdx.json`. Attach it, and attest it with `sbom-path`.
6. **Image:** `dockers_v2` reusing the built binary; distroless static base pinned by digest; MIT label.
   - cosign-signed by digest, with provenance attested and pushed to the registry.
   - `latest` only for non-prerelease tags.
7. **GitHub settings:**
   - Immutable releases on (draft, upload, publish).
   - The "require SHA-pinned actions" policy on.
   - Tag protection for `v*`.
8. **Hygiene:**
   - Every action SHA-pinned (adopt `actions.lock` once its status is confirmed).
   - Dependabot (`gomod`, `github-actions`, `docker`) with cooldown.
   - `govulncheck` on PRs, daily, and as a release gate.
   - Scorecard action on a schedule.
   - A reproducibility job that rebuilds the tag and diffs it against `checksums.txt`.
9. **Channels for v1:** GitHub Releases, GHCR, `install.sh` (verifying as above), a Homebrew cask, and `.deb`/`.rpm` attached to releases. Defer a hosted apt/yum repo and Apple notarization.
10. **Docs:** a `SECURITY.md` "Verify a release" section with the exact `gh attestation verify` and `cosign verify-blob` commands for binaries and the image. Add a release-checklist step where someone other than the workflow actually runs them.
