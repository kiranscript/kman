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
