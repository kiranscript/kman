# kman v1 (solo tier): specification

kman is a self-hosted proxy that holds real upstream API keys and hands apps virtual keys that can be limited, watched, and killed, so a leaked key is worth less and is easy to contain. It is a fresh-code rebuild that supersedes [keman](https://github.com/kirank55/keman). This document is what gets built.

## How to read this

- **Terms** are defined in [`GLOSSARY.md`](GLOSSARY.md) and used exactly. A bold term below is a glossary term.
- **Hard decisions** live in the ADRs under [`docs/adr/`](docs/adr/). This spec states what each one means for the build in a line or two and links to it. The ADR wins if the two ever disagree.
- **Why** lives in the decision tickets on [the map](https://github.com/kirank55/kman/issues/1). They are history: where a ticket comment and this spec disagree, **this spec wins**. Every disagreement found while assembling it is listed in [Consistency fixes](#consistency-fixes).
- **Requirements** are bullets. "Must" and "never" are tested; each milestone's [done criteria](#milestones-done-criteria-and-build-order) name the tests.
- The [dashboard prototype](https://github.com/kirank55/kman/tree/prototype/dashboard) (`internal/dashboard/prototype/index.html`, variant B) is the visual reference for M3. It is never merged.

## Contents

1. [Invariants](#1-invariants)
2. [Architecture](#2-architecture)
3. [Install, first run, and configuration](#3-install-first-run-and-configuration)
4. [Tokens and virtual keys](#4-tokens-and-virtual-keys)
5. [Upstreams, presets, credentials, and secrets](#5-upstreams-presets-credentials-and-secrets)
6. [The proxy](#6-the-proxy)
7. [Policy](#7-policy)
8. [Spend and pricing](#8-spend-and-pricing)
9. [Events and alerts](#9-events-and-alerts)
10. [Detection and incidents](#10-detection-and-incidents)
11. [Realtime](#11-realtime)
12. [Admin API and CLI](#12-admin-api-and-cli)
13. [Dashboard](#13-dashboard)
14. [Storage](#14-storage)
15. [Release and supply chain](#15-release-and-supply-chain)
16. [Docs](#16-docs)
17. [Testing and quality gates](#17-testing-and-quality-gates)
18. [Milestones: done criteria and build order](#milestones-done-criteria-and-build-order)
19. [Later, and out of scope](#later-and-out-of-scope)
20. [Consistency fixes](#consistency-fixes)
21. [Handoff](#handoff)

## 1. Invariants

These hold in every milestone. Each has a test.

1. **The master key never lives in the data directory.** kman refuses to start otherwise ([ADR 0003](docs/adr/0003-master-key-outside-the-data-dir.md)).
2. **Every change to a running kman goes through the admin API's service layer**, is one store transaction, and is audited with its **actor**. Nothing is dashboard-only. Child keys are the one exception ([ADR 0004](docs/adr/0004-one-mutation-path-nothing-dashboard-only.md)).
3. **A credential is never shown to an app, an operator, a log, an event, an alert, or a metric** after it is entered. Virtual-key secrets and admin tokens are shown once, at creation.
4. **Capped or allowlisted keys never forward an unmetered request.**
5. **The request path takes no locks** and reads an immutable snapshot ([ADR 0006](docs/adr/0006-module-map.md)).
6. **kman makes no network call the operator didn't configure or ask for** ([ADR 0008](docs/adr/0008-no-network-call-unless-configured.md)).
7. **Price data changes only with a release or an audited price override** ([ADR 0007](docs/adr/0007-price-data-ships-with-releases.md)).
8. **No listener is public by default.** Both bind to `127.0.0.1`. Plain HTTP on a non-loopback admin or dashboard address is refused unless explicitly enabled.

## 2. Architecture

Fifteen modules, one Go package each under `internal/`, plus the `token` leaf package, with an import rule checked by a `go list` test ([ADR 0006](docs/adr/0006-module-map.md)).

| Module | Interface | Owns |
|---|---|---|
| `gateway` | `http.Handler` for the proxy listener | The pipeline: authenticate → policy → limit → reserve → forward → settle → emit. Header stripping, redirect blocking, a bounded body buffer. |
| `profiles` | `Profile`, `RealtimeAdapter` | OpenAI, Anthropic, and generic profiles; realtime adapters; presets as data. |
| `policy` | `Evaluate(facts, policy) Decision`, pure | Allowlists, locks (`allowed_cidrs`, `allowed_countries`; each lock that is set must hold), model rules, the unmetered rule, and the GeoIP reader (`policy/geoip`, files only, no network). |
| `limiter` | `Allow(key)`, `AllowInvalid(ip)` | RPM limits and invalid-key throttling, in bounded LRU memory. |
| `meter` | `Reserve`, `Settle`, `Spend` | All spend: counters, periods, increment flushes, fail-closed, soft-budget crossings. |
| `pricing` | `Estimate`, `Cost` | The **price table** and **price overrides**. |
| `registry` | `Lookup(keyID)`, `Apply(change)` | The snapshot, swapped atomically, and its generation. |
| `vault` | `Credential(id) []byte`, `Seal`, offline `Rekey` | Envelope encryption, the wipeable plaintext cache, four key-source adapters. |
| `store` | `Do(ctx, func(Tx) error)`, typed repos | SQLite, migrations, typed errors (`ErrConflict`, `ErrNotFound`). |
| `events` | `Emit`, `Subscribe` | A bounded queue with a priority lane, and fan-out. |
| `detect` | `Observe(event) []Decision` | Baselines, rules, incidents. Returns decisions, mutates nothing. |
| `alerts` | `Notify(alert)` | The persisted outbox, retries, and the channel adapters. |
| `sessions` | `Open`, `CloseKey(keyID)`, child-key mint | Live realtime sessions and in-memory child keys. |
| `admin` | one command per operation | Store transaction → registry update → audit entry. |
| `adminapi`, `dashboard`, `cli` | transports over `admin` | Typed DTOs and OpenAPI, templ pages, the one typed client. |
| `token` | `Kind`, `KeyID`, and the token formats | A leaf: the token types shared by every module, and minting and parsing per ADR 0001. It imports no internal package. |

Import rule: transports import only `admin`. `admin` imports `store`, `registry`, `vault`, `events`, `sessions`. `gateway` imports `registry`, `policy`, `limiter`, `meter`, `vault`, `profiles`, `events`, `sessions`, and never `admin`. `alerts` imports `store` and `events`, and gets what `admin` hands it (the audit entries, in their transaction) through an interface that `admin` defines. Nothing imports a transport. `token` is a leaf that imports no internal package, and any module may import it. `detect` decides; `admin` executes its decisions as actor `system:detect`.

## 3. Install, first run, and configuration

**Platforms and channels** ([onboarding](https://github.com/kirank55/kman/issues/23), [release](https://github.com/kirank55/kman/issues/24)):
- First-class: release archives for Linux and macOS (amd64, arm64) and Windows (amd64); `install.sh`; the GHCR image with a Compose example.
- Best-effort: a Homebrew formula in `kirank55/homebrew-tap`; `.deb`/`.rpm` with a `kman.service` unit; `go install` (unsigned, no version stamp).
- Not code-signed by Apple or Microsoft. macOS users install with `install.sh` (no quarantine flag). The docs cover `xattr -d com.apple.quarantine` for browser downloads and the Windows SmartScreen warning.

**`kman init`** asks nothing:
- Writes `kman.yaml` and `master.key` (`0600`) to the **config directory**, creates the **data directory**, prints the `owner` admin token once, saves it to a `0600` credentials file in the config directory (`--no-save-token` skips this), and prints the next three commands.
- Directories: `~/.config/kman/` and `~/.local/share/kman/` on Linux and macOS; `%APPDATA%\kman\` and `%LOCALAPPDATA%\kman\` on Windows.
- Listeners: proxy `127.0.0.1:8080`, admin `127.0.0.1:8081`. The admin API may instead be a Unix socket (`0600`), in which case the dashboard needs `dashboard_listen`.
- Every choice is a flag (`--data-dir`, `--key-source file|systemd-creds|passphrase|env`).
- In a container with no key source, `init` refuses and prints the Docker steps. In a container it never saves the token. `serve` never initialises an empty data directory by itself.

**Docker:** `docker run --rm ghcr.io/kirank55/kman keygen > master.key`, then `docker compose run --rm kman init` (token to the terminal, not logs), then `docker compose up -d`. The Compose example mounts the key as a secret, keeps the data directory on a volume, uses a read-only root filesystem, and publishes admin on host `127.0.0.1:8081` only.

**systemd:** the packaged unit uses `DynamicUser=yes`, `StateDirectory=kman`, `ConfigurationDirectory=kman`, and `LoadCredentialEncrypted=` for the master key. Package scripts create nothing.

**Signing in elsewhere:** `kman login --admin-url <url>` saves a token and URL to the same credentials file; `kman logout` removes it. `KMAN_ADMIN_TOKEN` still works.

**The happy path** (under 5 minutes, and a CI test runs these exact lines from the docs):
```sh
curl -fsSL https://raw.githubusercontent.com/kirank55/kman/main/install.sh | sh
kman init
kman serve
kman upstream add openai --preset openai
kman key create my-app --upstream openai
export OPENAI_BASE_URL=http://127.0.0.1:8080/v1 OPENAI_API_KEY=kmn_…
```
then optionally `kman dashboard` and `kman canary create`. Plain `kman` prints a status summary and names the next command at every stage.

**Configuration** splits in two:
- `kman.yaml`, read at startup only: listeners, `data_dir`, `key_source`, `trusted_proxies`, GeoIP database paths (`geoip.country_db`, `geoip.asn_db`), `retention`, `update_check`, `dashboard.url`, `allow_key_in_data_dir`, TLS for listeners.
- The `settings` table, changed while running through the admin API and audited: maximum key lifetime, default key TTL (90 days), `spend_timezone` (UTC), install-wide detection thresholds, soft-budget defaults.

**`kman demo`** runs a throwaway install in a temp directory with an in-process mock upstream, seeds a week of compressed traffic that sets off the incidents kman exists for, and opens the dashboard. It shows a "DEMO" banner everywhere, picks free ports, sends nothing off the machine, deletes itself on exit, and refuses to run against a real config.

**Day two:** `kman backup <file>` (a consistent `VACUUM INTO` while running; ciphertext only, so the master key is backed up separately); `kman restore <file>` (server stopped, same master key); upgrades replace the binary, and migrations run forward-only at startup after kman copies the database to `kman.db.pre-<version>` (last two kept). Downgrade means restoring that copy.

## 4. Tokens and virtual keys

**Formats** ([ADR 0001](docs/adr/0001-token-format.md)), all base62, secret 256 random bits, a 6-character CRC32 of everything before it:
- Virtual key: `kmn_<id:8>_<secret:43><crc:6>`
- Child key: `ek_kmn_<id:8>_<secret:43><crc:6>` (the CRC covers `ek_`)
- Admin token: `kma_<secret:43><crc:6>`
- A canary is a virtual key in the standard format. Nothing in a token reveals a canary.
- Scanner pattern: `\b(?:ek_)?kmn_[0-9A-Za-z]{8}_[0-9A-Za-z]{49}\b`.
- A bad checksum is rejected before any lookup. Only a SHA-256 hash of each secret is stored.

**Lifecycle:**
- Create, list, show, disable, enable, revoke. New keys expire after the default TTL. No key may be created or extended past the **maximum key lifetime**, and `never` is refused.
- Keys have an optional unique name, a note, and tags. The CLI accepts the name or the **key id**; the dashboard and admin API use the key id.
- **Rotation** replaces the secret and keeps the key: key id, spend, baseline, incidents, policy, and audit history stay ([ADR 0002](docs/adr/0002-rotation-keeps-the-key.md)).
  - During the **grace period**, the current and previous secrets both work. A key holds at most these two; a second rotation inside the grace ends the older one at once.
  - After the grace, the previous secret becomes a **retired secret**: its hash is kept 30 days for detection only and never authenticates.
  - Rotation is one transaction. A rotation that fails leaves the key exactly as it was.
- **Credential compromise** replaces a credential and rotates every virtual key on it in one step, then lists the apps that need new keys.
- Disabling, revoking, or rotating with grace 0 takes effect on the next request and closes the key's realtime sessions within 1 s.

**Canary keys** (M2):
- Never forward anything. Any use opens a critical incident and alerts through the priority lane.
- Plant formats: `.env`, MCP server config, shell-profile `export`, CI-secrets snippet. Each canary records where it was planted, tags, and a per-canary ignore list of addresses. Known sources never silence a canary.
- After firing, `kman canary reset` re-arms it.

## 5. Upstreams, presets, credentials, and secrets

**Presets** ([price and preset data](https://github.com/kirank55/kman/issues/22)):
- keman's 21 presets, as data in the binary, updated only by releases. Gemini gains its Live path in M4.
- `upstream add --preset` copies the preset's host, TLS, path allowlist, auth shape, and SDK env name into the upstream, recording the preset and its version. An upgrade never changes an existing upstream.
- When a newer preset differs, the status summary says so and `kman upstream sync <name>` shows the difference and applies it after a confirmation.
- No operator-defined presets in v1. A custom upstream is built with flags.

**Upstreams and credentials** have full create, update, and delete. Delete is refused while active virtual keys depend on the item. Names are unique among items not deleted.

**Secret handling** ([ADR 0003](docs/adr/0003-master-key-outside-the-data-dir.md), [secrets model](https://github.com/kirank55/kman/issues/9)):
- Credentials are encrypted with a **data key** (AES-256-GCM, bound to the credential id as AAD). The data key is stored only wrapped under the **master key**.
- **Key sources** (four adapters): file (default; also Docker and Kubernetes secret mounts), systemd-creds (`$CREDENTIALS_DIRECTORY`), env `KMAN_MASTER_KEY` (warns on every start), passphrase sealed with Argon2id (prompts on every start).
- The master key is wiped once the data key is unwrapped. kman holds one decrypted copy of each credential as a wipeable `[]byte`, wiped on rotation or delete.
- Before reading any secret: `PR_SET_DUMPABLE=0` and `RLIMIT_CORE=0` (`--allow-debug` opts out).
- `kman rekey` runs offline: a new data key, every credential re-encrypted, wrapped under the new master key, in one transaction.
- `kman keygen` prints a fresh master key and stores nothing.
- `THREAT-MODEL.md` lists what is and isn't resisted, as recorded in the secrets ticket. Root, `CAP_SYS_PTRACE`, a same-user process reading the key file, and a lost master key are not resisted.

## 6. The proxy

- Profiles: OpenAI, Anthropic, and generic HTTP (header or Basic auth). The generic profile is unmetered.
- Streaming (SSE) passes through. OpenAI-style streams get `stream_options.include_usage` injected where the preset allows.
- Inbound auth and hop-by-hop headers are stripped, and the credential is injected. No forwarding headers are added. Query strings pass through unchanged and are never logged.
- No ambient proxy. Cross-host redirects are blocked.
- The request body buffer is bounded (413 `request_too_large`).
- Client IP comes from the connection, or from `X-Forwarded-For` only through configured `trusted_proxies`.
- Invalid keys are throttled per IP in bounded memory.
- **Errors** are OpenAI-shaped JSON with an `X-Kman-Error` header carrying the code:

| Status | Codes |
|---|---|
| 401 | `invalid_key`, `key_expired`, `key_revoked` |
| 403 | `key_disabled`, `path_not_allowed`, `model_not_allowed`, `address_not_allowed`, `unmetered_request`, `unknown_model_price`, `spend_cap_reached` |
| 413 | `request_too_large` |
| 429 | `rate_limited` |
| 502 | `upstream_unavailable` |
| 503 | `store_unavailable` |

`unknown_model_price` names the model and upstream and gives the exact `kman price set` command.

## 7. Policy

A key's policy is a versioned JSON value owned by `policy`, edited through the admin API, and audited. It survives rotation. Fields:
- path allowlist and model allowlist;
- CIDR and country locks (GeoIP from `kman geoip update`, which downloads DB-IP Lite, or a supplied `.mmdb`);
- RPM limit;
- daily and monthly spend caps, and soft budgets (with or without caps);
- expiry;
- auto-disable mode: `off`, `dry-run` (the default for new keys), or `on`;
- detection overrides: any numeric threshold, the learning-period length, and turning off any rule except canary;
- realtime: `max_concurrent_sessions`, `max_session_duration`;
- child keys only: model, a lower cap, and an Origin allowlist, each only narrowing the parent.

The default LLM allowlists exclude batch, files, fine-tune, and multipart audio and image routes. An operator may add them to a key with neither a cap nor a model allowlist.

## 8. Spend and pricing

**Metering** ([spend model](https://github.com/kirank55/kman/issues/12)):
- Every key on an LLM upstream has daily and monthly spend, capped or not.
- Metered: JSON chat, responses, messages, completions, and embeddings, plus realtime (section 11).
- **Unmetered requests** (no top-level `model`, a non-JSON body, or a model with no price) are refused on keys with a cap or a model allowlist. Other keys forward them and record the cost as unknown, shown as "+ N unmetered requests", never as $0.

**Reservations:**
- Size: request bytes ÷ 4 × input price, plus the output allowance × output price. The allowance is `max_tokens` capped at the model's max output, or the max output when unset.
- Admission: committed spend + open reservations + the new reservation ≤ cap.
- Settlement: usage reported → charge it, whatever the status. Non-2xx without usage → $0. A broken stream or client disconnect → the full reservation, marked estimated.
- Settle at what the upstream reports: the response's model if it differs and has a price, the reported service tier, and provider-reported cost (xAI `cost_in_usd_ticks`, OpenRouter `usage.cost`). Overshoot is bounded to one request.

**Persistence:** spend flushes as increments every 5 s and on shutdown. Reservations are memory only. Capped keys fail closed (503 `store_unavailable`) after 60 s of failed flushes. Periods reset at 00:00 in `spend_timezone`.

**Soft budgets:** 50/80/100% of each cap by default, overridable per key, each alerting once per period. A monthly projection (spend so far + the last 7 days' daily average × days left) alerts once when it exceeds the cap or budget.

**The price table** ([ADR 0007](docs/adr/0007-price-data-ships-with-releases.md)):
- Generated at build time from LiteLLM, cross-checked against genai-prices v2 and models.dev, keeping the conservative value. Max output for preset models comes from a reviewed file. CI flags max output equal to the context window, and any changed value.
- A weekly job opens a diff PR. A merged data-only change ships as a patch release.
- Modelled: long-context tiers (reserve by the estimated input, settle by actual), per-modality prices (text, audio, cached, cache write), per-minute voice prices, per-call server-tool fees charged at settlement, and service tiers (reserve at the highest known when none is given).
- Approximated: DeepSeek at peak price always; regional uplifts by a per-upstream `price_multiplier` (default 1.0) that applies to built-in prices and install overrides, but not to per-upstream overrides or provider-reported cost.
- Model ids match exactly. The generator includes every dated id and alias the source lists.
- **Price overrides:** `kman price set|rm|list|import`, install-wide or per upstream, requiring input, output, and max output. Lookup order: per-upstream override, install override, the preset's built-in entry, the global built-in entry. An override wins until removed; `kman price list` marks overrides that hide a built-in entry.
- **Staleness:** the table carries `generated_at`. Its age shows in `kman price list`, the status summary, and `kman_price_data_age_seconds`. Past 60 days the status summary and dashboard ask for an upgrade. Age never changes enforcement.
- Refusals for an unpriced model emit a low-severity event, and the dashboard's Needs attention lists "Models without a price" with an inline fix.

## 9. Events and alerts

- Every request and every realtime session produces one **event**: key, client IP, country, ASN, upstream, model, operation, status, blocked code, latency, bytes, tokens by kind, cost (or unknown), and whether the cost is estimated. Never request or response content.
- `events` fans out to the store writer, `detect` (from M2), and the live feed (SSE and `--follow`).
- Canary and disabled-key events take a **priority lane** straight to the alert outbox.
- **Alerts** go through a persisted outbox with retries and backoff, throttled per incident.
  - Channels: Slack and signed webhook in M1; Discord and ntfy in M2. A webhook can bridge to anything else.
  - Each channel has `min_severity` (default medium) and a toggle for spend and lifecycle warnings (default on).
  - Contents: key name and id, severity, rules, evidence, the incident id, and copy-paste CLI commands. Never a secret or request content. No action links; a deep link to the incident page only if `dashboard.url` is set.
- M1 producers: the audit log, soft budgets, and credential and expiry warnings.
- Prometheus metrics are served on the admin listener at `/metrics`, behind the admin token. An opt-in `metrics_listen` serves them without auth for scrapers.

## 10. Detection and incidents

From [detection quality](https://github.com/kirank55/kman/issues/15), all in M2:
- **Baselines** per key, built during a **learning period** in which baseline rules only record.
- **Rules:** keman's 8 rules with their defaults, plus **Retired secret used** (High). Thresholds are configurable install-wide (settings) and per key (policy). Severities are fixed.
- **Incidents:** one per key, grouping its **detections**. The incident's severity is the highest of its detections.
  - Alerts on open and on escalation (higher severity or a new rule). Repeats only update a counter.
  - States open → acknowledged → resolved (confirmed, false positive, expired).
  - Quiet after 1 h without a firing (display only). An unacknowledged incident quiet for 7 days expires. Acknowledged and critical incidents never expire.
- **Response actions:** acknowledge; this was me (resolves as false positive and offers to add the sources as known sources); disable (the incident stays open); rotate, revoke, or credential compromise (each resolves as confirmed). Every containment command takes `--incident <id>`.
- **Baseline hold-out:** events in an open incident stay out of the baseline. They are merged back in on a false-positive or expired resolution and discarded on a confirmed one.
- **Known sources:** labelled CIDR, country, or ASN, install-wide or per key. They never trigger the location rules, spike rules still apply, and they never silence canaries.
- **Auto-disable:** 3 detections of medium or higher, from at least 2 rules, within 15 min, in one incident. It runs the operator's disable command as `system:detect` and sends its own alert. Re-enabling pauses auto-disable for 1 h.
- **Dry-run** results belong to the incident: when it would have disabled, and how many requests would have been blocked until resolution.
- **`kman detection test`** replays the embedded scenarios. `--key <id>` replays against a real key's baseline and overrides, read-only, safe while the server runs, and needs no master key.
- **CLI:** `kman incidents [--open] [--follow]`, `kman incident show|ack|resolve|this-was-me`, `kman known-source add|list|rm`, `kman detections --follow`.

## 11. Realtime

From [realtime](https://github.com/kirank55/kman/issues/13), [ADR 0005](docs/adr/0005-child-keys-not-provider-ephemeral-tokens.md), and [the `ek_` ticket](https://github.com/kirank55/kman/issues/19). All in M4.
- kman terminates both sides of the WebSocket (`coder/websocket`, read limit raised for audio) and runs two pumps. It parses only `type` and usage-bearing events; everything else passes through as bytes.
- **Supported:** OpenAI Realtime (including transcription and translation), OpenAI Responses WebSocket mode, GPT-Live, Gemini Live, xAI voice.
- **Key on the handshake:** an `Authorization` header; the subprotocols `openai-insecure-api-key.<key>` and `xai-client-secret.<key>` (kman echoes only `realtime`); `?key=` only for presets that document it (Gemini), stripped before forwarding. `Sec-WebSocket-Protocol` is redacted like an auth header.
- **Token-billed sessions** (OpenAI Realtime, Responses WS, Gemini) are reserved per Response: conversation input plus the model's max output, reconciled on `response.done`. A Response that doesn't fit closes the session. Overshoot is at most one Response. kman never rewrites `session.update`.
- **Time-billed sessions:** GPT-Live uses cumulative `usage.seconds` plus the 15 s WebRTC init; xAI uses wall clock. kman reserves one 60 s tick ahead and closes when the next tick won't fit. Overshoot is at most one tick.
- **At a cap:** a provider-shaped `error` event, then close with 1008 and `spend_cap_reached`.
- **Child keys:** a backend mints them on the proxy listener with its parent key. kman intercepts `POST /v1/realtime/client_secrets` and its xAI counterpart and answers in the provider's shape with an `ek_kmn_…` child key. The official OpenAI browser SDKs accept it with no opt-out flag. Defaults: 60 s to connect, one session. Children only narrow their parent, and their spend counts against the parent. kman never hands out a provider ephemeral token.
- **WebRTC**, brokered for OpenAI Realtime only: the browser POSTs its SDP to kman with a child key, kman forwards it with the real key, watches a sideband WebSocket by `call_id` for usage, and calls `hangup` at the cap.
- One event per session at close (duration, tokens, cost, close reason). Spend flushes during the session. The rate-spike rule counts session starts. Server timeouts never cut a session short of the provider's limit.

**M4 opens with the live checks** deferred from [the realtime verification task](https://github.com/kirank55/kman/issues/17). The spec assumes the left column until each is verified:

| Item | Assumed | If wrong |
|---|---|---|
| OpenAI Realtime `response.done.response.usage` | per Response | meter the delta between successive values |
| Server VAD emits `response.created` before audio | yes | check caps on the first audio delta |
| GPT-Live max duration and `session.closed` shape | unknown | the 60 s tick plus kman's own max session length |
| Gemini Live `usageMetadata` | cumulative | per-turn delta logic behind the same parser |
| Gemini `thoughtsTokenCount` | not in the response count | add it to output tokens |
| xAI usage or duration events | none | wall-clock metering (already the plan) |

## 12. Admin API and CLI

- The admin API is a versioned `/admin/v1` with typed requests and responses. Its OpenAPI document is generated from those types. Admin tokens are named; an install starts with `owner`. The dashboard cookie is never accepted on `/admin/v1/*`.
- **The CLI covers everything.** It is the admin API's one typed client, except for offline commands (`init`, `keygen`, `rekey`, `restore`, `scan`, `detection test`, `demo`) that work without a running server.
- Every read command takes `--json`. Exit codes: 0 OK, 1 error, 3 findings exist.
- **Command set:**
  - Install: `init`, `keygen`, `rekey`, `serve`, `login`, `logout`, `dashboard`, `demo`, `version [--check]`, `geoip update`, `backup`, `restore`, `prune`, `settings get|set`, plain `kman` (status summary).
  - Admin tokens: `admin-token create|list|rotate|revoke`. Rotating or revoking a token ends its dashboard sessions.
  - Upstreams and credentials: `upstream add|list|show|update|rm|sync`, `credential add|update|rm|compromise`.
  - Keys: `key create|list|show|update|disable|enable|revoke|rotate [--grace]`, `canary create|list|reset`.
  - Spend: `price set|rm|list|import`.
  - Detection (M2): `incidents`, `incident …`, `known-source …`, `detections --follow`, `detection test`.
  - Alerts: `channel add|list|test|rm`.
  - Audit: `audit [--follow]`, `audit export`.
  - Tools: `scan`, `exposure [--strict]`.
- **Update awareness:** `kman version --check` asks GitHub only when run. `update_check: true` (opt-in) checks daily and reports in the status summary, the dashboard, and `kman_update_available`. The status summary and dashboard always show how old the binary and its price table are.

## 13. Dashboard

From [dashboard look and flows](https://github.com/kirank55/kman/issues/16) and [roles](https://github.com/kirank55/kman/issues/10). All in M3.
- Server-rendered with templ and vendored htmx, with SSE for live updates (one stream per page). No Node, no CDN, assets embedded, works air-gapped.
- Served under `/ui/` on the admin listener, or on `dashboard_listen` when admin is a Unix socket.
- **Login:** a single-use link from `kman dashboard` (valid 60 s; on a headless host it prints the link and the `ssh -L` tunnel command), with a fallback form for pasting an admin token.
- **Session:** an `HttpOnly`, `SameSite=Strict` cookie, about 12 h idle and 7 d absolute.
- **Protections:** `CrossOriginProtection`, a Host allowlist, a strict CSP, `frame-ancestors 'none'`.
- **Layout (prototype variant B), light theme:** rail (Needs attention, Keys, Upstreams, Live feed, Audit log), list, detail pane. The list and pane swap with `hx-get` and `hx-push-url`, so every pane has a reloadable URL. A live update never re-renders a form being typed in.
- **Needs attention:** open and acknowledged incidents first, most severe first; then **keys at risk** with their reasons; then models without a price; then the stale-data and preset-update notes; then recently resolved. With nothing selected, the pane shows today's spend and the open-incident and active-key counts.
- **Incident pane:** header, evidence, the dry-run note, and a numbered checklist that ticks itself from state: seen it → was this you? → contain → replace the secret → did the real credential leak? → close it. Each step confirms inline and shows its exact `kman` command. Canary incidents drop "was this you?" and "contain" and instead point at the planted location.
- **Key pane:** one long page with actions, an open-incident callout, three gauges (today, month with soft-budget ticks and projection, expiry with lock), then callers (7 days), incidents, policy, known sources, and rotation history.
- **Upstreams:** the list with credentials; "This credential leaked…" runs credential compromise inline. Adding an upstream is one page with three sections (upstream, credential, first key), each saved on its own.
- **Live feed** matches `kman detections --follow` and can be paused. **Audit log** matches `kman audit --follow`.
- Every action shows its CLI equivalent. New secrets appear once, in the only modal.
- **Routes:** `/ui/`, `/ui/incidents/<id>`, `/ui/keys/<key-id>`, `/ui/upstreams/<name>`, `/ui/upstreams/new`, `/ui/feed`, `/ui/audit`.

## 14. Storage

The full table-by-table schema is in [the schema ticket](https://github.com/kirank55/kman/issues/25). The build follows it, with the flush interval corrected to 5 s (see [Consistency fixes](#consistency-fixes)). In brief:
- `modernc.org/sqlite` (pure Go), WAL, `synchronous=NORMAL`, `busy_timeout=5000`, `foreign_keys=ON`, `trusted_schema=OFF`, `auto_vacuum=INCREMENTAL`. Files `0600`, data directory `0700`.
- `STRICT` tables, Unix-millisecond integer timestamps, integer microdollars rounded up per request, prefixed random `TEXT` ids, integer incident ids, JSON as `TEXT`, one install-wide `generation` stamped per admin transaction, soft deletes for configuration rows, no tenant column.
- One migration per milestone (`0001` M1 core, `0002` detection, `0003` dashboard sessions, `0004` realtime session columns), tracked by `user_version`, forward-only.
- Spend is `spend_daily`, written only as increments. `usage_hourly` is incremented with each event batch. The audit log is append-only, enforced by a trigger.
- **Never persisted:** reservations, limiter windows, child keys, live sessions, login links, plaintext secrets. **Rebuilt at startup:** the registry snapshot, detection's rolling windows (from the last 24 h of events), and month-to-date spend.
- **Retention defaults:** events 30 days, hourly usage 400 days, daily spend, incidents, and the audit log forever, the alert outbox 30 days after delivery. `retention: keep_everything` disables pruning. `kman prune --before <date>` is always available and audited, including a guarded prune of the audit log.
- **Fixtures:** each release tag commits `internal/store/fixtures/<tag>.db` from a deterministic generator. A migration test upgrades every past fixture.

## 15. Release and supply chain

From [release, signing, and distribution](https://github.com/kirank55/kman/issues/24):
- The repo goes **public at the `v0.1.0` tag**, and stays at `kirank55/kman`. Signing identity: `https://github.com/kirank55/kman/.github/workflows/release.yml@refs/tags/<tag>`, issuer `https://token.actions.githubusercontent.com`.
- GoReleaser OSS, pinned, from one `release.yml` triggered by a maintainer-pushed `v*` tag. Five static targets, `CGO_ENABLED=0 -trimpath`, the toolchain pinned in `go.mod`, timestamps from the commit.
- cosign v3 signs `checksums.txt` once. GitHub artifact attestations cover every archive and the image digests (SLSA Build L2), with the bundle attached as `kman_<v>.intoto.jsonl`. Immutable releases.
- An SPDX 2.3 SBOM per archive and for the source, attached and attested.
- The image: `distroless/static-debian12:nonroot` pinned by digest, UID 65532, built from the released binary (amd64, arm64), signed by digest, provenance in the registry. `latest` only on stable releases.
- `install.sh` always checks SHA-256, verifies the signature with `gh` or cosign using the exact tag identity, warns loudly without a verifier, and fails closed with `KMAN_REQUIRE_SIGNATURE=1`.
- Hygiene: actions pinned by SHA, Dependabot (`gomod`, `github-actions`, `docker`, 7-day cooldown), govulncheck, weekly Scorecard, `harden-runner` in audit mode, read-only default permissions, a tag ruleset.
- **Versions:** semver. M1–M4 are `v0.1.0`–`v0.4.0`, release candidates `-rc.N`. From `v1.0.0`, a major version is needed to break the token format, the documented CLI commands and flags, admin API `v1`, config keys, or the data directory's forward migrations. Data-only patch releases skip the live suite.
- **Release checklist:**
  1. `main` green on every gate; a snapshot dry run on any PR touching release config.
  2. The live suite passes and re-records the mock transcripts (every minor and major).
  3. The fixture database for the tag is generated and committed.
  4. The tag's `CHANGELOG.md` section is final; it becomes the release notes.
  5. Push the tag.
  6. After release, a job runs the documented verify commands, rebuilds and compares checksums, and smoke-tests `install.sh` and the Homebrew formula (including that the formula's binary isn't quarantined).
  7. Benchmarks at the tag are published to `docs/benchmarks/<version>.md`.
- `SECURITY.md`: private vulnerability reporting, acknowledgement within 7 days, fix and disclosure within 90 days, the latest release supported (the latest minor after 1.0), and the exact verify commands.

## 16. Docs

All in the repo, rendered by GitHub, with no docs site:
- `README.md` (pitch and quickstart), `docs/quickstart.md`, `docs/operator-guide/` (install per platform, key sources, systemd, Docker and Compose, TLS behind a reverse proxy, alerts, upgrade, backup, and retention), `docs/sdks.md`, `THREAT-MODEL.md`, `SECURITY.md`, `CHANGELOG.md`.
- A CLI reference and an admin API reference, both generated. CI fails if either is stale.
- SDK snippets for curl, OpenAI Python and Node, and Anthropic Python and Node are pulled from e2e tests. Other presets get a generated table of env var and base URL.
- `examples/realtime-browser` (M4): a backend minting child keys and a page using `@openai/agents-realtime` with no opt-out flag. It is also an e2e test.

## 17. Testing and quality gates

The whole strategy is in [testing strategy and quality gates](https://github.com/kirank55/kman/issues/18). What every build issue relies on:
- Time is an argument; timer-driven code is tested in `testing/synctest`. Only `main` reads the clock.
- Each module is tested through its interface: table tests, fuzz targets, and property tests as listed in that ticket. Each test gets its own SQLite database.
- **E2E** on every PR: the real binary against a mock upstream whose responses are recorded transcripts, plus the pinned official Python and Node SDKs. A weekly job runs the latest SDKs. A live suite runs before each release.
- **Required gates:** `go vet`, `staticcheck`, `govulncheck`, `go mod tidy` and `templ generate` with no diff, the dependency-rule test, no package under `internal/` without tests, `-race` as its own job, a `CGO_ENABLED=0` cross-compile, the canary deadline gate (M2), the secret-leak sweep (after `v0.1.0`; the M1 security review runs it once by hand), the benchmark gate (allocations, or >10% by `benchstat`). No coverage-percentage gate.
- **OS matrix:** Linux runs everything; macOS and Windows run unit and e2e tests; systemd-creds tests run on Linux in a systemd container.
- **Dashboard tests** (M3) drive a headless browser from Go with `chromedp`, so no Node enters the toolchain.
- **Regression tests** named for keman's bugs: `TestRegression_F1_…` through `F9`.
- **Performance targets** (published at v1.0): p50 under 1 ms and p99 under 5 ms of overhead at 256 concurrent.

## Milestones: done criteria and build order

Each milestone ships as a tagged prerelease. Build issues are slices that each end in a passing test. Within a milestone, slices with the same number can run in parallel once the slices before them are done.

### M1 Contain (`v0.1.0`)

Put real keys behind kman and kill a leak.

**Build order:**
1. **Skeleton and gates:** module layout, `cmd/kman`, the toolchain pin, and every required CI gate (the dependency-rule test included), green on an empty tree.
2. **Store:** the SQLite adapter, `Do`, typed errors, migration `0001`, and the fixture generator.
2. **Vault:** the four key sources, data key, seal and unseal, startup hardening, `init`, `keygen`, `rekey`.
3. **Admin spine:** `admin` commands, named admin tokens, the audit log, `/admin/v1` with generated OpenAPI, the typed CLI client with `--json` and exit codes, the credentials file, `login`/`logout`, `serve`, and the plain `kman` summary.
4. **Registry and catalog:** the snapshot; presets as data; upstream and credential CRUD; `upstream sync`; key create, list, show, and update.
5. **Gateway tracer bullet:** token parsing with checksum, `key_secrets` lookup, forwarding through the generic profile, header stripping, redirect blocking, the bounded body buffer, the error table.
6. **Metering:** the OpenAI and Anthropic profiles with streaming usage; the price-table generator and embedded table; `pricing`; `meter` with reservations, settlement, increments, and fail-closed; caps; the unmetered rule; price overrides and the staleness line.
6. **Policy and limits:** allowlists, `limiter` with bounded memory, `trusted_proxies`.
7. **Lifecycle:** disable, enable, revoke, expiry, maximum key lifetime.
7. **Security review:** one focused review of everything `v0.1.0` ships ([#59](https://github.com/kirank55/kman/issues/59)).
8. **Ship:** the release pipeline, `install.sh`, the image and Compose example, the Homebrew formula and packages, `README`, quickstart, operator guide, `SECURITY.md`, `THREAT-MODEL.md`, the quickstart CI test, the `v0.1.0` fixture.

**Deferred past `v0.1.0`** (these sections of the spec still describe where kman is going, not what `v0.1.0` does): CIDR and country locks ([#43](https://github.com/kirank55/kman/issues/43)); key rotation and credential compromise ([#45](https://github.com/kirank55/kman/issues/45)); events, `usage_hourly` and `/metrics` ([#46](https://github.com/kirank55/kman/issues/46)); the alert outbox and channels ([#47](https://github.com/kirank55/kman/issues/47)); soft budgets and warnings ([#48](https://github.com/kirank55/kman/issues/48)); `scan` and `exposure` ([#49](https://github.com/kirank55/kman/issues/49)); `backup`, `restore`, `prune` and retention ([#50](https://github.com/kirank55/kman/issues/50)). M2 needs events and alerts, so #46 and #47 return before it starts.

**Done when:**
- The quickstart test passes on Linux and macOS, word for word from the docs.
- Pinned OpenAI and Anthropic SDKs (Python and Node, streaming and not) work through kman in e2e, with spend metered and visible per key.
- Regression tests F1 (modelless bypass), F2 (errors charge caps), F3 (counters only for capped keys), F6 (unbounded maps), and F8 (rekey keeps the data key) are green.
- E2E containment: disable, revoke and expiry stop the next request.
- `meter` property tests hold under concurrency, and the kill-mid-stream and locked-store failure tests pass.
- The security review is finished, with its critical and high findings fixed.
- `v0.1.0` is published, the post-release verification job is green, and the repo is public.

### M2 Detect (`v0.2.0`)

Notice a leak before the bill does.

**Build order:**
1. **Baselines and rules:** migration `0002`, `detect` with baselines, the learning period, the 9 rules, configurable thresholds and per-key overrides, the replay fixtures and golden files.
2. **Incidents:** incidents and detections, quiet and expiry, the response actions, `--incident` on containment commands, baseline hold-out, the incident CLI.
2. **Known sources and canaries:** `known-source` commands; canary keys with the four plant formats, the ignore list, and reset.
3. **Auto-disable:** the trigger, `system:detect` through `admin`, dry-run results on the incident, the 1 h pause.
3. **Alert channels and streams:** Discord and ntfy, per-channel `min_severity`, incident throttling, `detections --follow` and `audit --follow`, `detection test --key`.

**Done when:**
- Every replay scenario matches its golden file, and `kman detection test` replays the same embedded scenarios.
- The canary deadline gate passes: canary and disabled-key alerts arrive within 5 s under 256-way load with another channel hung.
- Regression tests F5 (dry-run never resets) and F9 (auto-disable bypassing admin) are green.
- An e2e incident flow by CLI alone: a new-country detection opens an incident, escalates, is acknowledged, and is resolved by rotation, with the timeline and audit entries correct.

### M3 Watch (`v0.3.0`)

See and respond from a browser.

**Build order:**
1. **Shell:** migration `0003`, dashboard sessions, login links, the cookie, the protections, the rail and list-and-pane layout, SSE plumbing.
2. **Needs attention and incidents:** the attention list and the incident checklist with inline confirms.
2. **Keys:** the key page with gauges and every section.
2. **Upstreams:** the upstream list and pane, add-an-upstream, credential compromise inline, the show-once modal.
3. **Feed and audit,** plus the "Models without a price" and stale-data notes.

**Done when:**
- `chromedp` tests drive the leak-response flow from an incident to resolution, and add an upstream with a first key.
- A test checks that every dashboard action's displayed CLI command is the one the admin service runs.
- Security tests pass: login links are single-use and expire, the cookie is refused on `/admin/v1/*`, cross-origin and wrong-Host requests are refused, and the CSP is present on every page.
- The dashboard builds with no Node and works with the network disabled.

### M4 Realtime (`v0.4.0`)

Contain keys used for long-lived sessions and from browsers.

**Build order:**
1. **Live checks:** the six checks in [section 11](#11-realtime), recorded in the ticket, with any fallback taken updating this spec.
2. **WebSocket core:** the terminating pump, handshake key presentation, `sessions` with `CloseKey`, migration `0004`.
3. **Adapters:** OpenAI Realtime and Responses WS (per Response), GPT-Live and xAI (ticks), Gemini Live (with its preset path).
3. **Child keys:** the mint route, client-secret interception, the `ek_kmn_` format in parsing and `scan`.
4. **WebRTC broker** for OpenAI Realtime, and `examples/realtime-browser`.

**Done when:**
- An e2e test exists for each adapter against recorded WebSocket transcripts: caps close sessions with 1008 `spend_cap_reached`, and overshoot stays within one Response or one tick.
- Revoking, disabling, or rotating with grace 0 closes open sessions within 1 s.
- The `@openai/agents-realtime` browser flow (WebSocket and WebRTC) and `openai-node`'s realtime WebSocket run in a headless browser with a child key and no opt-out flag, pinned on every PR and on latest weekly.
- `max_concurrent_sessions` and `max_session_duration` are enforced.

### v1.0.0

All four milestones done, plus:
- The live suite is green against every supported upstream.
- The published benchmarks meet p50 under 1 ms and p99 under 5 ms at 256 concurrent.
- The docs set is complete, and each verify command in `SECURITY.md` has been run against the `v1.0.0` release.
- The compatibility promise is published in `SECURITY.md` or the README.
- keman's README points to kman, and keman is archived.

## Later, and out of scope

**Later** (not in v1, listed so they aren't lost): keys limited to N uses or claim-once; temporary cap raises that expire; scheduled rotation that writes the new key to a file; a TUI as a thin admin-API client; Together and Mistral realtime STT; GPT-Live over WebRTC; metered batch, files, fine-tune, and multipart routes; a macOS keychain or Windows DPAPI key source behind the key-source seam; hosted apt/yum repositories; Apple notarization and Windows code signing; SLSA Build L3; operator-defined presets.

**Out of scope for this effort:** the team tier (multiple operators, SSO and RBAC, multi-instance, Postgres, cloud KMS, the team licence); hosted or managed kman; non-HTTP protocols.

## Consistency fixes

Contradictions found across the tickets while assembling this spec. Each is settled here.

1. **Spend flush interval.** The spend model says every 5 s; the schema ticket said every 1 s. **5 s**, as the spend model decided.
2. **`default_reservation`.** The spend model kept it for models whose max output is unknown. The price ticket since requires max output on every built-in entry and every override, and capped keys refuse unknown models, so it can never apply. **Dropped.**
3. **Key sources.** The secrets model listed five adapters, including an OS keychain with DPAPI on Windows. The keychain test dropped the macOS source, and ADR 0003 and the glossary now list four sources with no keychain at all. **Four sources.** DPAPI moves to Later behind the same seam. Windows installs use a key file or a passphrase.
4. **Keychain tests in the OS matrix.** The testing ticket ran keychain tests on macOS. **Removed**, since there is no keychain source.
5. **Child-key prefix.** The realtime ticket and the original ADR 0005 showed child keys as `kmn_`. **`ek_kmn_`**, per the `ek_` ticket and the amended ADRs 0001 and 0005.
6. **Homebrew.** Onboarding made a Homebrew cask first-class. The release ticket chose no notarization, so it's a **best-effort formula**.
7. **The release checklist** gains the fixture-database step from the schema ticket.
8. **Needs attention** gains "Models without a price", stale data, and preset updates, from the price ticket. The dashboard ticket predates them.

The amendments already made to ADRs 0001 (child-key prefix), 0002 (retired secrets), 0003 (no keychain), 0004 (child keys as the exception), and 0005 (the `ek_` check) agree with this spec.

## Handoff

- **Build issues are filed per milestone, when the milestone starts.** M1 first. Each later milestone is sliced when the one before it ships, so what M1 teaches can reshape it.
- Each milestone gets a parent issue ("M1 Contain", …). Its build issues are sub-issues, one per slice in the build order, with native blocking edges matching the order, labelled `ready-for-agent`.
- **Acceptance criteria** in every build issue name the interface tests it adds, the regression tests it turns green, and the gates it must pass. Each issue is built test-first.
- Changes to this spec after handoff go through a PR that edits `SPEC.md`, plus an ADR when the change meets the ADR bar.
- The wayfinder map is closed once this spec is committed. The research and prototype branches stay as primary sources and are never merged.
