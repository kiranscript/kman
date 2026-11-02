# keman beta post-mortem: keep, fix, drop

Ticket: kirank55/kman#2. Question: what did keman's beta get right, what did it get wrong, and what should kman keep, fix, or drop?

- **Source audited:** `kirank55/keman` at commit `3dd4a09`. Every `file:line` below is pinned to that commit, relative to the repo root.
- **Specs it was checked against:** `beta.md`, `docs/ASSUMPTIONS.md`, `docs/adr/0001`–`0004`, `docs/THREAT-MODEL.md`, `SECURITY.md`, `CHANGELOG.md`, `GLOSSARY.md`, `benchmarks/RESULTS.md`.
- **How it was checked:** I read the code, then ran the commands listed below. Three suspected bugs were confirmed with throwaway tests run on a copy of the repo, not on the reference clone.

## What was run

| Check | Result |
| --- | --- |
| `CGO_ENABLED=0 go test ./...` (Go 1.27.3) | All packages pass. |
| `CGO_ENABLED=0 go vet ./...` | Clean. |
| `go test -race` on proxy, app, state, admin, detect (CGO on, on the copy) | Clean. CI never runs `-race`, because CI uses `CGO_ENABLED=0` (`.github/workflows/ci.yml:16`). |
| `go test -cover ./internal/...` | Low on the packages that matter most: state 16.7%, store 28.8%, cli 32.3%, admin 43.4%, alert 40.4%, price 47.1%. No tests at all for pipeline, geoip, metrics, profile, model. |
| `go mod tidy` drift | `github.com/muesli/termenv` is imported directly but listed as `// indirect` (`go.mod:35`). `-mod=mod` rewrites the file. Minor. |
| Scratch tests (deleted afterwards) | Confirmed bugs F1, F2 and F3 below. |

## Keep

These held up under audit and are worth carrying into kman nearly as they are.

| # | Keep | Evidence |
| --- | --- | --- |
| K1 | **Hot path reads only memory.** Admin writes go to SQLite first, then to memory, and memory is left alone when the commit fails. Revocation therefore applies to the next request. | `internal/state/state.go:1-4`, `docs/adr/0004-admin-api-is-the-mutation-path.md`, `internal/admin/admin.go:316-330` |
| K2 | **The CLI never writes SQLite directly.** Only `init` and `serve` open the database. Every other command calls the admin API. | `internal/cli/cli.go:225,262,272,365`, `internal/cli/server.go:42` (the only `app.Open`/`app.Init` call sites) |
| K3 | **The TUI is a pure admin-API client.** `internal/tui` imports only `adminclient`, `expiry`, `model`, `money`, `preset`, not store or state. That is the right seam; only the size is the problem (see S1). | `go list` import graph |
| K4 | **Virtual key format.** `kmn_<8 base62 id>_<43 base62 secret>`. The secret is stored as SHA-256 and compared in constant time, and unknown ids are compared against a dummy hash. | `internal/keyfmt/key.go:16-40`, `internal/state/state.go:222-235` |
| K5 | **Envelope encryption with the credential id as AAD,** so ciphertext rows cannot be swapped. | `internal/app/app.go:320`, `docs/adr/0001-pure-go-sqlite-and-single-dek.md` |
| K6 | **Director, not Rewrite.** The raw query is preserved, `X-Forwarded-For` is set to nil so Go adds nothing, and the carrier header is stripped. | `internal/proxy/proxy.go:256-275`, `docs/adr/0002-director-not-rewrite.md` |
| K7 | **Egress hardening.** `Proxy: nil` (ignores `HTTP_PROXY`), TLS 1.2 minimum, cross-host redirects become 502, and a corrupt upstream allowlist fails closed instead of falling back to the built-in list. | `internal/proxy/proxy.go:454-467,543-546,583-618` |
| K8 | **Integer microdollars and an injectable clock throughout.** | `internal/money/money.go`, `internal/clock/clock.go`, `beta.md:17-18` |
| K9 | **Reservation includes the input estimate** even when `max_tokens` is absent, so a large prompt cannot slip under a small cap. | `docs/adr/0003-reservation-includes-input-estimate.md`, `internal/proxy/proxy.go:234` |
| K10 | **Spend fail-closed timer:** 60 s of failed flushes returns 503 for capped keys only. | `internal/state/state.go:311-335`, `internal/app/app.go:551-567`, e2e `internal/app/e2e_test.go:193` |
| K11 | **Canary behaviour.** The 401 is shaped like the provider's own error, there is no `X-Keman-Error` header, and the canary check runs before status and expiry checks. | `internal/proxy/proxy.go:136-142,420-432`, `docs/ASSUMPTIONS.md:29` |
| K12 | **Fuzz targets** for key parsing, header sanitizing and path matching, run in CI. Path traversal (`..`, `..;`, backslash) is refused. | `Makefile:9-12`, `.github/workflows/ci.yml:21-23`, `CHANGELOG.md:149` |
| K13 | **Detection replay harness:** `keman detection test` plus a fixture loader and the injectable clock. This is the right way to test behaviour rules. | `internal/cli/detection.go`, `internal/detect/engine.go:231-237,578+` |
| K14 | **Product surface:** presets, `keman scan`, `keman exposure`, `key rotate` with grace, and a default key TTL. These are cheap and directly serve leak containment. | `beta.md:26,37-38`, `internal/preset/preset.go`, `internal/scan/scan.go`, `internal/exposure/exposure.go` |
| K15 | **Honest docs.** `ASSUMPTIONS.md` and `THREAT-MODEL.md` state their limits openly: memory dumps, master.key next to the data, the 5 s crash window, and what was not finished. Keep that habit. | `docs/THREAT-MODEL.md:50-59`, `docs/ASSUMPTIONS.md:57` |

