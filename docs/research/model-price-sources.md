# Sources for model prices and max output tokens

Research for issue #20. Gathered 2026-11-03.

**Question.** Which machine-readable sources could keep kman's price table current, including each model's maximum output tokens, and how far can each be trusted? A reservation is sized from the price table, so an understated price or max output can let a spend cap be overshot.

**Method.** I read provider pricing pages, model pages and API references directly. I downloaded each dataset from its canonical URL on 2026-11-03 and compared it, row by row, against keman's frozen snapshot (`internal/price/prices.json`, version `2026-10-29`, 52 rows). Then I spot-checked a sample of rows against the providers' own pages. Claims marked **[search summary]** rest on a web-search summary, not a primary page I read.

## Answer in brief

- **No single source is authoritative for both price and max output.** Only the providers' own pages are authoritative. Three providers expose max output through an API that needs a key: Anthropic, Gemini and Groq. Three expose prices through an API: xAI and Together (both need a key), and OpenRouter (prices for OpenRouter only).
- **LiteLLM's `model_prices_and_context_window.json` is the strongest dataset.** It is MIT-licensed, has about 74 commits to that file in the last week, and has the widest coverage of kman's upstreams, including realtime and per-second voice. It matched the provider page on every price I spot-checked. Its weak spots are max output on some hosts: it uses the free-tier Cerebras value, which understates the limit for paid accounts, and often sets max output equal to the context window.
- **pydantic `genai-prices` (v2 feed) models the awkward shapes best:** time-of-day prices, price history, cliff-style long-context tiers and service-tier variants. It ships a Go package. But it has **no max-output field**, and its old `prices/data.json` is a frozen v1 file that is months stale.
- **models.dev** (MIT) has good max-output coverage, but it stores DeepSeek's **off-peak** price. That is half the peak price, an unsafe understatement for reservations.
- **OpenRouter's `/api/v1/models`** returns OpenRouter's own prices and its "top provider" limits. Use it only for the `openrouter` preset. At least one of its max-output values is wrong (gpt-4.1-nano: 942,818, where OpenAI says 32,768).
- **Portkey's** DeepSeek prices are stale, and its `default` entry prices unknown models at zero. **Helicone's** cost package has had no price update since March 2026.
- **Peers fail open on unknown models.** In LiteLLM an unpriced model's spend logs as $0. Portkey's default template is $0. Cloudflare's spend limits only cover "models with known pricing". kman's rule to refuse unmetered requests on capped keys is stricter than any of them.
- **Many prices don't fit a flat per-million table:** time-of-day pricing, whole-request long-context cliffs, per-second voice billing, per-request tool fees, service tiers and regional uplifts, plus request routing that changes the billed model. These are listed below.

**Recommendation.** Generate kman's embedded snapshot at build time from LiteLLM, cross-checked against models.dev and genai-prices v2. Keep the most conservative value for reservation inputs: the higher price and the larger max output. Send every disagreement and every price change to a human diff review, and correct it against the provider page before release. Don't fetch prices from GitHub at runtime the way LiteLLM does by default; a remote file that sets prices is a supply-chain input. Where the provider reports the actual cost (xAI `cost_in_usd_ticks`, OpenRouter `usage.cost`), settle from that figure instead of recomputing it.

## 1. Provider APIs

