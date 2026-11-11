# Keys, policy and spend

A **virtual key** is what an app holds in place of a real API key. It names one upstream (and so one credential), it expires, and it carries a **policy** that says what it may do. This page says what each part does in v0.1.0, including the places where the behaviour is a choice that may change before v1.0 (marked **Today**).

## The life of a key

```sh
kman key create my-app --upstream openai          # prints the key once
kman key list
kman key show my-app                              # status, expiry, policy, spend
kman key update my-app --note "billing service" --tag prod
kman key disable my-app                           # pause: refused from the next request
kman key enable my-app
kman key revoke my-app                            # end it, for good
```

- **Creation.** The secret is shown once and only its hash is stored. A key may be named (names are unique among keys that are not revoked) and may carry a note of up to 1024 bytes and up to 32 tags.
- **Expiry.** A new key expires after the `default_key_ttl` setting (90 days) or the `--ttl` you give (`90d`, `36h`, `1h30m`). `kman key update my-app --ttl 30d` sets a new expiry that long from now. No key may be created or extended past the `max_key_lifetime` setting (365 days), counted **from the key's creation**, so editing a key can't keep it alive for ever: an app must move to a new key before then. `never` is refused. An expired key is refused with `key_expired` from its first request after the expiry; expiry needs no command.
- **Disable and enable** pause a key and resume it. **Revoke** is final: a revoked key can never be enabled, disabled or updated, and its name is free again.
- All of them are audited (`kman audit`) with the admin token that made the change, and take effect at the key's **next request**, with no restart.

**Today:**

- A request already running when its key is disabled or revoked runs to the end. A long stream outlives the kill. Only the next request is refused.
- A command that changes nothing (disabling a disabled key, revoking a revoked one) succeeds, records nothing, and says so.
- Enabling a key does not extend its expiry: a key enabled after its expiry still answers `key_expired`.
- Revoke asks for no confirmation, since it is the emergency button.
- After a revoke the CLI can no longer find the key by name (the name may belong to a new key); use its id, which `kman key list` shows.
- A key's credential can't be changed by an update: create a new key on the other credential.

## The policy

A key's policy is a JSON value, empty by default, that replaces the old one whenever it is set. Every command below sets the whole policy; run them one after another and the last one wins. kman refuses a field it does not know.

<!-- policy:begin -->

```sh
kman key update my-app --policy '{"allowed_models":["gpt-4o-mini","gpt-4.1*"],"rpm_limit":60}'
kman key update my-app --policy '{"allowed_paths":["POST /v1/chat/completions","POST /v1/embeddings"]}'
kman key update my-app --policy '{"monthly_cap_micro":50000000}'
kman key update my-app --policy '{}'
kman key update my-app --policy '{"daily_cap_micro":5000000,"monthly_cap_micro":50000000,"rpm_limit":120}'
kman key update my-app --policy '{"monthly_cap_micro":50000000,"monthly_budget_micro":40000000,"soft_budget_percents":[50,90,100]}'
```

<!-- policy:end -->

| Field | Meaning |
|---|---|
| `daily_cap_micro` | The most the key may spend in a day, in microdollars (1 000 000 = $1). Above 0. |
| `monthly_cap_micro` | The same for a month. Above 0, and at least the daily cap. |
| `daily_budget_micro` | A soft budget for a day: a warning level that never refuses a request. Above 0, and at most the daily cap if there is one. |
| `monthly_budget_micro` | The same for a month. At least the daily budget, and at most the monthly cap if there is one. |
| `soft_budget_percents` | The percentages of a cap or budget that warn, rising, from 1 to 1000, at most 10. Leave it out to use the install's `soft_budget_defaults`. |
| `allowed_models` | Model ids the key may ask for: exact, or a prefix ending in `*` (`gpt-4.1*`). |
| `allowed_paths` | Rules the key's requests must match, such as `POST /v1/chat/completions`. |
| `allowed_cidrs` | Networks the key may be used from, such as `203.0.113.0/24`; a bare address is a network of one. |
| `allowed_countries` | Countries the key may be used from, as ISO 3166-1 codes (`US`). Needs a GeoIP database. |
| `rpm_limit` | The most requests per minute. |

A cap or budget of `0` or less is refused, never read as "no cap".

### Soft budgets and warnings

