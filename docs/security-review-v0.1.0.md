# Security review of v0.1.0

Ticket #59. One focused review of everything v0.1.0 ships, against SPEC §1. It asks one question: can someone get past a key's limits, read a secret, or keep using a killed key? It feeds `THREAT-MODEL.md` (#54). Reviewed at `m1-contain` 8ea27b8 (tickets 29–42, 44, 52, 53). Everything not marked "fixed" below is open.

## Result

| Severity | Count | State |
|---|---|---|
| Critical | 0 | |
| High | 1 (S-1, three parts) | fixed in this ticket, test-first |
| Medium | 7 | open; list them as issues |
| Low / informational | 7 | open |

No secret reached any place it should not: the planted-secrets session found none. A bare install sent no packet off the machine. A disabled, revoked or expired key was refused at its next request by every route we tried. The one high finding is availability: a key holder, and for one part an unauthenticated client, could hold kman's connections and memory.

## Method

- Read every module on the request path and the admin surface, and the tickets' notes, against the SPEC §1 invariants.
- Ran two sessions end to end, kept as tests or scripts so they can be rerun (below).
- Tried the bypasses the ticket lists, with probes written for the purpose. The probes that found something became regression tests. The rest are recorded here as evidence.

## Invariants

| # | Invariant | Verdict | Evidence |
|---|---|---|---|
| 1 | Master key never in the data directory | Holds | `e2e` `TestServeRefusesAMasterKeyInsideTheDataDirectory`, `…ReachedThroughASymlinkIntoTheDataDirectory`, `…AKeyFileFromTheEnvironmentInsideTheDataDirectory`, `TestInitRefusesADataDirectoryHoldingTheMasterKey`. By hand: data dir `0700`, `kman.db` `0600`, config dir `0700`, `master.key`, `credentials.json`, `kman.yaml` `0600`, under umask 022. |
| 2 | Every change goes through admin: one transaction, audited with an actor | Holds, with two bookkeeping exceptions (L-5) | Writers outside `admin`: `init` (audited `system:init`), `rekey` (audited `system:rekey`), and the meter's spend increments (a counter, kept out of the audit on purpose, `TestSpendIsNotWrittenToTheAudit`). `TestEveryCommandIsAuditedWithItsActor`. The audit row commits inside the command's transaction. |
| 3 | A credential is never shown to an app, operator, log, event, alert or metric; virtual-key secrets and admin tokens only once | Holds, with the caveats M-1 and L-1 | `e2e/secrets_test.go` `TestPlantedSecretsAppearOnlyWhereTheyAreMeantTo` (below), and `TestACredentialReachesNoOutputOfTheRealBinary`, `TestAKeySurvivesARestartAndItsSecretIsShownOnce`. |
| 4 | Capped or allowlisted keys never forward an unmetered request | Holds | `TestRegression_F1_*` (gateway, meter, e2e). Probes: duplicate and re-cased `model`, escaped keys, non-string model, compressed and non-JSON bodies, GET with no body: every one is `unmetered_request` or reads the same model the upstream will. Residual risk is M-2 and M-3. |
| 5 | The request path takes no locks and reads an immutable snapshot | Holds for the registry; not literally for the whole path (L-6) | `TestLookupTakesNoLocks`, `TestLookupAllocatesNothing`, `TestLookupsSeeOnlyWholeSnapshots`. |
| 6 | No network call the operator did not configure or ask for | Holds | Packet recording below; `TestPricingCannotReachTheNetwork`, `TestPriceDataUsesNoNetwork`, `TestPriceCommandsMakeNoOutboundCall`, `TestTheProxyInTheEnvironmentIsNotUsed`. The only dialer in production code is the gateway's transport (and the CLI client, which dials the admin URL it was given). |
| 7 | Price data changes only with a release or an audited override | Holds | The table is embedded; overrides are admin commands with audit entries (`TestAPriceOverrideSurvivesARestartAndIsAudited`). The release-side risk is M-7. |
| 8 | No listener is public by default; plain HTTP on a non-loopback admin address is refused | Holds | `TestServeBindsBothListenersToLoopback`, `TestServeRefusesPlainHTTPOnAPublicAdminAddress`, `TestServeBindsAdminToAPrivateUnixSocket`. The proxy listener may be bound off loopback with no warning (L-3). |

## The two sessions

