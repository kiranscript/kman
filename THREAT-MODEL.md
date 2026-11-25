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
| 3 | A credential is never shown to an app, an operator, a log, an event or a metric; virtual-key secrets and admin tokens are shown once | A planted-secrets session searched every output, response, file and log for each secret and its fragments, and found nothing outside the places they belong. The one deliberate exception is a credential's last four characters (L-1). Later builds add a planted `User-Agent` to the session, with a key, an address and a sentinel in it, which an event keeps only as a client (below). | `e2e/secrets_test.go`; the caveats are M-1 and L-1. |
| 4 | A capped or allowlisted key never forwards an unmetered request | Probes with duplicate and re-cased `model`, escaped keys, non-string models, compressed and non-JSON bodies, and bodiless GETs were each refused as `unmetered_request` or read to the same model the upstream will read. | Regression tests in the gateway, the meter and end to end. Residual risk: M-2, M-3. |
| 5 | The request path takes no locks and reads an immutable snapshot | True of the registry's lookup. The limiter, the meter's day rollover and some pools take small locks or atomics (L-6). | Lookup tests for no locks, no allocations and whole snapshots only. |
| 6 | kman makes no network call the operator did not configure or ask for | A recorded session of a bare install sent **0 packets**; alerts reach the network only through a channel the operator added; with one OpenAI upstream and one request it sent 4 DNS packets for the upstream's name and nothing else. | `scripts/record-network.sh` (Linux; run before each release). |
| 7 | Price data changes only with a release or an audited override | The table is embedded in the binary; overrides are admin commands with audit entries. The release-side risk is M-7. | Tests that pricing cannot reach the network. |
| 8 | No listener is public by default; plain HTTP on a non-loopback admin address is refused | Both listeners bind `127.0.0.1`; an admin Unix socket is `0600`. The proxy listener, though, can be bound off loopback in plain HTTP with no warning (L-3). | Listener tests. |

## Threats, one by one

### A virtual key leaks

**What kman does.** The key can only do what its policy allows: its cap (reserved before the request, so it overshoots by at most one request), its models, its paths, its rate limit, and until its expiry, at most the maximum lifetime (365 days by default) from creation. You stop it with `kman key disable` or `kman key revoke`, and a disabled, revoked or expired key was refused at its next request by every route the review tried: every token form (`Authorization` as Bearer, token, bare or Basic, `x-api-key`, any header carrying the prefix) goes through one authentication that checks status after the secret. The registry swaps whole snapshots atomically, and an older generation never replaces a newer one. The real credential is not in the key, so a leak never exposes it.

**What is left.**

- **No detection.** Nothing in v0.1.0 notices a leak; you find out from the spend ([what to do meanwhile](docs/v0.1.0-scope.md)). A build after it adds canary keys, the one tripwire that needs no baseline: a key with no credential that raises a critical alert at once when anyone uses it ([ADR 0013](docs/adr/0013-a-canary-is-a-key-with-no-credential-and-a-row-of-its-own.md)). It answers `invalid_key` like any bad key and nothing in it or in its planting snippet says what it is, and it is only as good as where it is planted. Its ignore list is the operator's own scanners and is bounded so that it cannot silence the canary. An alert names the address the key was used from, which is personal data in a channel the operator chose. A canary's use also opens an incident the operator can acknowledge and resolve ([ADR 0014](docs/adr/0014-an-incident-is-one-case-per-key-and-an-operator-answers-it-with-commands.md)); the incident's evidence holds the address, and the admin API withholds it unless `expose_client_addresses` is on. A **known source** is the operator's statement that a place is legitimate and is bounded (no wider than an IPv4 /16 or IPv6 /32, never `none` or `unknown` as a client, at most 200 per scope) so that one cannot silence a rule for everyone; it never applies to a canary. Anyone with an admin token can add one, and doing so is audited and alerted (medium).
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

### Alert channels

**What kman does.** An alert is the one thing kman sends on its own, and only to a channel the operator added (invariant 6, [ADR 0008](docs/adr/0008-no-network-call-unless-configured.md)): with none, a test fails if alerts touch the network at all. A channel's address is sealed like a credential, since a Slack address is itself a secret: it is read from a prompt, a pipe or an environment variable and never an argument, it is shown nowhere after entry except its host, and the errors kman records or prints never quote it. The address must be `https` (plain `http` only to loopback), holds no login, and kman follows no redirect, so an alert does not end up somewhere the operator did not name. An alert is built from names, ids, counts and addresses and has no field for a secret or for request content; the audit entry's before and after are not copied into it. A webhook is signed with HMAC-SHA256 over its timestamp and body, under a secret shown once. Each channel has its own delivery worker and a 10 second timeout, so a channel that hangs delays no other. The audit log's alert is queued in the transaction of the change, so a committed change is never without its alert.

**What is left.**