A cap refuses. A soft budget only tells you. kman warns through your [alert channels](operator-guide/alerts.md) when a key's spend reaches **50, 80 and 100 percent** of each of its caps (or `daily_budget_micro` and `monthly_budget_micro`, which count in place of the cap for that period, and work with no cap at all). Each threshold warns **once per period**: once per day for a daily limit, once per month for a monthly one. Spend that jumps over several thresholds between two checks is one alert, for the highest. Change the percentages for every key that sets none with `kman settings set soft_budget_defaults 25,50,90` (or `none`), and for one key with `soft_budget_percents`.

A **projection** warns once per month when the month is on course to pass its monthly cap or budget: the spend so far this month, plus the daily average of the last 7 full days for each day left. It says nothing once the month has passed the limit, since the 100 percent warning has said so. A soft budget never refuses anything, so unlike a cap it does not require every request to be one kman can meter.

Spend and expiry are checked when kman starts and then every minute, on what the meter has saved (every 5 seconds), in the `spend_timezone` read when kman started. A warning goes only to channels with `--lifecycle-alerts` on, at medium severity below 100 percent and high at 100 percent. kman [also warns](operator-guide/alerts.md#warnings) before a key expires.

### Address locks

`allowed_cidrs` and `allowed_countries` lock a key to where it may be used from. A request from anywhere else is refused with `403 address_not_allowed`, before its body is read. With both, the address must satisfy both. A country lock is refused when you set it unless kman has a GeoIP country database, and an address the database does not know fails the lock. See [Address locks and GeoIP](operator-guide/geoip.md).

### Paths

A rule is `METHOD /path`, with the method in capitals or `*`. A `*` segment matches one segment, and a last `**` segment matches the rest (one or more segments). `* /**` allows everything. Paths are matched case-sensitively, and methods exactly.

Every upstream has a path list too (the presets carry theirs, and `kman upstream show` prints it), and it applies to **every key on that upstream**. A key's `allowed_paths` **replaces** the upstream's list for that key: it can narrow it, or, on a key with no model allowlist, allow a route the upstream's default list leaves out (the OpenAI preset leaves out batches, files and fine-tuning). On a key **with** a model allowlist the request must match **both** lists, so the allowlist can't be bypassed through a route kman reads no model from.

**Today:** kman reads the path as it was sent. A path with a `.` or `..` segment, an empty segment (`//`), or a segment that decodes to something holding `/`, `\` or a control character matches nothing, and is refused with `path_not_allowed` instead of being forwarded. TRACE, CONNECT and `OPTIONS *` are always refused.

### Models

A request on a key with a model allowlist is checked against the `model` field of its JSON body. A request whose model kman can't be sure of — no body, a body that isn't JSON or is compressed, a repeated `model`, a differently-cased `Model`, a `model` that isn't a string — is refused with `unmetered_request`.

**Today:** that includes `GET /v1/models`, the call that lists models, so a key with a model allowlist can't list them. A model allowlist on a generic-profile upstream refuses every request with no JSON `model`.

### Rate limits

`rpm_limit` is a sliding one-minute window per key. Over it, a request is answered `429 rate_limited` with a `Retry-After` header. A request refused for any other reason doesn't count against the limit. Limiter memory is bounded: 10 000 keys and 10 000 addresses, least recently used out first.

Separately, an address that presents more than 30 **invalid** keys in a minute (a bad format, an unknown id, a wrong secret) is refused with `429 rate_limited` until the minute passes. Revoked, expired and disabled keys are not counted, and an IPv6 network of 64 bits counts as one address. **Today:** the throttle refuses *good* keys from a throttled address too, for up to a minute, which is what stops guessing at the speed of the server; and the three limits above are constants, with no setting. kman reads an address from the connection. Behind a reverse proxy, set `trusted_proxies` in `kman.yaml` (see [TLS and reverse proxies](operator-guide/tls.md)), or every request seems to come from the proxy.

## Spend and caps

Every key on an **OpenAI or Anthropic profile** upstream is metered, capped or not: kman reads each response's usage as it passes (tokens by kind, the model, the service tier, server-tool calls, and any cost the provider reports) and prices it from the [built-in price table](price-data.md) or your [overrides](price-data.md#price-overrides). `kman key show` prints the spend for today and this month beside the caps, and `kman key list --json` carries it for every key. A **generic** upstream (Stripe, GitHub, anything you add by hand without a profile) is not metered, and a key with a cap or a model allowlist on one is refused every request with `unmetered_request`.

A day and a month begin at 00:00 in the `spend_timezone` setting (UTC). **Today:** the timezone is read when kman starts, so `kman settings set spend_timezone …` takes effect at the next restart.

**How a request is admitted.** Before it is forwarded, kman reserves its likely cost: the body's bytes ÷ 4 at the model's input price, plus the output allowance (the largest of `max_tokens`, `max_completion_tokens` and `max_output_tokens`, else the model's maximum) at the output price, at the highest service tier it knows when the request names none. If the committed spend, the open reservations and this one would pass a cap, the request is refused with `spend_cap_reached`. A key therefore goes over its cap by at most the size of one request. When the response arrives it is charged what it reports, and the reservation is released.

**What is charged:**

| Response | Charge |
|---|---|
| Reports usage | What it reports, at the model, tier and cost it reports, whatever its status. |
| Non-2xx with no usage | Nothing. |
| 2xx that reports no usage | The whole reservation, marked **estimated**. |
| A stream that breaks, or an app that leaves mid-response | The whole reservation, marked estimated, even if some usage was read. |
| No response at all | Nothing. |
| `count_tokens`, `input_tokens`, `moderations` | Nothing: these routes are free, and exempt. |

`kman key show --json` reports what the provider reported (`cost_micro`) and what kman estimated (`estimated_micro`) separately.

**Refusals on a capped key (or a key with a model allowlist).** A request kman can't meter is refused rather than forwarded unmetered: `unmetered_request` for no usable top-level `model`, a body that isn't JSON, or a generic upstream; and `unknown_model_price` for a model with no price, which names the model, the upstream, and the `kman price set` command that prices it. **Today:** this makes `GET /v1/models` an `unmetered_request` on a capped key, since it has no `model`; an uncapped key sends it freely, and such requests show as *unmetered* in the spend, never as $0. An unpriced model on an uncapped key is charged only if the response reports its own cost, else counted as unmetered.

**When the store is unwell.** Spend is held in memory and written to the database every 5 seconds and on shutdown. If spend has failed to reach the database for 60 seconds, capped keys are refused with `store_unavailable` until it can again. A stopped kman waits 10 seconds for requests still running; any still running after that are settled too late and lost, so a restart can forget the last few seconds of spend.

**What can go wrong with caps.** The input side of the reservation is bytes ÷ 4, and text that tokenizes densely (rare scripts, emoji, code, letters spaced apart) can cost up to about three times that, so a key can overshoot by up to about three times the input side of what is in flight. Output is exact, because the upstream enforces `max_tokens`. A price override replaces the whole built-in entry, so it loses that model's tiers and long-context bands (see [Price data](price-data.md)). See [Threat model](../THREAT-MODEL.md).

## The errors an app sees

Every error from kman is OpenAI-shaped JSON, with an `X-Kman-Error` header carrying the code.

| Status | Code | When |
|---|---|---|
| 401 | `invalid_key` | Not a kman key, a bad checksum or secret, an unknown id. |
| 401 | `key_expired` | The key's expiry has passed. |
| 401 | `key_revoked` | The key was revoked. |
| 403 | `key_disabled` | The key was disabled. |
| 403 | `path_not_allowed` | The method and path match no rule. |
| 403 | `model_not_allowed` | The model is not on the allowlist. |
| 403 | `address_not_allowed` | The client address is outside the key's `allowed_cidrs`, or its country outside `allowed_countries`. |
| 403 | `unmetered_request` | The key may send only requests kman can meter, and this is not one. |
| 403 | `unknown_model_price` | A capped key asked for a model with no price. |
| 403 | `spend_cap_reached` | The request would take the key over a cap. |
| 413 | `request_too_large` | The body is over 32 MiB. |
| 429 | `rate_limited` | Over the key's `rpm_limit`, a throttled address, or too many request bodies in memory at once (with `Retry-After`). |
| 502 | `upstream_unavailable` | kman could not get an answer from the upstream, or the upstream redirected somewhere else. |
| 503 | `store_unavailable` | Spend can't be recorded (see above). |

Errors the upstream itself sends (a wrong real key, a provider outage, its own rate limit) pass through unchanged.