**Planted secrets (invariant 3)**, `e2e/secrets_test.go`. One session creates a bearer credential, a Basic `user:password` credential, three virtual keys, an admin token that is then rotated, and an owner token and master key from `init`. It sends good requests, an upstream 401, a forged key, a downed upstream, and the key in `X-Api-Key`, `Api-Key`, `X-Goog-Api-Key` and a provider header. It runs every read command in both forms, every GET of the admin API, failing commands, a revoke and a disable, and a second start with the key from the environment. It then searches what every command printed, every API response after creation, `kman audit` and `audit export`, the server log (both starts) and every file under the data and config directories, `kman.db`, its WAL and everything else, for each secret, its tail without the id and checksum, the Basic password alone, the first 40 characters of the master key and its raw bytes. A secret may appear only where it belongs: the owner token in `credentials.json`, the master key in `master.key`, the credential in the one request that needs it. Nothing else matched. The upstream was also checked: it received the credential as it should, and no virtual key in any header or body. Mutation check: allowing nothing for the owner token fails the test on `credentials.json`.

**Network (invariant 6)**, `scripts/record-network.sh <kman> [--control]`, Linux only. It runs the session in a private network namespace (no root needed) whose only route is a dummy interface that a raw socket records, so nothing leaves the machine. The session: `keygen`, `init`, `serve`, plain `kman`, `version`, `settings get`, `price list` and `price set`, `key list`, `upstream list`, `admin-token list`, `audit`, a stop, `rekey`, a second start. Result, nothing configured: **0 packets**. Control, with an OpenAI upstream and one request through the proxy: 4 UDP packets to the resolver, for `api.openai.com`, and nothing else. The control shows the recorder works. This is not a CI job (it needs user namespaces); run it before each release.

## Limits

- **Cap bypass (F1 and kin).** F1 and F2 and F3 regression tests are green. The parser the meter reads `model` with and the one the allowlist reads it with agree on every unambiguous body, and the allowlist's refuses every ambiguous one. The one disagreement is a repeated `model` on a cap-only key (last wins in kman, upstreams differ); settlement charges the response's own model, so the overshoot is one request. Path rules were probed with 18 evasions (`;x`, `%2e%2e`, `%2f`, `%00`, `//`, case, a trailing slash, `%u0041`): only the equivalent `%63ompletions` matches, and the upstream reads it the same way.
- **Reservations under a price override.** An override sets `max_output`, which sizes the output allowance, so an operator who sets it low sizes reservations low; the audit shows who set it. More important is M-3: an override replaces the whole built-in entry.
- **Avoiding metering.** On an LLM upstream every request is either metered, charged the whole reservation as an estimate (a 2xx with no usage), refused, or exempt only on routes that are free (`count_tokens`, `moderations`, `input_tokens`). An app's `include_usage: false` is overridden. Compressed responses are refused by sending `Accept-Encoding: identity`. A generic-profile upstream is unmetered by design, and a capped key on it is refused.

## Kill path

A disabled, revoked or expired key cannot complete a new request by any route found. Evidence: `TestDisablingAndRevokingAKeyStopTheNextRequest`, `TestAnExpiredKeyStopsTheNextRequest`, `TestDisablingAKeyStopsItInTheRegistryAndIsAudited`, and the gateway's status table. Reading the code for the other routes:

- **Token forms.** One `presentedKey` serves every form (Bearer, token, bare, Basic user or password, `x-api-key`, any header by prefix), and `authenticate` does the status check after the secret check, for all of them. A child key (`ek_`) is refused as `invalid_key`.
- **Stale snapshot.** The registry swaps whole snapshots atomically; `Apply` ignores a generation not newer than the current one, and a command holds the `publishing` mutex from its transaction's start to its publish, so memory sees commit order. The window between a commit and its publish is the length of the `Apply` call.
- **Expiry** is compared with the clock on every request; enabling a key never extends it.
- **In-flight requests** are not cut (M-4).

## Gateway

- **Token parsing:** checksum before any lookup; constant-time compare over every secret; unknown, wrong-secret, canary and credential-less keys all answer `invalid_key`, and an invalid key is counted for the throttle, so guessing is slow.
- **Header stripping:** hop-by-hop, auth (`Authorization`, `Api-Key`, `X-Api-Key`, `X-Goog-Api-Key`), and forwarding headers (`Forwarded`, `Via`, `X-Forwarded-*`, `X-Real-Ip`) are dropped; any header, under any name, whose value starts with a key is dropped; headers named by `Connection` are dropped. The app's `Host` is never forwarded.
- **What reaches the upstream:** the method, the path and query as sent (after the path rules, which refuse `.`, `..`, empty and encoded-slash segments), the metered profile's rewritten body, and the credential. The target host comes only from the upstream row.
- **Redirects:** never followed; within-upstream `Location` is rewritten to a path, anything else is a 502 with the target withheld; backslashes and `//` are refused.
- **What reaches the app:** the upstream's response, minus hop-by-hop headers. kman cannot see what an upstream puts in a body (M-1).
- **Resource bounds:** S-1.