- **Alerts are at least once.** A receiver may see a delivery twice and must drop a repeated `X-Kman-Delivery`.
- **An alert is not authentication.** Slack and the receiver of an unsigned address take what arrives: only a webhook is signed, and a receiver has to check the signature and the timestamp.
- **A key's name and id travel to the channel.** They are labels, not secrets, but a Slack workspace will hold them. The outbox holds delivered alerts, with the same content, for the retention period (30 days).
- **A failing alert pipeline is silent to the app** and visible only in `kman channel list` and the log. Watch for a channel with alerts pending.
- **A request kman never saw cannot alert.** Alerts follow the audit log and the priority lane; the detection rules are not in this release. Spend, expiry and refused-credential warnings are alerts too, with the same content rules, and a warning can come up to a minute after the spend that raised it.

### The supply chain

**What kman does.** One workflow builds every release from a pushed tag, with a pinned toolchain, `-trimpath`, and commit timestamps, so releases are reproducible and a post-release job rebuilds them and compares checksums. `checksums.txt` is signed keylessly (cosign) against the exact tag identity, archives and the image carry build provenance (SLSA Build L2) and SBOMs, and the GitHub release is immutable. `install.sh` always checks the SHA-256, verifies the signature with `cosign` or `gh` against that identity (a failed check is final), uses HTTPS only, redirects included, validates the tag before using it, and works in a `0700` temp directory. Workflows pin every action by SHA (a gate enforces it), start every job with `harden-runner`, keep top-level permissions read-only, pass untrusted values through `env`, and use no `pull_request_target`. [How to verify](docs/verify.md).

**What is left.**

- **M-5: `install.sh` installs with no verifier after a warning** when neither `cosign` nor `gh` is installed. The warning is loud and `KMAN_REQUIRE_SIGNATURE=1` refuses instead, but the default trusts a checksum that comes from the same place as the archive. Consider making `1` the default before v1.0.
- **L-8:** `install.sh` is not wrapped in a function, so a truncated `curl | sh` could run part of it. Every line is quoted so a cut leaves a parse error and no action, but it is cheap to fix.
- **M-6: one release job holds every write scope** (`contents`, `attestations`, `id-token`, `packages`) and also runs `govulncheck` built from `tools/go.mod`, so a compromised tool dependency would run with signing rights. `harden-runner` is in audit mode, not block.
- **M-7: the price data.** The weekly job fetches three third-party datasets from their `main` branches into a table that governs cap enforcement. A person reviews the pull request and the generator keeps the most conservative of the three sources, but a poisoned dataset lowering a price is a quiet way to weaken every cap, and the token that opens the pull request is broad.
- A binary from `go install` is unsigned and unstamped.

### What an event records about the caller

**What kman does.** An event holds no header, with one exception ([ADR 0012](docs/adr/0012-an-event-records-the-calling-client-reduced.md)): the request's `User-Agent`, reduced to a **client**, which is a product name and a major version, such as `openai-python/3` or `curl/8`, in at most 32 bytes (`none` when there is no header, `unknown` when it holds no product). An app controls its `User-Agent`, and apps put things in it that they should not: a key, a token, a host name, an email address, a user's name. So the reduction keeps only a word of letters, digits, `_` and `-` that starts with a letter, and up to four digits after a `/`, and cuts everything else; it is a pure function that reads at most 256 bytes, is fuzzed to hold that shape whatever it is given, and never keeps a word that holds `kmn_`, that is followed by `@`, `:` or `.` (an email, a host name, an address, a URL), that is twenty characters or more with a digit in it (a token), that has more than one run of digits or more than three digits (a host label such as `ip-10-0-3-7`, or a short token), or that fills all 256 bytes it reads, so that what follows it cannot be seen. The raw header is not stored, logged, put in a metric or sent in an alert. The client is shown always, unlike a client address ([ADR 0010](docs/adr/0010-client-addresses-are-opt-in-on-the-admin-api.md)): it names a program and not a person. It is recorded for a request that proved no key too, so probing can be told by its client, at the cost of reducing a few hundred bytes of an unauthenticated request's header.

**What is left.**

- **A client is a claim, not proof.** It is as easy to change as the header; a holder of a leaked key can send the `User-Agent` of the key's own app. It is a signal for an operator and for the location rules, and no part of authentication.
- **A first word that looks like a product but is not one survives.** An app that sends only its user's first name, or a project's code name, as its whole `User-Agent` has that word kept, in lower case and cut to 32 bytes. What follows a first word that is an address, an email, a dotted host name or a URL is never kept, and neither is a `kmn_` key, a name of twenty characters or more holding a digit, or a name with more than one run of digits or more than three digits, which an EC2 default host name (`ip-10-0-0-1`), a numbered host (`node-0042`) and a short token (`a1b2c3`) all have. A dotless host label in words with one short number (`build-server-03`, `web01`) or a short token of letters can survive, bounded to one word of 32 bytes: it cannot be told from a product (`urllib3`, `aws-sdk-go-v2`).
- **A product named with numbers reads as `unknown`.** The rule that refuses host labels refuses a real product with two runs of digits or more than three digits too (`x86_64-linux-gnu`, a name with a year in it). `unknown` cannot be a known source, since it would silence every request that does not say what it is, so an operator whose app is one of these takes one `new_client` alert after learning, or turns the rule off for the key. Once `unknown` is in a key's places, any other `User-Agent` that reads as `unknown` is no new client for that key: a client is a claim and not proof, so the rule is a signal and this weakens it only for such keys.
- **The `User-Agent` is still forwarded to the upstream as the app sent it,** as before: kman's header stripping removes a key at the start of a header's value (see above), not one inside a longer one. That is the app's own header going to the provider it chose, and not something kman records.
- **A request with no key costs a little more.** Its client is reduced, in about 60 nanoseconds for a real `User-Agent` (`BenchmarkClientOf`; `BenchmarkBadKey` builds its gateway without an event bus, so it does not reach the reduction); the made-up key itself still costs nothing further (`BenchmarkBadKey`).