## Fix: correctness and security bugs

Ranked by severity.

**F1. Spend caps and model allowlists are bypassed by any request body without a top-level `model`. CONFIRMED.**
- **Cause:** `usage.Prepare` returns empty metadata for non-JSON bodies and for JSON with no `model` key (`internal/usage/usage.go:62-71`). The model allowlist is only checked `if ... meta.HasModel` (`internal/proxy/proxy.go:199`). Reservation only happens `if ... meta.HasModel` (`internal/proxy/proxy.go:227`).
- **Why it matters:** the built-in allowlists include endpoints whose spend is invisible to the proxy:
  - Anthropic `POST /v1/messages/batches` (`internal/profile/profile.go:58`). Its model lives in `requests[].params.model`.
  - OpenAI `/v1/batches` and `/v1/fine_tuning/jobs` (`profile.go:46-52`).
  - Multipart endpoints: `/v1/audio/transcriptions` and `/v1/images/edits` (`profile.go:34-37`).
- **Scratch test:** a key with `ModelAllowlist: ["claude-sonnet-4-5"]` and a 1-microdollar daily cap sent a batch for `claude-opus-5` with `max_tokens: 64000`. Result: 200, forwarded upstream, committed and open spend both 0.
- **Uncertain:** the multipart endpoints were not tested, but they follow the same code path.
- **Fix in kman:**
  - Fail closed when a key has a cap or a model allowlist and the request's model cannot be determined.
  - Or remove batch, fine-tune and multipart routes from the default LLM allowlist until they are metered.

**F2. Upstream errors charge the full reservation against the cap. CONFIRMED.**
- **Cause:** when the response carries no usage, `onClose` charges the reservation (`internal/proxy/proxy.go:298-301`, also `:345-352`). Only redirects and keman-blocked responses are exempt (`:295`). A 429, 5xx or 401 from upstream carries no usage.
- **Scratch test:** an upstream 429 on a `gpt-4o-mini` request with `max_tokens: 1000000` charged 600,002 µ$ to the daily cap.
- **Effect:** SDKs retry 429s, so an upstream outage or rate limit can drain a cap with no tokens used, and a broken real key (repeated 401s) does the same.
- **Spec status:** this matches `beta.md:114` literally, so it is a spec defect rather than a coding slip.
- **Fix in kman:** release the reservation, or charge only the input estimate, when the status is not 2xx.

**F3. Spend counters only exist for keys that already have a cap. CONFIRMED.**
- **Cause:** `Reconcile` is skipped `if !hasCap` (`internal/proxy/proxy.go:292`, `:345`).
- **Scratch test:** an uncapped key spent about $0.75 and its day and month counters stayed at 0.
- **Effect:** adding a daily cap at 15:00 ignores everything spent since 00:00. Any "spend so far" UI must fall back to summing events.
- **Spec status:** `beta.md:80` defines `spend_counter` as committed spend per key, with no mention of caps, so this was implemented differently from the spec.
- **Fix:** always reconcile when the cost is known.

