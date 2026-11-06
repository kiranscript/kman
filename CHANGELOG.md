# Changelog

All notable changes to kman are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and kman uses
[semantic versioning](https://semver.org/). A release's section, headed
`## [<version>]`, becomes its GitHub release notes.

## [Unreleased]

### Added

- `kman init` sets up an install without asking anything: `kman.yaml` and a `0600` master key in the config directory, the data directory, and the `owner` admin token, printed once and saved to a `0600` credentials file. `--data-dir`, `--key-source file|systemd-creds|passphrase|env` and `--no-save-token`. In a container it needs a key from outside and never saves the token.
- `kman serve` binds the proxy to `127.0.0.1:8080` and the admin API to `127.0.0.1:8081` (or a `0600` Unix socket), with optional TLS per listener. It refuses a master key inside the data directory, an empty data directory, and plain HTTP on an admin address that is not loopback, and it shuts down cleanly on SIGINT and SIGTERM.
- The admin API, `/admin/v1`: the one way to change a running kman. Every change is one store transaction with an audit entry naming its actor, the admin token's name. Requests authenticate with an admin token as a bearer token, never a cookie. Its OpenAPI 3.1 document is generated from the Go types, served at `/admin/v1/openapi.json`, and committed as `docs/admin-api/openapi.json`.
- Named admin tokens: `kman admin-token create|list|rotate|revoke`. A revoked token is refused from its next request on, and the last live token cannot be revoked. Rotating the CLI's own token saves the new secret to the credentials file.
- `kman settings get|set` for `max_key_lifetime` (365d), `default_key_ttl` (90d), `spend_timezone` (UTC) and `soft_budget_defaults` (50,80,100), each change audited.
- `kman audit` lists the newest audit entries with their actor; `kman audit export` writes every entry as JSON Lines.
- `kman login --admin-url <url>` checks a token with the server and saves both to the credentials file; `kman logout` removes it. `KMAN_ADMIN_TOKEN` and `KMAN_ADMIN_URL` override the file.
- Every read command takes `--json`. Exit codes are 0 OK, 1 error (usage errors included), 3 findings.
- `kman init --proxy-listen`, `--admin-listen` and `--allow-plain-http`, each also read from `KMAN_PROXY_LISTEN`, `KMAN_ADMIN_LISTEN` and `KMAN_ALLOW_PLAIN_HTTP`. init now refuses, before writing anything, a config that serve would refuse.
- `kman upstream add|list|show|update|rm|sync`: an upstream comes from one of 21 built-in presets (`kman upstream add openai --preset openai`) or is built with flags, with header or Basic auth. A preset is copied once, recording its version; an upgrade never changes an existing upstream, and `kman upstream sync` shows the difference from a newer preset and applies it after a confirmation.
- `kman credential add|update|rm`: credentials are sealed under the data key, never shown after entry, and audited. A secret is asked for without echo, piped, or read from the variable `--credential-env` names, and is never an argument. `upstream add` takes the first credential in the same step.
- Deleting an upstream or a credential is refused while a virtual key that is not revoked depends on it. Names are unique among items not deleted.
- `kman serve` holds the upstreams and credentials in a lock-free snapshot, rebuilt at startup and updated by every admin command after it commits, and refuses to start with a credential it cannot decrypt.
- Best-effort packages: a `.deb` and an `.rpm` for amd64 and arm64 with a sandboxed `kman.service` (`DynamicUser=yes`, `StateDirectory=kman`, `ConfigurationDirectory=kman`, the master key through `LoadCredentialEncrypted=`), and a Homebrew formula in `kirank55/homebrew-tap` for stable releases (`brew install kirank55/tap/kman`). See `docs/operator-guide/systemd.md`.
- A Compose example, `examples/compose/compose.yaml`: the key as a secret, the data on a volume, a read-only root filesystem, and both listeners published on host loopback only. See `docs/operator-guide/docker.md`.
- `kman price list` shows the built-in price table and how old it is, and plain `kman` shows the age too. Past 60 days both say `price data is N days old, upgrade kman`. Age never changes what kman enforces.
- The price table: generated from LiteLLM, cross-checked against genai-prices v2 and models.dev, keeping the conservative value, and embedded in the binary. It models long-context tiers, prices by modality, per-minute voice prices, per-call server-tool fees and service tiers, with DeepSeek at its peak price. Model ids match exactly. kman never fetches price data at runtime. See `docs/price-data.md`.