### What the location rules reveal and miss

**What kman does.** After a key's learning period (7 days from its first forwarded request, by default), the first use from a country, ASN, network or client the key has never been used from raises a detection on its incident and an alert ([ADR 0015](docs/adr/0015-the-location-rules-run-in-the-transaction-that-records-a-new-place.md)). A refused request counts. The detection runs in the event writer's transaction, off the request path.

**What is left.**

- **A thief inside the baseline is invisible.** A place the key had during its learning period is normal to these rules, so a key that leaked in its first week, or that is used from the thief's network while it is still being learned, raises nothing. A canary is what catches that; a baseline is only as good as its first week.
- **A thief who copies the owner's country, network and client is invisible too.** A client is the caller's own claim, a country and ASN are only as precise as the GeoIP database, and a network is a /24 or /64. The rules make the easy case loud, a key turning up somewhere new, and nothing more.
- **A key that was only ever refused is not watched.** No learning period starts until a forwarded request, so nothing is raised for a key whose every request was refused (the refusals are in `kman events` and `kman key places`).
- **The first place of a kind is raised once learning is over,** so that a thief abroad is loud on a key used only on a private network. The price is one incident per key when a GeoIP database is added later (or clients start being recorded), which an operator may learn to dismiss; `this-was-me` resolves it, and installing GeoIP before creating keys avoids it.
- **A key used from more than 1,000 places of a kind stops raising for that kind.** The overflow row is not a place.
- **A known source silences what it names, and a CIDR silences a whole request.** A country, ASN or client the operator added hides only a new place of its own kind, so a thief from a known country is still raised for a new ASN or client, but a thief who matches it in one way and is new in no other raises nothing. A known CIDR hides everything from inside it. The bounds that keep an operator from silencing everything are in [ADR 0014](docs/adr/0014-an-incident-is-one-case-per-key-and-an-operator-answers-it-with-commands.md).
- **A flood is partly counted, not raised.** Past 100 requests of one key in a writer batch, only a rule not yet raised in that batch is raised; the places are all recorded, and a log line says so. This keeps a thief spraying new addresses from stalling event recording.
- **Networks are personal data.** The alert names the address, the network and, for a new network, the networks the key normally uses, because it goes to the operator's own channel. The admin API, the CLI and the incident timeline withhold all three unless `expose_client_addresses` is on (ADR 0010). Text from a database file or a header in the alert is cleaned of control characters and bounded.

## Assumptions

kman is only as safe as these hold:

1. **The host is not compromised,** and its user accounts are separate. Someone who can read your files as you owns the install.
2. **TLS is in place wherever a key crosses a network.** kman serves plain HTTP on loopback by default, and a virtual key sent over an open network is a stolen key. The proxy listener can be exposed with no warning (L-3): put TLS in front ([TLS and reverse proxies](docs/operator-guide/tls.md)).
3. **You keep the master key apart from the data directory,** and keep an offline copy.
4. **The provider tells the truth about usage,** because kman charges what a response reports (and, where it reports none, the whole reservation).
5. **The clock is right.** Expiry and spend periods follow it.

## What v0.1.0 does not do

Detection, soft budgets and expiry warnings, address locks, the dashboard, leak scanning, backup and restore commands, and realtime (later builds add all but detection, the dashboard and realtime): [the list, and what to do meanwhile](docs/v0.1.0-scope.md). The review did not cover Windows or macOS behaviour, whether the systemd and Docker guides run end to end (no CI job runs `rekey` in them), or what a live provider really sends: the test transcripts are written from the SDKs' types, not captured.

## How this was checked, and what to rerun

Kept as tests or scripts so they can be rerun:

- the **planted-secrets session** (`e2e/secrets_test.go`): add any new secret-bearing surface to it;
- the **network recording** (`scripts/record-network.sh <kman> [--control]`, Linux, user namespaces): run before each release;
- the **regression tests** for the body and listener limits (`internal/gateway/bodyhold_test.go`, `cmd/kman/listener_test.go`);
- the test that fails if the docs or the program's output name a command this release does not ship (`cmd/kman/unshipped_test.go`).

The open findings (7 medium, 7 low) are listed in full, with suggested mitigations, in [the review](docs/security-review-v0.1.0.md#findings).