**F4. Key rotation resets the cap budget and the baseline, and can leave an unrestricted key behind.**
- **Budget reset:** spend is keyed by virtual-key id (`internal/state/state.go:40-43,353-356`). `RotateKey` copies the policy but neither spend nor baseline (`internal/admin/admin.go:400-416`). So a rotation:
  - gives the key a fresh daily and monthly budget;
  - restarts the 7-day / 500-request learning period, during which the spike and new-location rules only record (`internal/detect/engine.go:278`).
- **Unrestricted key:** the fresh key is inserted as active first (`admin.go:400`, via `CreateKey` at `:286-290`). The policy is copied afterwards (`:404-416`). If `UpsertPolicy` fails, the function returns an error but leaves an active key with no caps or allowlists. The store has no transaction spanning operations.
- **Fix:**
  - Treat rotation as one store transaction.
  - Carry over spend and baseline, or key them by a stable lineage id.

**F5. Dry-run "armed" never resets.**
- `st.dryArmed` is set once (`internal/detect/engine.go:405-409`). After that every request counts as would-have-blocked (`:391-394`).
- No code path clears it: a grep for `dryArmed = false` and for an admin route finds nothing.
- The value is persisted per event (`internal/store/store.go:822`), so the dry-run report degrades permanently after the first trip.

**F6. Unbounded in-memory maps.** None of these is ever deleted from (grep for `delete(s.invalid` and `delete(s.keyRate` finds nothing):
- `invalid` (per client IP) and `keyRate` (per key) (`internal/state/state.go:260-286`).
- `spend` (every past period) (`state.go:404-418`).
- `LoadSpend` loads all history at boot (`internal/store/store.go:501-516`).
- Retention prunes only events and detections, not `incidents` or `spend_counters` (`store.go:992-998`).

Invalid-key spray from many IPv6 addresses therefore grows memory without limit.

**F7. The detection and alert pipeline is serial and lossy in ways the canary SLA does not survive.**
- **One worker does everything per event,** in sequence, on a single SQLite connection (`internal/store/store.go:170`): insert the event, save the baseline (about two writes in a transaction, re-marshalling all countries and ASNs every time), then insert detections and incidents (`internal/app/app.go:467-504`).
- **Alerts are sent inline** with a 4 s timeout per destination and no retry (`internal/alert/alert.go:57-93`, `app.go:505-526`). One slow webhook stalls detection for every key.
- **Canaries can be lost:**
  - If `InsertEvent` fails, detection is skipped entirely, including for canaries (`app.go:479-483`).
  - Canary events share the drop-oldest queue (`internal/pipeline/queue.go:37-41`).
  - "Canary alert within 5 s" (`beta.md:227`) is only tested on an idle system (`internal/app/e2e_test.go:140`).
- **Fix:**
  - Give canary and disabled-key alerts a priority path that does not depend on SQLite.
  - Use an outbox with retries for alert delivery.

**F8. Rekey does not rotate the data key.**
- **What it does:** `Rekey` re-wraps the same DEK (`internal/admin/admin.go:590-611`, `docs/ASSUMPTIONS.md:5`). If the master key and any database backup leak together, the attacker has the DEK forever, and every future credential encrypted under it stays readable.
- **Operator footguns:**
  - Rekey does not update `master_key_file`. It only prints a warning (`internal/cli/cli.go:497`), so the next restart fails until someone swaps the file.
  - Both master keys travel in an HTTP body to the admin API (`cli.go:489-492`).
- **Fix in kman:** make rekey also re-encrypt every credential under a fresh DEK, which is cheap at single-instance scale, and write the new key file atomically.

**F9. Auto-disable bypasses the admin service, with a read-modify-write race.**
- The worker writes the store, then calls `state.Key` followed by `PutKey(cur)` (`internal/app/app.go:533-546`). Admin `SetStatus` does the same non-atomic Key/PutKey (`internal/admin/admin.go:297-327`).
- A concurrent admin enable and auto-disable can lose an update in memory.
- This also contradicts ADR 0004's "admin API is the only mutation path".
- **Uncertain:** the race is inferred from the code, not reproduced. The race detector did not flag it in the existing tests.

