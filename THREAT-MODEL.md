# Threat model

What kman v0.1.0 is built to defend, against whom, how, and what it leaves to you. It draws on [the v0.1.0 security review](docs/security-review-v0.1.0.md), which read every module on the request path and the admin surface against the [invariants](SPEC.md#1-invariants), ran two end-to-end sessions, and tried the bypasses a reviewer would. Finding ids (S-1, M-3, L-4, …) are that review's. Report a vulnerability as [SECURITY.md](SECURITY.md) says.

## The one-line version

kman limits the damage of a **leaked virtual key** and keeps **real credentials** from reaching apps. It does not defend a machine that is already compromised, and it is not yet a detector: v0.1.0 contains a leak, it does not notice one.

## What kman protects

| Asset | Why it matters |
|---|---|
| **Upstream credentials** (the real OpenAI, Anthropic, … keys) | Whoever holds one can spend your money and read your data at the provider. |
| **The master key**, and the data key it wraps | It decrypts every credential in the data directory. |
| **Admin tokens** (`kma_…`) | An admin token changes anything: keys, credentials, caps. |
| **Virtual-key secrets** (`kmn_…`) | Each is a bearer token for what its policy allows. Only a hash is stored. |
| **Spend** | The money a key can cost you before it is stopped. |
| **The proxy's availability** | One abusive client should not take every app down. |

## Who we are defending against

| Actor | Has | The question |
|---|---|---|
| **A holder of a leaked virtual key** | The key; network access to the proxy | How much can they do, and how fast can you stop them? |
| **A buggy or compromised app** | Its own virtual key and whatever it can send | Can it exceed its policy, read a credential, or reach another app's key? |
| **A client with no key** | Network access to the proxy | Can it guess a key, or hold the proxy's connections and memory? |
| **A network observer** | Traffic between app and kman, or kman and the provider | Can it read a key? (Not without TLS: see *Assumptions*.) |
| **Someone with the data directory, a backup, or a copied volume** | `kman.db` and its copies | Can they read credentials? |
| **A local user of the same machine** | A shell, not root | Can they read the master key, the admin token, the database? |
| **Someone who tampers with a release, a dependency or the price data** | The supply chain | Can they ship code or numbers that weaken the above? |

**Not defended against:** an attacker with root on the host, or who can read kman's memory (a credential must be in memory to be sent upstream, and some copies in header strings cannot be wiped: L-4); an operator who is the attacker; a provider that is itself compromised.

## Invariants, and how each is held

The review checked each of SPEC §1's eight invariants against the code and by running it.

| # | Invariant | In practice | Evidence |
|---|---|---|---|
| 1 | The master key is never in the data directory | `kman serve` refuses to start otherwise, symlinks included; `init` refuses a data directory holding the key. A backup or copied volume carries ciphertext only. | End-to-end tests for the direct, symlinked and environment-supplied cases; modes checked by hand under umask 022. |
| 2 | Every change goes through the admin service, in one transaction, audited with its actor | The audit row commits inside the command's transaction, so a crash cannot leave an unaudited change. Exceptions are bookkeeping only (L-5, L-6). | Every command is tested for an audit entry with its actor. |
| 3 | A credential is never shown to an app, an operator, a log, an event or a metric; virtual-key secrets and admin tokens are shown once | A planted-secrets session searched every output, response, file and log for each secret and its fragments, and found nothing outside the places they belong. The one deliberate exception is a credential's last four characters (L-1). | `e2e/secrets_test.go`; the caveats are M-1 and L-1. |
| 4 | A capped or allowlisted key never forwards an unmetered request | Probes with duplicate and re-cased `model`, escaped keys, non-string models, compressed and non-JSON bodies, and bodiless GETs were each refused as `unmetered_request` or read to the same model the upstream will read. | Regression tests in the gateway, the meter and end to end. Residual risk: M-2, M-3. |
| 5 | The request path takes no locks and reads an immutable snapshot | True of the registry's lookup. The limiter, the meter's day rollover and some pools take small locks or atomics (L-6). | Lookup tests for no locks, no allocations and whole snapshots only. |
| 6 | kman makes no network call the operator did not configure or ask for | A recorded session of a bare install sent **0 packets**; with one OpenAI upstream and one request it sent 4 DNS packets for the upstream's name and nothing else. | `scripts/record-network.sh` (Linux; run before each release). |
| 7 | Price data changes only with a release or an audited override | The table is embedded in the binary; overrides are admin commands with audit entries. The release-side risk is M-7. | Tests that pricing cannot reach the network. |
| 8 | No listener is public by default; plain HTTP on a non-loopback admin address is refused | Both listeners bind `127.0.0.1`; an admin Unix socket is `0600`. The proxy listener, though, can be bound off loopback in plain HTTP with no warning (L-3). | Listener tests. |

## Threats, one by one

### A virtual key leaks

**What kman does.** The key can only do what its policy allows: its cap (reserved before the request, so it overshoots by at most one request), its models, its paths, its rate limit, and until its expiry, at most the maximum lifetime (365 days by default) from creation. You stop it with `kman key disable` or `kman key revoke`, and a disabled, revoked or expired key was refused at its next request by every route the review tried: every token form (`Authorization` as Bearer, token, bare or Basic, `x-api-key`, any header carrying the prefix) goes through one authentication that checks status after the secret. The registry swaps whole snapshots atomically, and an older generation never replaces a newer one. The real credential is not in the key, so a leak never exposes it.

**What is left.**

- **No detection.** Nothing in v0.1.0 notices a leak; you find out from the spend ([what to do meanwhile](docs/v0.1.0-scope.md)).
- **In-flight requests are not cut (M-4).** A long stream outlives a revoke; only the *next* request is refused. Realtime sessions are not in this release.
- **The overshoot is bounded, not zero (M-2).** The reservation's input estimate is bytes ÷ 4. Densely tokenizing text (rare scripts, emoji, code) can cost up to about three times that, so a key can overshoot by up to roughly three times the input side of what is in flight; output is exact. This is a small multiple of the cap, not unbounded.
- **A generic upstream is unmetered by design,** and a capped key on one is refused rather than let through.
- **Price overrides weaken the reservation (M-3).** An override replaces the whole built-in entry, losing its tiers, long-context bands and per-call tool fees, so a request for a priority tier or a very long prompt can be reserved and charged at the base rate. The audit shows who set it.

### A client without a key, or a stolen key used to exhaust kman

**What kman does.** A key's checksum is verified before any lookup, secrets are compared in constant time, and unknown, wrong-secret, canary and credential-less keys all answer the same `invalid_key`. An address that presents more than 30 invalid keys a minute is refused (a /64 counts as one address for IPv6), so guessing is slow, in bounded memory. Request bodies are limited: 32 MiB each; a body that stops arriving for 30 s, or takes over 5 minutes, ends its connection; 512 MiB held across all requests, past which requests get `rate_limited`; idle connections close after 2 minutes; a request head is limited to 64 KiB.

**S-1 (high, fixed in this release).** Before the fix, 20 connections each sending 31 MiB of a stated 32 MiB body and stopping took kman from 19 MB to 898 MB resident and held it, with one valid key, and a client with **no** key could hold a connection indefinitely by promising a body it never sent. The limits above are the fix, with tests.

**What is left.** The limits are constants, with no `kman.yaml` setting. The invalid-key throttle also refuses *good* keys from a throttled address for up to a minute, which is what stops fast guessing and also what lets an abuser lock a shared address (behind a reverse proxy, set `trusted_proxies` so addresses are real).

### A buggy or hostile app

**What kman does.** Strips the app's inbound auth (`Authorization`, `Api-Key`, `X-Api-Key`, `X-Goog-Api-Key`, and any header whose value starts with a key), hop-by-hop headers, and forwarding headers (`Forwarded`, `Via`, `X-Forwarded-*`, `X-Real-Ip`), and never forwards the app's `Host`. The upstream's address comes only from the upstream's row, so an app cannot choose where its request goes. Paths are matched after refusing `.`, `..`, empty and encoded-slash segments. An upstream redirect is never followed: one within the upstream is rewritten to a path, anything else is a 502 with its target withheld, so the app never carries its key off kman. A mid-stream failure cuts the app's connection, so half an answer cannot read as whole. An app's `include_usage: false` is overridden, and requests to OpenAI and Anthropic upstreams go out with `Accept-Encoding: identity`, so an app cannot opt out of metering.

**What is left.**

- **M-1: an upstream's error can show a masked credential to an app.** Providers answer a rejected key with text such as "Incorrect API key provided: sk-proj-…abcd". If a credential is revoked or wrong, the app reads those characters. kman does not read response bodies, and the masking is the provider's. A generic upstream that echoes request headers would show the whole credential: that is the operator's choice of upstream.
- A key and a body are not scanned for secrets: kman forwards what an app sends.

### Real credentials, the master key, the data directory

**What kman does.** Credentials are encrypted under a data key, itself wrapped by the master key; the master key stays out of the data directory (invariant 1), and unlocking hardens the process (no core dumps; on Linux, non-dumpable) and wipes the key after use. `kman rekey` replaces both keys in one transaction, refuses to run beside a server, and deletes the old data key, with `secure_delete` on so old sealed bytes do not linger in free pages. Credentials are never shown after entry, and `last4` aside, appear nowhere (invariant 3). The planted-secrets session found nothing.

**What is left.**

- **A copy made before a rekey opens with the old master key** (the `kman.db.pre-<NNNN>` copies, a backup of the data directory, a snapshot), and a rekey can overwrite that key. After a master-key leak, destroy old copies too and rotate the credentials at their providers ([Upgrade, rekey and old copies](docs/operator-guide/upgrade.md#replace-the-master-key)).
- **L-4:** the master key file's mode is not checked, so a `0644` key file starts silently (Docker secrets are `0444`, so a warning needs care). With `KMAN_MASTER_KEY` the key sits in the process's environment (kman warns). Credential copies in header strings for forwarding live in Go strings that cannot be wiped, so "wipeable" covers the vault's cache, not every copy.
- **L-1: `last4`.** A credential's last four characters are kept (for secrets of 16 bytes or more) so you can tell which provider key to revoke; providers show the same four. Invariant 3 read literally says "never shown", so the SPEC is to be amended to say so; this is the maintainer's call and, if refused, `last4` comes out before v0.1.0.
- **A Unix socket is removed on startup (L-2):** `kman serve` removes any file at the admin socket path, which could be another live kman's socket.
- **Windows and macOS.** The review ran on Linux. File modes (`0600`, `0700`) are Unix concepts; on Windows kman relies on the profile directory's access, and no hardening is applied. Treat the key sources there as unreviewed.

### The admin API

**What kman does.** Bearer tokens only (`Authorization: Bearer kma_…`, never a cookie); the token is looked up by its hash on every request, so a revoked token is refused at once; `Cache-Control: no-store`; bodies are capped at 1 MiB and unknown fields are refused; no CORS, and every route needs a non-simple header, so a web page cannot call it blind. The listener is `127.0.0.1`, or a `0600` Unix socket; plain HTTP off loopback is refused unless TLS or `allow_plain_http` says otherwise. Every change is audited with the token's name. `/metrics` on the admin listener needs an admin token too; the opt-in `metrics_listen` serves it with no authentication, so keep it on loopback or a private network.

**What is left.** The owner admin token is kept in `credentials.json` (`0600`) by design, so anyone who can read that file as you is the owner. `kman login` warns on plain HTTP to a remote host and proceeds (L-7). Keep the admin listener off the network ([TLS and reverse proxies](docs/operator-guide/tls.md)).

### The supply chain

**What kman does.** One workflow builds every release from a pushed tag, with a pinned toolchain, `-trimpath`, and commit timestamps, so releases are reproducible and a post-release job rebuilds them and compares checksums. `checksums.txt` is signed keylessly (cosign) against the exact tag identity, archives and the image carry build provenance (SLSA Build L2) and SBOMs, and the GitHub release is immutable. `install.sh` always checks the SHA-256, verifies the signature with `cosign` or `gh` against that identity (a failed check is final), uses HTTPS only, redirects included, validates the tag before using it, and works in a `0700` temp directory. Workflows pin every action by SHA (a gate enforces it), start every job with `harden-runner`, keep top-level permissions read-only, pass untrusted values through `env`, and use no `pull_request_target`. [How to verify](docs/verify.md).

**What is left.**

- **M-5: `install.sh` installs with no verifier after a warning** when neither `cosign` nor `gh` is installed. The warning is loud and `KMAN_REQUIRE_SIGNATURE=1` refuses instead, but the default trusts a checksum that comes from the same place as the archive. Consider making `1` the default before v1.0.
- **L-8:** `install.sh` is not wrapped in a function, so a truncated `curl | sh` could run part of it. Every line is quoted so a cut leaves a parse error and no action, but it is cheap to fix.
- **M-6: one release job holds every write scope** (`contents`, `attestations`, `id-token`, `packages`) and also runs `govulncheck` built from `tools/go.mod`, so a compromised tool dependency would run with signing rights. `harden-runner` is in audit mode, not block.
- **M-7: the price data.** The weekly job fetches three third-party datasets from their `main` branches into a table that governs cap enforcement. A person reviews the pull request and the generator keeps the most conservative of the three sources, but a poisoned dataset lowering a price is a quiet way to weaken every cap, and the token that opens the pull request is broad.
- A binary from `go install` is unsigned and unstamped.

## Assumptions

kman is only as safe as these hold:

1. **The host is not compromised,** and its user accounts are separate. Someone who can read your files as you owns the install.
2. **TLS is in place wherever a key crosses a network.** kman serves plain HTTP on loopback by default, and a virtual key sent over an open network is a stolen key. The proxy listener can be exposed with no warning (L-3): put TLS in front ([TLS and reverse proxies](docs/operator-guide/tls.md)).
3. **You keep the master key apart from the data directory,** and keep an offline copy.
4. **The provider tells the truth about usage,** because kman charges what a response reports (and, where it reports none, the whole reservation).
5. **The clock is right.** Expiry and spend periods follow it.

## What v0.1.0 does not do

Detection, alerts, address locks, soft budgets, the dashboard, leak scanning, backup and restore commands, and realtime: [the list, and what to do meanwhile](docs/v0.1.0-scope.md). The review did not cover Windows or macOS behaviour, whether the systemd and Docker guides run end to end (no CI job runs `rekey` in them), or what a live provider really sends: the test transcripts are written from the SDKs' types, not captured.

## How this was checked, and what to rerun

Kept as tests or scripts so they can be rerun:

- the **planted-secrets session** (`e2e/secrets_test.go`): add any new secret-bearing surface to it;
- the **network recording** (`scripts/record-network.sh <kman> [--control]`, Linux, user namespaces): run before each release;
- the **regression tests** for the body and listener limits (`internal/gateway/bodyhold_test.go`, `cmd/kman/listener_test.go`);
- the test that fails if the docs or the program's output name a command this release does not ship (`cmd/kman/unshipped_test.go`).

The open findings (7 medium, 7 low) are listed in full, with suggested mitigations, in [the review](docs/security-review-v0.1.0.md#findings).
