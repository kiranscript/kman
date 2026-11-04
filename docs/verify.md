# Verify a kman release

Every kman release is built by one GitHub Actions workflow, `release.yml`, from a pushed tag. Each release proves where it came from in three ways:

- **A cosign signature on `checksums.txt`**, made keylessly with Sigstore. `checksums.txt` lists the SHA-256 of every archive, package and SBOM, so one signature covers them all.
- **GitHub artifact attestations** (SLSA Build L2 provenance) for every archive, the source archive and the image, plus an SPDX 2.3 SBOM attestation for each archive. An offline copy of every attestation is attached as `kman_<version>.intoto.jsonl`.
- **An immutable GitHub release**, which GitHub attests itself. Its assets and tag cannot change after publishing.

Whichever you check, check it against this exact identity, with the tag you are installing:

- Identity: `https://github.com/kirank55/kman/.github/workflows/release.yml@refs/tags/<tag>`
- Issuer: `https://token.actions.githubusercontent.com`

A different identity, or a pattern in place of the tag, accepts a signature from some other workflow or ref. `install.sh` does the first two checks for you (see the end of this page).

You need [cosign](https://docs.sigstore.dev/cosign/system_config/installation/) v3 or [gh](https://cli.github.com/) 2.49 or later (for `gh attestation`). Set the release you are checking:

```sh
TAG=v0.1.0   # the release you are checking
V=${TAG#v}   # archive names use the version without the v
```

The commands below are the ones the post-release job in `release.yml` runs against every release, through `scripts/verify-release.sh`. Change them only together with what they check.

<!-- verify:begin -->

### Download

Download one archive, the checksums, the signature and the attestation bundle:

```sh
gh release download "$TAG" -R kirank55/kman \
  -p "kman_${V}_linux_amd64.tar.gz" \
  -p checksums.txt -p checksums.txt.sigstore.json -p "kman_${V}.intoto.jsonl"
```

Without gh, download the same files from `https://github.com/kirank55/kman/releases/download/<tag>/<file>`.

### The signature, with cosign

Verify `checksums.txt`, then check the archive against it:

```sh
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity "https://github.com/kirank55/kman/.github/workflows/release.yml@refs/tags/$TAG" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum --ignore-missing -c checksums.txt
```

On macOS, use `shasum -a 256 --ignore-missing -c checksums.txt`.

### The provenance, with gh

Verify the archive's build provenance, online and then from the attached bundle:

```sh
gh attestation verify "kman_${V}_linux_amd64.tar.gz" -R kirank55/kman \
  --cert-identity "https://github.com/kirank55/kman/.github/workflows/release.yml@refs/tags/$TAG" \
  --deny-self-hosted-runners
gh attestation verify "kman_${V}_linux_amd64.tar.gz" -R kirank55/kman \
  --bundle "kman_${V}.intoto.jsonl" \
  --cert-identity "https://github.com/kirank55/kman/.github/workflows/release.yml@refs/tags/$TAG" \
  --deny-self-hosted-runners
```

### The SBOM

The archive's SPDX 2.3 SBOM is attached as `kman_<version>_<os>_<arch>.tar.gz.spdx.json` and attested:

```sh
gh attestation verify "kman_${V}_linux_amd64.tar.gz" -R kirank55/kman \
  --predicate-type https://spdx.dev/Document/v2.3 \
  --cert-identity "https://github.com/kirank55/kman/.github/workflows/release.yml@refs/tags/$TAG"
```

### The release itself

GitHub's own attestation of the immutable release, its tag and the asset:

```sh
gh release verify "$TAG" -R kirank55/kman
gh release verify-asset "$TAG" "kman_${V}_linux_amd64.tar.gz" -R kirank55/kman
```

### The image

The image `ghcr.io/kirank55/kman` is signed by digest and has its provenance stored in the registry. Verify it, then deploy it by digest:

```sh
cosign verify "ghcr.io/kirank55/kman:$V" \
  --certificate-identity "https://github.com/kirank55/kman/.github/workflows/release.yml@refs/tags/$TAG" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
gh attestation verify "oci://ghcr.io/kirank55/kman:$V" -R kirank55/kman \
  --cert-identity "https://github.com/kirank55/kman/.github/workflows/release.yml@refs/tags/$TAG"
```

<!-- verify:end -->

`latest` points at the newest stable release only. A release candidate (`-rc.N`) never gets it.

## Rebuild it yourself

Releases are reproducible: `CGO_ENABLED=0 -trimpath`, the Go toolchain pinned in `go.mod`, and every timestamp taken from the commit. The post-release job rebuilds each tag and compares the archives' checksums with the published ones. To do the same:

```sh
git clone https://github.com/kirank55/kman && cd kman && git checkout "$TAG"
goreleaser release --clean --skip=publish,sign,sbom,docker,announce,validate
gh release download "$TAG" -R kirank55/kman -p checksums.txt -O published-checksums.txt
sh scripts/compare-checksums.sh dist/checksums.txt published-checksums.txt
```

Use the GoReleaser version pinned in `.github/workflows/release.yml`. SBOMs are not compared: Syft stamps each with a time and a random namespace.

## What install.sh checks

`install.sh` always checks the archive's SHA-256 against `checksums.txt`. Then:

- With cosign installed, it runs the `cosign verify-blob` check above against the exact tag identity.
- Otherwise, with gh 2.49 or later, it runs `gh attestation verify` on the archive with the attached `kman_<version>.intoto.jsonl`, which needs no `gh auth login`.
- With neither, it installs with a loud warning. Set `KMAN_REQUIRE_SIGNATURE=1` to make it refuse instead.

A failed check stops the install. It never falls back to the next verifier.

The checksum alone proves only that the download is intact, since it comes from the same release as the archive. And `curl … | sh` trusts GitHub and its TLS for the script itself. To avoid that, download `install.sh` from the tag, read it, and run it with `KMAN_VERSION` set.