**F10. Smaller items.**
- The transport cache is keyed by upstream id and never invalidated (`internal/proxy/proxy.go:434-469`). It is harmless today only because upstreams cannot be updated (there is no PUT route in `admin.go:819-1042`). It becomes a bug the day update is added.
- Every *valid* virtual-key secret presented is copied in plaintext into the log redactor for the life of the process (`internal/proxy/proxy.go:101-103`, `internal/redact/redact.go:18-38`). That is a second plaintext cache the threat model does not mention (`docs/THREAT-MODEL.md:52` only mentions credentials).
- LLM bodies are buffered up to 32 MiB per request (`proxy.go:162-178`, `docs/ASSUMPTIONS.md:47`), with no concurrency bound. Any key holder can exhaust memory.
- Detection thresholds are hardcoded. `app.go:242` uses `detect.DefaultConfig()` and `config.Config` has no detection fields (`internal/config/config.go:19-41`), yet `beta.md:179` says "defaults, configurable".

## Fix: weak defaults

| # | Default | Evidence | kman should |
| --- | --- | --- | --- |
| W1 | `master.key` is written beside the config. In Docker, config, `master.key` and `keman.db` all share `/home/nonroot`, so one volume snapshot holds everything. | `beta.md:148`, `internal/cli/cli.go:290-292`, `Dockerfile:21-24`, `docs/THREAT-MODEL.md:21,57`, `SECURITY.md:84` | Refuse, or loudly warn, when the key file sits in the database's directory. Prefer env/secret-mount, an OS keyring, or a separate dir by default. |
| W2 | Plaintext credentials are decrypted at boot and held for the process lifetime as immutable Go `string`s, which cannot be zeroed. | `internal/app/app.go:320-331`, `internal/model/model.go:52`, `SECURITY.md:87` | Keep ciphertext in memory and decrypt per request into a `[]byte` (AES-GCM is microseconds), or at least use `[]byte` plus a short-TTL cache. Document the residual risk. |
| W3 | One shared admin token, and every audit entry gets `actor = "admin"`. | `internal/admin/admin.go:816`, `docs/ASSUMPTIONS.md:7` | Use named operator tokens from day one, even with one operator. The audit actor becomes meaningful and the team tier gets its seam. |
| W4 | The proxy listens on `0.0.0.0:8080` and cleartext h2c, with no TLS option. | `beta.md:14`, `internal/app/app.go:275-281`, `docs/ASSUMPTIONS.md:51` | Default to `127.0.0.1`, or document a sidecar pattern. Offer optional TLS. |
| W5 | Running `keman` with no command starts the full proxy *inside the TUI process* and stops it on exit, so closing the terminal cuts off the apps. | `internal/cli/server.go:30-58`, `docs/THREAT-MODEL.md:67` | Make the TUI client-only and have it tell the user to start `serve`, or make the embedded server explicit and opt-in. |
| W6 | The admin Unix socket is created with umask permissions. `listen` does not `chmod`. | `internal/app/app.go:640-647` | `chmod 0600` the socket. Low severity, because the token is still required. |

## Fix: structure

**S1. God-files.**

| File | Lines | What is wrong |
| --- | --- | --- |
| `internal/tui/view.go` | 1924 | God-file. |
| `internal/tui/update.go` | 1891 | One `model` type with about 93 methods; `Update` alone is 235 lines (`update.go:157`). The `model` struct has about 91 lines of fields (`internal/tui/types.go:256`). Every screen and form shares one state bag. |
| `internal/cli/cli.go` | 1257 | One `cmd` type with 40 functions, plus a hand-rolled flag parser (`cli.go:1106-1165`) and a second admin HTTP client (`cli.go:1090`) duplicating `internal/adminclient`. |
| `internal/admin/admin.go` | 1186 | Service logic and a 239-line `ServeHTTP` that routes by `strings.HasPrefix`/`HasSuffix` (`admin.go:810-1049`). |
| `internal/store/store.go` | 1062 | Every table in one file. |
| `internal/proxy/proxy.go` | 670 | `ServeHTTP` is 272 lines (`proxy.go:83-355`) holding auth, policy, reservation, forwarding and reconcile as closures over shared mutable `evBase`/`settled`. |

