# Price data ships with releases and is never fetched at runtime

kman's price table (each model's prices and its maximum output tokens) is embedded in the binary and changes only with a kman release. It is generated at build time from LiteLLM's `model_prices_and_context_window.json` (MIT), cross-checked against pydantic genai-prices (v2 feed) and models.dev. Where the sources disagree, kman keeps the conservative value (the higher price or tier, the larger max output), and a person reviews each disagreement against the provider's own page. Maximum output for every model in a preset comes from a reviewed file checked against the provider's docs, because LiteLLM often lists the context window there. A weekly CI job regenerates the table and opens a pull request with the diff, and a merged data-only change ships as a patch release. Between releases, operators fix or add a model with a price override, stored and audited through the admin API.

Stale price data is a safety problem, not only an accuracy one: reservations are sized from a model's maximum output, so an understated value lets a cap be overshot and an overstated one refuses legitimate requests. That makes the price table an input to enforcement, and enforcement inputs should change only through a reviewed release or an audited operator action.

## Considered options

- **Fetch prices at runtime (LiteLLM's default fetches from GitHub `main`).** Rejected. Anyone who can change that file can change every install's spend enforcement, and a self-hosted proxy would gain a network dependency at startup.
- **An opt-in fetch of kman-signed data.** Rejected for v1. It would need its own signing key, rollback rules, and a failure mode, and price overrides already cover the urgent case of a model released between kman releases.
- **An overlay file read at startup (keman's `KEMAN_PRICES_FILE`).** Rejected. It changes enforcement without an audit entry or an actor, which breaks the single mutation path (ADR 0004).

## Consequences

- An install's price table is only as fresh as its kman version. kman shows the table's age, and past 60 days the status summary and the dashboard ask for an upgrade. Old data never changes what is refused.
- Prices move often, so data-only patch releases can come as often as weekly. The release pipeline has to be cheap and safe to run that often.
- A price override wins until the operator removes it, even after a release adds the model. `kman price list` shows overrides that hide a built-in entry.