| Provider | Endpoint | Prices? | Max output? | Key needed | Source |
|---|---|---|---|---|---|
| OpenAI | `GET /v1/models` | No | No (fields: `id`, `created`, `object`, `owned_by`, `shutdown_date`) | Yes (examples send a bearer token) | [Models API](https://developers.openai.com/api/reference/resources/models/methods/list) |
| Anthropic | `GET /v1/models` | No | **Yes:** `max_tokens` ("Maximum value for the `max_tokens` parameter") and `max_input_tokens`. Also `lifecycle` (active/deprecated/retired) and `retires_at` | Yes | [List Models](https://platform.claude.com/docs/en/api/models/list) |
| Gemini | `models.list` / `models.get` | No | **Yes:** `outputTokenLimit`, `inputTokenLimit` | Page doesn't say. The Gemini API authenticates calls with a key (not verified on this page) | [Models reference](https://aistudio.google.com/api-reference/models) |
| Groq | `GET /openai/v1/models/{model}` | No | **Yes:** `max_completion_tokens` and `context_window` in the example; the list endpoint shows only `context_window` | Yes (examples) | [API reference](https://console.groq.com/docs/api-reference#models-list) |
| Mistral | `GET /v1/models` | No pricing fields mentioned | Not found. I couldn't render the schema | Yes (assumed) | [API docs](https://docs.mistral.ai/api/) (schema not readable) |
| DeepSeek | none for prices | No | No (only the pricing page) | n/a | [Pricing](https://api-docs.deepseek.com/quick_start/pricing) |
| xAI | `GET /v1/language-models` | **Yes:** `prompt_text_token_price`, `cached_prompt_text_token_price`, `completion_text_token_price` as integers in "USD cents per 100 million tokens", plus `prompt_text_token_price_long_context` and `long_context_threshold` | No | Yes ("available to the authenticating API key") | [Models endpoint](https://docs.x.ai/developers/rest-api-reference/inference/models) |
| xAI responses | `usage.cost_in_usd_ticks` | **Actual cost of each request**: "10'000'000'000 ticks in one dollar". Also `cost_in_nano_usd` | Request `max_output_tokens` "Defaults to 128,000 when unset" | Yes | [API reference](https://docs.x.ai/docs/api-reference) |
| Together | `GET /v1/models` | **Yes:** `pricing.input`, `pricing.output`, `pricing.cached_input` in $/M | No separate max output; only `context_length` | Yes | No primary schema found; behaviour taken from [LiteLLM PR #42851](https://github.com/BerriAI/litellm/pull/42851), which priced 39 rows "straight from the Together models API" and notes "the API does not state a separate max output" |
| Fireworks | `GET /v1/accounts/{id}/models` | Not on this endpoint (`serverlessModes[].skuInfos`, "sourced live from Orb", is "only populated by ListServerlessModels") | No; only `contextLength` | Yes | [List models](https://docs.fireworks.ai/api-reference/list-models) |
| Cerebras | `GET /v1/models` | No | No (fields: `id`, `object`, `created`, `owned_by`) | Yes | [Models API](https://inference-docs.cerebras.ai/api-reference/models) |
| OpenRouter | `GET /api/v1/models` | **Yes**, USD per token as strings: `prompt`, `completion`, `request`, `input_cache_read`, `input_cache_write(_1h)`, `audio`, `internal_reasoning`, `web_search`, plus `overrides` ("based on prompt token count or a UTC time window") and `discount` | `top_provider.max_completion_tokens`, `per_request_limits` | The spec says yes, but my unauthenticated `curl` on 2026-11-03 returned 451 models | [Get models](https://openrouter.ai/docs/api/api-reference/models/get-models) |
| OpenRouter responses | `usage.cost` | **Actual cost**, "Cost in credits", returned by default | n/a | Yes | [Usage accounting](https://openrouter.ai/docs/guides/guides/usage-accounting) |

None of these APIs claims its values are the authoritative billing price. The pricing pages carry their own caveats; DeepSeek, for example, "reserves the right to adjust" prices.

Realtime upstreams:

- **OpenAI Realtime** prices per modality per token. gpt-realtime-2.1: text $4 / $0.40 cached / $24 output; audio $32 / $0.40 / $64; image $5 / $0.50 ([pricing](https://developers.openai.com/api/docs/pricing)).
- **GPT-Live:** "gpt-live-1 is priced at $0.05 per minute," "billed per second, without rounding up to a whole minute"; "Backend model and tool usage are billed separately" ([pricing](https://developers.openai.com/api/docs/pricing)).
- **Responses WebSocket mode:** no page I found states its billing. That it bills like HTTP Responses is an inference **[search summary]**.
- **Gemini Live** (3.8 Live family): input $0.75 text, and "$3.00 or $0.005/min" audio; output $4.50 text, and "$12.00 or $0.018/min" audio ([pricing](https://aistudio.google.com/docs/pricing)).
- **xAI voice** grok-voice-3.0: "$0.08 per minute ($4.80 per hour), plus $0.004 per text input" ([models](https://docs.x.ai/docs/models)).

None of these upstreams exposes realtime prices through an API.

## 2. Aggregated datasets

| Dataset | License | Update cadence (checked 2026-11-03) | Max output | Cached / cache write | Audio / realtime | Tiers and long context | Notes |
|---|---|---|---|---|---|---|---|
| [LiteLLM](https://github.com/BerriAI/litellm/blob/main/model_prices_and_context_window.json) | MIT (outside `enterprise/`) | 74 commits to the file since 2026-10-27; 100+ since 2026-10-04 | `max_output_tokens` on 3,425 of 4,561 rows | `cache_read_input_token_cost`, `cache_creation_input_token_cost`, `_above_1hr` | `input/output_cost_per_audio_token`, `input_cost_per_second` (`gpt-live-1`, `xai/grok-voice-3.0`), `mode: realtime` | Suffixed keys: `_above_200k_tokens`, `_above_272k_tokens`, `_above_100k_tokens`, `_batches`, `_flex`, `_priority`; also `tiered_pricing` | Rows cite a `source` URL. 4,561 rows |
| [genai-prices](https://github.com/pydantic/genai-prices) v2 feed `prices/new_data/v2/data.json` | MIT | 19 commits in the last week; an automated weekly "Update provider prices from official sources" job | **None.** Only `context_window` | Yes, including `cache_write_1h_mtok` | Audio token prices, `audio_hours` (gpt-live-1 = 3, which is $0.05/min) | `TieredPrices` uses the "cliff" model: crossing a tier "applies that rate to ALL tokens". Time-window and start-date constraints; `price_variants` for service tier and batch | Python, JS, **Go** and Rust packages. **Trap:** `prices/data.json` is "frozen" v1 and "receive[s] no provider, model or price updates"; it was last touched 2026-07-25 |
| [models.dev](https://github.com/anomalyco/models.dev) (`https://models.dev/api.json`) | MIT | 100+ commits in the last week | `limit.output` | `cost.cache_read`, `cost.cache_write` | `cost.input_audio`, `cost.output_audio` | `cost.tiers` (context-size tiers) | 235 providers, 8,932 models. Stores one price per model, so time-of-day pricing collapses (see DeepSeek below) |
| [OpenRouter models API](https://openrouter.ai/docs/api/api-reference/models/get-models) | No license stated in the docs | Live | `top_provider.max_completion_tokens`. Several look derived: 209,715 = 0.8 × 262,144 for Mistral; 450,000 = 0.9 × 500,000 for Grok | Yes | `audio`, `input_audio_cache` | `overrides` with `min_prompt_tokens` (82 models) | Prices are OpenRouter's, which can differ from the native provider (DeepSeek V4 Pro: $1.60/$3.20 on OpenRouter, $1.32/$3.96 peak on DeepSeek) |
| [Portkey models](https://github.com/Portkey-AI/models) (`configs.portkey.ai/pricing/{provider}.json`) | MIT | 85 commits since 2026-10-04 | In `general/{provider}.json`, as a `max_tokens` param with `maxValue` | Yes | `request_audio_token`, `response_audio_token` | `context_tier_map`, `batch_config`, regions | Units are **cents per token**. "Free API, no authentication required" |
| [Helicone cost package](https://github.com/Helicone/helicone/tree/main/packages/cost) | Apache-2.0 | Last price-related commit 2026-04-04; the October commits are security syncs | `maxCompletionTokens` | n/a | n/a | n/a | Code search found Opus 4.6 and GPT-5.4 but no Claude Opus 5 entry (a search index, not exhaustive) |

### Spot check against provider pages

All prices are $ per million tokens.

| Model (preset) | Provider page | LiteLLM | models.dev | genai-prices v2 | OpenRouter | keman snapshot |
|---|---|---|---|---|---|---|
| gpt-5.4 | 2.50 / 0.25 cached / 15; >272K: 5 / 0.50 / 22.50 "for the full session"; max out 128,000 ([model page](https://developers.openai.com/api/docs/models/gpt-5.4)) | ✓ incl. tier, 128,000 | ✓ incl. tier | ✓ incl. tier | ✓ incl. override, 128,000 | base ✓, no tier |
| gpt-4.1-nano | 0.10 / 0.025 / 0.40; max out 32,768; status "Shut down" ([model page](https://developers.openai.com/api/docs/models/gpt-4.1-nano)) | ✓ | ✓ | n/c | ✗ cached 0.03, max out **942,818** | ✓ (stale row) |
| claude-opus-5-5 | 4 / 5 (5m write) / 8 (1h write) / 0.20 hit / 20; max out 128K ([pricing](https://platform.claude.com/docs/en/about-claude/pricing), [models](https://platform.claude.com/docs/en/about-claude/models/overview)) | ✓ | ✓ | ✓ | ✓ | ✓ |
| claude-haiku-5-5 | 0.10 / 0.50 out; prompts over 100,000 tokens: 0.50 / 2.50 | ✓ incl. `_above_100k` | ✓ incl. tier | ✓ incl. tier | ✓ incl. override | base only |
| claude-3-5-haiku-latest | "retired, except on Claude Models on Google Cloud" | absent | absent | n/c | absent | still present |
| gemini-2.5-pro | 1.25 / 10; >200K: 2.50 / 15; cache 0.125 / 0.25 ([pricing](https://aistudio.google.com/docs/pricing)) | ✓ incl. tier, batch, flex, priority | ✓ incl. tier | ✓ incl. tier | ✓ incl. override | base only |
| deepseek-v4-pro | peak 1.32 / 0.044 hit / 3.96; off-peak half; max out 384K ([pricing](https://api-docs.deepseek.com/quick_start/pricing)) | peak ✓, 393,216 ✓ | **off-peak only** (0.66 / 1.98) ✗ unsafe | ✓ time windows (its comment says weekend all-day off-peak isn't modelled, so weekends get the peak price, an upper bound) | own price 1.60 / 3.20 | peak ✓ |
| grok-4.7 | 2 / 0.50 / 6; ≥200K prompt: 4 / 1 / 12 for "all tokens in the request" ([models](https://docs.x.ai/docs/models)) | ✓ incl. tier; max out 500,000 (= context) | ✓ incl. tier | ✓ (tier start 199,999) | ✓ override; max out 450,000 | base only |
| gpt-oss-120b (groq) | 0.15 / 0.075 / 0.60; max out 65,536 ([model page](https://console.groq.com/docs/model/openai/gpt-oss-120b)) | ✓ | ✓ | n/c | ✓ | ✓ |
| gpt-oss-120b, deepseek-v4p1-flash, kimi-k3, glm-5p3 (fireworks) | 0.15/0.015/0.60; 0.30/0.006/1.20; 3/0.30/15; 1.40/0.26/4.40 ([pricing](https://fireworks.ai/pricing)) | ✓ all four | n/c | n/c | n/c | ✓ all four |
| gpt-oss-120b (cerebras) | max out "32k tokens on the Free tier and 40k tokens on the Paid tier" ([model page](https://inference-docs.cerebras.ai/models/openai-oss.md)). Price not readable on the client-rendered pricing page | max out **32,768** (understates paid) | 40,960 ✓ | n/c | n/a | price unverified |
| mistral-large-2512 | Mistral pricing page: "Mistral Large costs $0.5 /M tokens in and $1.5 /M tokens out" (version not named); model deprecated 2026-10-31 | ✓ | ✓ | n/c | ✓ | ✓ |
| mistral-medium-3-5 | deprecated 2026-10-31 and absent from the current pricing page, so **unverifiable** | 1.5 / 7.5 | absent | 1.5 / 7.5 | 1.5 / 7.5 | 1.5 / 7.5 |
| gpt-live-1 | $0.05/min, billed per second | ✓ `input_cost_per_second` 0.000833 | n/c | ✓ `audio_hours` 3 | n/a | absent |
| grok-voice-3.0 | $0.08/min + $0.004 per text input | ✓ per second + `input_cost_per_text_message` 0.004 | n/c | n/c | n/a | absent |
| gpt-realtime-2.1 | text 4 / 0.40 / 24, audio 32 / 0.40 / 64 | ✓ | ✓ | n/c | n/a | absent |

n/c = not checked.

Other findings from the full 52-row comparison of keman's snapshot against LiteLLM:

- Every keman row that LiteLLM also carries has identical base prices. The only row missing from LiteLLM is the retired `claude-3-5-haiku-latest`.
- keman's flat table drops all the long-context tiers that LiteLLM records.
- keman has no max-output field. kman is adding one.

Max-output quality in LiteLLM, as missing / equal to `max_input_tokens` / total chat rows, per provider: openai 5 / 1 / 101; anthropic 0 / 0 / 22; xai 0 / 36 / 36; mistral 0 / 40 / 51; together_ai 15 / 70 / 89; fireworks_ai 7 / 225 / 321.

A max output equal to the context window usually means the host doesn't publish a separate limit. Two examples: xAI's models page "does not list a max output token value", and Mistral's model card says "Max output tokens: Not stated". Such a value overstates, which is safe for caps but makes reservations larger than needed. Real understatements are rarer but do happen: Cerebras paid tier, and LiteLLM's own fix "raise GovCloud West Llama 3 max output to the model card 8K" (#49543).

LiteLLM's commit log shows a steady stream of corrections in the last month, for example "set together_ai Qwen3.7-Max back to the published price" (#49125) and "price Mistral Large 4 at its current list price" (#49814). It is accurate on average but not something to embed without review.

## 3. What peers do

- **LiteLLM.** "Every process fetches the remote `model_prices_and_context_window.json` from GitHub `main`" at startup. It falls back to the bundled copy if the fetch fails. `LITELLM_LOCAL_MODEL_COST_MAP=True` disables the fetch, and `LITELLM_MODEL_COST_MAP_URL` overrides the URL. Admins can call `POST /reload/model_cost_map` or schedule reloads ([docs](https://docs.litellm.ai/docs/proxy/sync_models_github)). `register_model` adds or overrides prices ([docs](https://docs.litellm.ai/docs/completion/token_usage)).
  - **Unknown model:** a model missing from the map leads to a "cost map lookup failure" and "spend logs read zero" ([PR #42851](https://github.com/BerriAI/litellm/pull/42851)). The docs page itself doesn't say.
- **Portkey.** Its gateway is powered by the Portkey Models database; the docs don't say how often the gateway picks up changes. Enterprise rates can be overridden ([docs](https://portkey.ai/docs/product/model-catalog/portkey-models)).
  - **Unknown model:** each provider file has a `default` entry priced at 0, which the docs call "the base pricing template". That unlisted models therefore cost $0 is my inference, not stated.
- **Helicone.** Estimates cost from response `usage` "using OpenAI's pricing tables" and points to its open-source cost directory ([docs](https://docs.helicone.ai/references/how-we-calculate-cost)).
  - **Unknown model:** the docs don't say. The cost package is effectively unmaintained since April 2026.
- **Cloudflare AI Gateway.** Costs are "an estimation based on the number of tokens"; users with "a model not yet supported by our cost tracking" submit a form ([costs](https://developers.cloudflare.com/ai-gateway/observability/costs/)). Per-request overrides go in the `cf-aig-custom-cost` header as per-token values ([custom costs](https://developers.cloudflare.com/ai-gateway/configuration/custom-costs/)).
  - **Unknown model:** spend limits apply only "for models with known pricing". Spend is checked before the request and "recorded after completion", and limits "are eventually consistent", so "a burst of concurrent requests can briefly exceed the limit" ([spend limits](https://developers.cloudflare.com/ai-gateway/features/spend-limits/)). Cloudflare has no reservation step.

## 4. Shapes a flat per-million table can't express

1. **Time-of-day pricing.** DeepSeek: "Off-peak rates are half of peak rates"; peak is "01:00–04:00 and 06:00–10:00 UTC, Monday through Friday, excluding Chinese public holidays." Reserving at the peak price is safe.
2. **Whole-request long-context cliffs.** The threshold reprices *all* tokens, not only the tokens above it:
   - gpt-5.4: over 272K costs 2x input and 1.5x output "for the full session"
   - xAI: at or above 200K, "billed at the higher rate for all tokens in the request"
   - Gemini 2.5 Pro: over 200K
   - Claude Haiku 5.5: over 100K. Prompt length "counts all of its input tokens, including cache reads and cache writes"

   A reservation must use the tier price whenever the prompt could cross the threshold.
3. **Per-modality token prices.** Audio input on Gemini 2.5 Flash is $1.00, against $0.30 for text. gpt-realtime audio costs 8x text input.
4. **Per-second and per-minute billing.** gpt-live-1 is $0.05/min, billed per second. grok-voice-3.0 is $0.08/min plus $0.004 per text input. Gemini Live audio has a per-minute alternative.
5. **Per-request and tool fees:**
   - Anthropic web search: $10 per 1,000
   - Gemini grounding: $35 per 1,000 after 1,500/day free, or $14 per 1,000 after 5,000/month free on Gemini 3+. The free allowances make this stateful
   - DeepSeek Search/Fetch: $0.75 per 1,000 from 2026-11-01
   - Anthropic code-execution containers: $0.05 per hour after free hours
6. **Service tiers:**
   - Batch: 50% off at Anthropic, Mistral and Fireworks
   - OpenAI: Flex, plus "Priority processing was renamed Fast mode on July 30, 2026"
   - Anthropic fast mode: Opus 5.5 at $8 / $40
   - Gemini priority: 1.8x
   - Fireworks: Spot at 30% off, and a Priority tier
7. **Regional and data-residency uplifts:**
   - OpenAI: +10% on regional endpoints for models released on or after 2026-03-05; FedRAMP +10% from 2026-11-01
   - Anthropic: `inference_geo: "us"` is 1.1x for 4.6 and later models
   - Fireworks: US-only serverless at +50%
8. **Cache-write duration and storage.** Anthropic charges a different price for 5-minute and 1-hour cache writes (Opus 5.5: $5 vs $8). Gemini charges cache storage per million tokens per hour (2.5 Pro: $4.50), which is time-based and outside any one request.
9. **Routing that changes the billed model.** From 2026-12-01, gemini-2.5-pro and gemini-2.5-flash requests "automatically route" to Gemini 3.8 Flash and Gemini 3.5 Flash-Lite and are billed at those rates. DeepSeek's retired `deepseek-v4-flash` names are "billed at the Flash price". `*-latest` aliases (Mistral) move under a fixed ID.
10. **Limits that depend on the account or a header.**
    - Cerebras max output: 32k free tier, 40k paid.
    - Anthropic Batch: up to 300k output "with the output-300k-2026-03-24 beta header" (the synchronous API allows 128K).
    - xAI: the default output is 128,000 tokens when `max_output_tokens` is unset.

## Open items

- I couldn't read the Mistral and Together `GET /v1/models` schemas because the docs pages render on the client. For Together, the behaviour comes from LiteLLM PR #42851.
- The Cerebras price for gpt-oss-120b ($0.35 / $0.75 in every dataset) wasn't confirmed on a Cerebras page.
- Responses WebSocket billing parity with HTTP is unconfirmed **[search summary]**.
- The xAI `/v1/language-models` unit is documented as "USD cents per 100 million tokens". A third-party Rust crate states a different unit **[search summary]**, so check one live response against the models page before relying on it.