kman should split the TUI per screen, use `net/http` 1.22 pattern routing, and make proxy steps separate functions with a typed request context.

**S2. Shallow modules and no contracts.**
- `admin.Service` depends on concrete `*store.Store` and `*state.State` (`internal/admin/admin.go:51-63`). There is no store interface and no transaction type, so multi-step operations cannot be atomic (F4).
- Duplicate-name detection matches SQLite error text (`strings.Contains(..., "unique")`, `admin.go:798`).
- Admin responses are built as `map[string]any` (`admin.go:1060-1126`), and the TUI keeps a hand-written mirror in `internal/tui/wire.go` (164 lines) plus `types.go`.
- Rotation state is encoded in the key's *name* (`RotatedSuffix = " (rotated)"`, `admin.go:365`, read back at `admin.go:1089`).

**S3. Schema migrations are ad hoc.**
- `CREATE TABLE IF NOT EXISTS` is followed by a column-adding list (`internal/store/store.go:183-203`).
- There is no `user_version` and no ordered migrations.
- That is enough for one column. It is not enough for a schema that will add operators and tenants.

**S4. One global lock.**
- `state.State` puts every map behind one `sync.Mutex` (`internal/state/state.go:20-38`).
- Every request takes it several times: `Match`, `Credential`, `Upstream`, `Policy`, `AllowKey`, `SpendDegraded`, `Reserve`, `Reconcile`.
- The redactor's `Add` takes a write lock on every request (`proxy.go:101-111`).
- The published benchmark runs 200 *sequential* requests with no queue, metrics or store attached (`internal/proxy/proxy_test.go:86-93`, `internal/proxy/bench_test.go`, `benchmarks/RESULTS.md`). The p99 target (`beta.md:232`) is therefore met only for an uncontended single client.

## Seams that make a later team tier hard

These matter only if kman plans multi-operator, Postgres or multi-instance. All of them are cheap to set up now and expensive to retrofit.

1. **Absolute-value spend flush.** `SaveSpend` writes `micro = excluded.micro` (`internal/store/store.go:518-531`). Two instances overwrite each other's counters. kman should flush deltas (`micro = micro + ?`) and keep the reservation and cap logic behind an interface that a shared counter (Redis or Postgres) can implement.
2. **Memory is authoritative for keys and policies.** Every admin write updates only *this* process's memory (`docs/adr/0004-admin-api-is-the-mutation-path.md`). A second instance would serve a revoked key until restart. kman needs a change feed or version-polling seam (for example, a `generation` counter on each row) even while it stays single-instance.
3. **Detection state lives in process,** rebuilt at boot from 7 days of events (`internal/app/app.go:377-397`). That gives slow boots as events grow, and per-instance baselines once there are several instances.
4. **No store interface,** SQLite-specific error matching (`admin.go:798`), and SQLite date strings. Postgres would mean a rewrite rather than a second implementation.
5. **No operator identity** (W3) and no tenant column anywhere in the schema (`internal/store/store.go:18-160`).
6. **One DEK per install,** an explicit decision (`docs/adr/0001-pure-go-sqlite-and-single-dek.md`). That is fine for now, but credentials should carry a `dek_id` so per-tenant keys can be added later. A `key_version` column already exists but means nothing while there is a single DEK.

## Spec items not implemented, or implemented differently

| Spec item | Status | Evidence |
| --- | --- | --- |
| Detection thresholds "configurable" (`beta.md:179`) | Not implemented. Hardcoded. | `internal/app/app.go:242`, `internal/config/config.go:19-41` |
| `spend_counter` = committed spend per key per day and month (`beta.md:80`) | Implemented differently: only for capped keys. | F3 |
| Model allowlist and spend cap apply on LLM profiles (`beta.md:105,113`) | Bypassed for model-less bodies. | F1 |
| Canary alert within 5 s (`beta.md:227`) | Met only on an idle system. No priority path. | F7 |
| Dry-run "records how many requests would have been blocked" (`beta.md:206`) | Implemented, but the count never resets. | F5 |
| Official Python and Node SDK streaming runs (`beta.md:225`) | Not done. A mock transcript stands in. | `docs/ASSUMPTIONS.md:57`, `internal/proxy/proxy_test.go:372` |
| Signed release with SBOM (`beta.md:234`) | Scaffolded only. Nothing published. | `SECURITY.md:115`, `docs/ASSUMPTIONS.md:57` |
| Stranger quickstart under 10 min (`beta.md:233`) | Not done. | `docs/ASSUMPTIONS.md:57` |
| p99 overhead under 10 ms, published (`beta.md:232`) | Published, but only for an uncontended loopback run. | S4, `benchmarks/RESULTS.md` |
| Retention: events 14 d, detections 90 d (`beta.md:158`) | Done, but incidents and spend counters grow without limit. | F6 |
| Error table (`beta.md:135-142`) | Extended with `request_too_large` (413) and `upstream_unavailable` (502). Documented. | `docs/ASSUMPTIONS.md:47` |