## Admin surface

- Bearer only (`Authorization: Bearer kma_…`); cookies never; the token is looked up by its hash on every request, so revoking is immediate; `Cache-Control: no-store`; request bodies capped at 1 MiB with unknown fields refused; no CORS, and every route needs a non-simple header, so a web page cannot call it blind.
- Listener defaults: `127.0.0.1`, plain HTTP off loopback refused unless TLS or `allow_plain_http`; a Unix socket is `0600`. `kman login` warns on plain HTTP to a remote host.
- Files: see invariant 1. The credentials file is written atomically, `0600`, and holds the owner token by design.
- `rekey`: refuses a running server, the same key and an old key that does not unwrap; a failed rekey leaves the database as it was; the old data key row is deleted, not retired; `secure_delete` is on; its report now says to destroy copies made before it (fixed, F-4). The documented limit stands: a copy of the database made before a rekey still opens with the old key, and a rekey can overwrite that key.

## Supply chain

- `install.sh` checks the archive's SHA-256 against `checksums.txt` always; with cosign it verifies `checksums.txt` against the exact tag identity (certificate identity and OIDC issuer), else with `gh attestation verify` on the archive with `--cert-identity` and `--deny-self-hosted-runners`; a failed check is final; `KMAN_REQUIRE_SIGNATURE=1` refuses with no verifier. It uses HTTPS only, redirects included; validates the tag against a version pattern before using it; extracts one named member; works in a `0700` `mktemp -d`. Findings: M-5 and L-8.
- Workflows: top-level `permissions: contents: read`; write scopes only per job; every action pinned by SHA (a gate enforces it, with `harden-runner` as every job's first step); `cache: false` in release jobs; untrusted values (`github.ref_name`) go through `env`, not into scripts; no `pull_request_target`; `release-snapshot.yml` runs on `pull_request` with read-only permissions. Findings: M-6, M-7.

## Open items decided

1. **Slow-body connection hold (#37).** Decided: fix now. It was a connection and memory hold available to any key holder, so a leaked key could take the proxy down for every other app, and its sibling needed no key at all. No new error code: a stalled body has no one to answer, so the connection is closed (S-1).
2. **`last4` (#34) against invariant 3.** Decided: keep, as it is. Providers show the same four characters in their own dashboards, and an operator needs them to tell which key to revoke at the provider. It is shown only for secrets of 16 bytes or more, so at most a quarter is revealed. Invariant 3 reads literally as "never shown", so **SPEC §1.3 should be amended in a PR** to say "no part of a credential but its last four characters, for secrets of 16 bytes or more". The e2e planted-secrets test already allows exactly that and nothing more. This is a maintainer call; if it is refused, remove `last4` from schema 0001 (editable until the tag), the DTOs and the CLI before v0.1.0.
3. **Output and docs naming unshipped commands.** Found one: `kman rekey`'s report told operators to keep the old key for "every kman backup file you made". Fixed, and `TestNothingNamesACommandThisReleaseDoesNotShip` now fails if any non-comment Go source, `docs/` page (ADRs and this review excepted), example or package file names `kman backup|restore|prune|scan|exposure|dashboard|demo|canary|geoip|channel|incident(s)|detection(s)|known-source` or `metrics`. Take a name off its list when its command ships.

## Findings

### S-1 High, fixed: a request could hold a connection and memory without limit

**Evidence.** With one valid key, 20 connections each sending 31 MiB of a stated 32 MiB body and stopping took kman's resident memory from 19 MB to 898 MB, and it stayed there for as long as the connections were open (no read timeout, no cap across requests). The key's rate limit did not apply, as it counts requests read. A refused request also waited: Go's server reads what is left of a body before it lets an early response out, with no deadline, so a client with no key at all could hold a connection with `Content-Length: 1000` and three bytes. Neither listener closed an idle connection.

**Fix** (`internal/gateway/body.go`, `gateway.go`, `cmd/kman/serve.go`; tests `internal/gateway/bodyhold_test.go`, `cmd/kman/listener_test.go`):
- a body that stops arriving for 30 s, or takes over 5 minutes, ends its connection (`Options.BodyStall`, `BodyTimeout`);
- request-body bytes held across all requests are capped at 512 MiB; past it a request is refused with 429 `rate_limited`, with `Retry-After` (`Options.MaxBuffered`);
- a refusal made before the body was read closes the connection after the answer;
- both listeners close idle connections after 2 minutes and limit a head to 64 KiB.

The limits are constants and options, with no `kman.yaml` keys, as for the body limit (#37).

### Medium (open)

- **M-1. An upstream's error can show a masked credential to an app.** Providers answer a rejected key with text such as "Incorrect API key provided: sk-proj-…abcd". If a credential is revoked or wrong, the app reads those characters (the prefix and the last four). kman does not read response bodies, and masking is the upstream's. Mitigation to consider: replace upstream 401s on a metered profile with kman's own `upstream_unavailable` (the app can do nothing about a bad credential anyway), at the cost of hiding the provider's message. A generic upstream that echoes request headers would show the whole credential; that is the operator's choice of upstream.
- **M-2. The reservation's input estimate is SPEC's bytes ÷ 4.** Text that tokenizes densely (rare scripts, emoji, code, separated letters) costs up to about three times that, so the amount a key may overshoot its cap is up to about three times the input side of what is in flight. Output is exact (the upstream enforces `max_tokens`). Admission still bounds reservations to the cap, so the bound is a small multiple of the cap, not unbounded. Consider bytes ÷ 2.
- **M-3. A price override replaces the whole built-in entry.** Tiers, long-context bands and per-call tool fees are gone, so a request for a priority tier, a very long prompt or a search tool is reserved and charged at the base rate. Documented in the admin API; the audit shows the override. Consider keeping the built-in's tiers, bands and fees unless the override lists its own.
- **M-4. Requests in flight when a key is disabled or revoked run to completion.** A long stream outlives the kill. SPEC promises only the next request (realtime sessions are M4), but operators will expect a revoke to cut a running stream. Consider cancelling a key's in-flight contexts on a disable or revoke.
- **M-5. `install.sh` installs with no verifier after a warning.** That is SPEC (§15) and the warning is loud, but then the only check is a checksum from the same origin as the archive. Consider making `KMAN_REQUIRE_SIGNATURE=1` the default and `0` the opt-out before v1.0.
- **M-6. One release job holds every write scope.** `contents`, `attestations`, `id-token` and `packages` write are on one job that also runs `govulncheck` built from `tools/go.mod`. A compromised tool dependency would run with signing rights. Consider splitting the scan into its own read-only job, and moving `harden-runner` to `block` with an allowlist once the egress set is known.
- **M-7. The weekly price job feeds three third-party datasets, fetched from their `main` branches, into a table that governs cap enforcement.** A human reviews the pull request, and the generator keeps the conservative value of three sources, but a poisoned dataset lowering a price is a quiet way to weaken every cap. The `PRICE_DATA_TOKEN` is a broad token. Consider pinning each dataset by commit in the script and having the PR show the diff of lowered prices first.

### Low and informational (open)

- **L-1.** `last4` (decided above): amend SPEC §1.3.
- **L-2.** `listenUnix` removes any existing socket file at the admin path, which could be another live kman's.
- **L-3.** The proxy listener can be bound off loopback, in plain HTTP, with no warning. Invariant 8 only constrains admin. Consider a startup warning.
- **L-4.** The master key file's mode is not checked, so a `0644` key file starts silently (Docker secrets are `0444`, so a warning would need care). With `KMAN_MASTER_KEY`, the key stays in the process's environment, which kman warns about. Credentials copied into header strings for forwarding are not wiped (Go strings), so "wipeable" covers the vault's cache, not every copy.
- **L-5.** `Authenticate` writes `last_used_at` outside a command, at most once a minute, without audit or generation. It is bookkeeping, but SPEC §1.2 should say so.
- **L-6.** The limiter, the meter's day-rollover, `sync.Pool` and `sync.Map` take small locks or atomics on the request path. SPEC §1.5 and ADR 0006 mean the registry; say "the registry read takes no locks".
- **L-7.** Plain `kman login --admin-url http://remote` warns and proceeds; fine.
- **L-8.** `install.sh` is not wrapped in a function, so a truncated `curl | sh` could run a partial script. Every line is quoted so a cut leaves a parse error and no action, but wrapping it in `main` is cheap.
- **F-4 (fixed).** `rekey`'s report named `kman backup`.

## What this review did not cover

- Windows and macOS behaviour (file modes, the key sources). The review ran on Linux.
- Whether the systemd and Docker operator guides run: no CI job runs `rekey` in them (#35's note).
- The live upstreams: transcripts are hand-written from the SDKs' types (#40's note), so what a real provider sends in a body or header was read, not captured.
- Anything not in v0.1.0: realtime, canaries, detection, the dashboard, alerts, events, rotation, CIDR locks.
