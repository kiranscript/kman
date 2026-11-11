# Install kman

kman is one static binary, `kman`, with no runtime to install. Pick the channel that fits the machine. Every release is built by one GitHub Actions workflow from a pushed tag, and you can check where it came from: [Verify a kman release](../verify.md).

| Channel | For | Support |
|---|---|---|
| [`install.sh`](#linux-and-macos-installsh) | Linux and macOS | First-class |
| [Release archive](#a-release-archive-by-hand) | Linux, macOS and Windows | First-class |
| [Docker image and Compose](docker.md) | A server | First-class |
| [Homebrew](#homebrew) | macOS and Linux with brew | Best effort |
| [`.deb` and `.rpm`](systemd.md) | A Linux server run by systemd | Best effort |
| [`go install`](#go-install) | Developers | Best effort, unsigned |

Releases cover Linux and macOS on amd64 and arm64, and Windows on amd64. The image is for linux/amd64 and linux/arm64.

## Linux and macOS: install.sh

```sh
curl -fsSL https://raw.githubusercontent.com/kirank55/kman/main/install.sh | sh
```

It finds the latest stable release, downloads the archive for your OS and CPU, and **checks its SHA-256 against the release's `checksums.txt`**. Then it verifies the release's signature, against the exact identity of the release workflow at that tag:

- with `cosign` installed, it verifies the signature on `checksums.txt`;
- otherwise, with `gh` 2.49 or later, it verifies the archive's build attestation, which needs no `gh auth login`;
- with neither, it **installs anyway, after a loud warning** that only the checksum was checked, and the checksum comes from the same release as the archive. Install `cosign` or `gh` and run it again, or set `KMAN_REQUIRE_SIGNATURE=1` to make it refuse.

A failed check stops the install; it never falls through to the next verifier.

`kman` goes to `/usr/local/bin` if you can write there, otherwise to `~/.local/bin`, and the script tells you if that directory is not on your `PATH`. These variables change what it does:

| Variable | Meaning |
|---|---|
| `KMAN_VERSION` | Install this tag (such as `v0.1.0`) instead of the latest stable release. |
| `KMAN_INSTALL_DIR` | Install here. |
| `KMAN_REQUIRE_SIGNATURE=1` | Refuse to install unless a signature verified. |

`curl … | sh` trusts GitHub and its TLS for the script itself. To avoid that, download `install.sh` from the tag you want, read it, and run it: [Verify a kman release](../verify.md#what-installsh-checks) says how.

## A release archive, by hand

Download the archive for your machine from the [releases page](https://github.com/kirank55/kman/releases), together with `checksums.txt`, and [verify them](../verify.md): `kman_<version>_<os>_<arch>.tar.gz` for Linux and macOS, and `kman_<version>_windows_amd64.zip` for Windows. Each holds the `kman` binary (`kman.exe` on Windows), the changelog and the README. Put the binary somewhere on your `PATH`.

### macOS: the quarantine flag

kman is **not code-signed or notarized by Apple**. `install.sh` and Homebrew don't hit this, because `curl` and Homebrew don't mark what they download. A file you download **in a browser** is marked with the `com.apple.quarantine` attribute, and Gatekeeper then refuses to open it ("kman cannot be opened because the developer cannot be verified"). After you have verified the archive, remove the flag from the binary:

```sh
tar -xzf kman_0.1.0_darwin_arm64.tar.gz
xattr -d com.apple.quarantine kman
./kman version
```

If `xattr` says the attribute is not there, the file is not quarantined, and the problem is something else.

### Windows: the SmartScreen warning

kman is **not code-signed by Microsoft**, so Windows treats a downloaded `kman.exe` as an unknown publisher. After you have verified the zip, the cleanest way through is to unblock it before you unpack it, in PowerShell:

```powershell
Unblock-File .\kman_0.1.0_windows_amd64.zip
Expand-Archive .\kman_0.1.0_windows_amd64.zip -DestinationPath "$env:LOCALAPPDATA\Programs\kman"
```

If you unpack it first, the first run shows **"Windows protected your PC"**: choose **More info**, then **Run anyway**. (Or right-click the zip or `kman.exe`, choose **Properties**, and tick **Unblock**.) Add the folder to your `PATH`:

```powershell
[Environment]::SetEnvironmentVariable("Path", "$env:Path;$env:LOCALAPPDATA\Programs\kman", "User")
```

then open a new terminal. Everything in the [quickstart](../quickstart.md) after the install line works in PowerShell. kman's files are under `%APPDATA%\kman` (config) and `%LOCALAPPDATA%\kman` (data), and `kman serve` runs in the foreground, so keep that window open or run it as a service of your own choosing. kman has no Windows service of its own.

The `0600` and `0700` file modes kman sets are a Unix idea. On Windows kman creates its files in your profile, which only your account and administrators can normally read, but it does not set access lists itself. **The v0.1.0 security review ran on Linux** and did not cover Windows or macOS behaviour (file modes, the key sources); see [Threat model](../../THREAT-MODEL.md).

## Homebrew

```sh
brew install kirank55/tap/kman
```

This installs the release archive for your machine from a formula pinned to the SHA-256 in the signed `checksums.txt`. It is a formula, not a cask, so macOS does not quarantine the binary. Only stable releases reach the tap, not release candidates.

## Docker, and packages for systemd

See [Docker and Compose](docker.md) and [systemd](systemd.md).

## go install

```sh
go install github.com/kirank55/kman/cmd/kman@latest
```

This builds from source with your own Go toolchain (the version `go.mod` names or newer). The result is **unsigned and carries no version stamp**: `kman version` shows the module's pseudo-version and commit. Use a release if you want to verify what you run.

## Where kman keeps its files

| | Linux and macOS | Windows |
|---|---|---|
| Config directory: `kman.yaml`, `master.key`, `credentials.json` | `~/.config/kman` (or under `$XDG_CONFIG_HOME`) | `%APPDATA%\kman` |
| Data directory: `kman.db` | `~/.local/share/kman` (or under `$XDG_DATA_HOME`) | `%LOCALAPPDATA%\kman` |

`kman init --config-dir … --data-dir …` puts them elsewhere, and the other commands take `--config-dir`. The master key is never kept in the data directory, and kman refuses to start if it is: see [Key sources](key-sources.md).

## After the install

Follow the [quickstart](../quickstart.md) from `kman init`. To keep kman running, use [systemd](systemd.md) or [Docker](docker.md); to put TLS in front of it, see [TLS and reverse proxies](tls.md).