## Spec gaps: decisions left implicit or deferred to ASSUMPTIONS.md

kman's spec should decide these up front:

- **Upstream non-2xx responses:** what spend to charge (F2). The spec never separates "no usage because of an error" from "no usage because of a protocol quirk".
- **Endpoints with no top-level `model`:** how batches, files and multipart requests interact with caps and allowlists (F1).
- **Rotation lineage:** whether spend, baseline and incidents follow the key across a rotation (F4). Also, rotation state should not live in the name.
- **Lifecycle of upstreams and credentials:** there is no update or delete route (`internal/admin/admin.go:819-1042`), so a typo in an upstream host is permanent. Nothing in beta.md says whether that is intended.
- **Alert delivery:** retries, ordering, a dead-letter queue, and the canary SLA under load (F7).
- **How dry-run resets** (F5).
- **Rekey semantics:** re-wrap only (`docs/ASSUMPTIONS.md:5`) versus true re-encryption (F8).
- **Where `master.key` lives by default:** beta.md itself mandates "next to the config" (`beta.md:148`), so this weak default came from the spec, not the code (W1).
- **What may be dropped:** the CLI needs a running server for every write (`docs/ASSUMPTIONS.md:9`); open reservations are lost on a crash (`:25`); the request that trips auto-disable has already been forwarded (`:41`). These are reasonable, but kman should state them in the spec rather than in a post-hoc list.
- **Memory bounds:** per-IP and per-key limiter state (F6) and concurrent body buffering (F10).

## Drop

| # | Drop | Why |
| --- | --- | --- |
| D1 | **The TUI as the default command that embeds the server.** | Two operating modes (embedded and attached) and a 3.8K-line UI are the biggest maintenance cost in the repo (S1). Embedding couples the proxy's lifetime to a terminal (W5). Ship CLI plus admin API first. Add a slim TUI later as a pure client, if at all. |
| D2 | **The `protocol` field and the "other protocols later" framing.** | It is a never-used column (`beta.md:7`). YAGNI. Add it with a migration when it is needed. |
| D3 | **The OpenAI batch, fine-tuning and file routes and the Anthropic batch route in the default LLM allowlists**, until they can be metered. | They are the bypass in F1 (`internal/profile/profile.go:42-58`). |
| D4 | **Hand-rolled flag parsing, the duplicate admin HTTP client in `cli.go`, and `map[string]any` API views.** | Replace them with the stdlib or a small CLI library, one typed client, and typed DTOs shared with the TUI (S1, S2). |
| D5 | **Rotation state encoded in the key name** (`" (rotated)"`). | Replace it with a `replaced_by` or `lineage_id` column (S2, F4). |
| D6 | **`docs/archive/PLAN.md` as a living artifact.** | beta.md already overrides it (`beta.md:3`). kman should keep one spec plus ADRs, and should fold the ASSUMPTIONS.md entries into ADRs or the spec as they are decided. |

## Uncertain, or not verified

- **F1 for multipart endpoints:** inferred from the code path (`usage.Prepare` returns early on non-JSON). Only the Anthropic batch case was run.
- **F9 lost-update race:** inferred. Not reproduced, and `-race` on the existing tests is clean.
- **Throughput limits of the single-connection SQLite worker (F7, S4):** no load test was run. The claim rests on the design: about 3 to 4 writes per request through `SetMaxOpenConns(1)`.
- **Docs:** I did not read `docs/archive/PLAN.md` in full. beta.md marks it as non-authoritative, so findings are measured against beta.md.
