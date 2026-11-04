# Changelog

All notable changes to kman are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and kman uses
[semantic versioning](https://semver.org/). A release's section, headed
`## [<version>]`, becomes its GitHub release notes.

## [Unreleased]

### Added

- `kman init` sets up an install without asking anything: `kman.yaml` and a `0600` master key in the config directory, the data directory, and the `owner` admin token, printed once and saved to a `0600` credentials file. `--data-dir`, `--key-source file|systemd-creds|passphrase|env` and `--no-save-token`. In a container it needs a key from outside and never saves the token.
- `kman serve` binds the proxy to `127.0.0.1:8080` and the admin API to `127.0.0.1:8081` (or a `0600` Unix socket), with optional TLS per listener. It refuses a master key inside the data directory, an empty data directory, and plain HTTP on an admin address that is not loopback, and it shuts down cleanly on SIGINT and SIGTERM.
