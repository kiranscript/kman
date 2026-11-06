# Price data

kman's price table says what each model costs and how much it can output in one response. It sizes reservations, so it is an input to enforcement: it changes only with a release ([ADR 0007](adr/0007-price-data-ships-with-releases.md)). kman never fetches price data at runtime. A gate fails if `internal/pricing` depends on anything that could reach the network.

## Where it lives

| File | What it is |
|---|---|
| `internal/pricing/table.json` | The table, embedded in the binary. One model per line. Generated; don't edit it. |
| `internal/pricing/pricegen/maxoutput.json` | The reviewed max output file. Edit this by hand. |
| `internal/pricing/pricegen/disagreements.md` | Every value the sources state differently, and every row left out. Generated. |
| `internal/pricing/NOTICE` | The licences of the datasets the table comes from. |

## How it's generated

`sh scripts/update-price-data.sh` downloads the three datasets and runs `pricegen generate`, which reads files and nothing else. To run it on files you have:

```sh
go run ./internal/pricing/pricegen generate --litellm a.json --genai b.json --modelsdev c.json
```

It leaves the table alone, `generated_at` included, when no price changed. It refuses a dataset with no rows for kman's providers.

- **Sources:** LiteLLM's `model_prices_and_context_window.json`, genai-prices' `prices/new_data/v2/data.json`, and models.dev's `api.json`.
- **Sections:** `openai` and `anthropic` rows are the `global` section. Each other LLM preset (`gemini`, `groq`, `mistral`, `deepseek`, `xai`, `together`, `fireworks`, `cerebras`, `openrouter`) has its own, under the id its API uses. Lookup tries the preset, then global.
- **Conservative merge:** at every input size, the highest price any source states; the largest context window and max output. Where two sources put a long-context threshold in different places, the lower one wins.
- **DeepSeek is priced at peak.** models.dev holds only the off-peak price, so its DeepSeek prices are never read (its limits are), and a DeepSeek model only models.dev prices is left out.
- **Exact ids:** every id and alias a source lists is a row. A source's prefix match names no id, and is not turned into one. An alias with no limits of its own takes those of the model the source says it names.
- **Rows left out:** LiteLLM modes kman doesn't meter (images, audio, video and so on, whatever the other sources say), rows with no input price, no output price or no max output (an embedding needs neither of the last two, nor does a model billed per minute). A capped key refuses such a model, and an operator can add it with a price override.

## Service tiers and bands

- Tiers are named as the sources name them: `batch`, `flex`, `priority`, `fast`, `ultrafast`, `balanced`. A tier that states only some prices takes the rest from the same source's base prices.
- A reservation with no tier, or a tier the table doesn't know, uses the tier that would charge the most. This means a model with a `fast` tier reserves at the `fast` price unless the request names its tier, and settlement then charges the tier the upstream reports.
- A band over a threshold reprices the whole request. Reservations pick it from the estimated input, settlement from the actual input, counting every kind of input token.

## Reviewing

**The weekly pull request.** `.github/workflows/price-data.yml` regenerates the table every Monday and opens or updates a pull request on `price-data/update` when a price changed. Its body lists each changed value, each added and removed model, and each max output equal to the context window.

1. Check each changed value against the provider's page.
2. Read the new rows in the `disagreements.md` diff. The table already keeps the conservative value; a row needs action only if that value is wrong. To correct a max output, edit `maxoutput.json`. A wrong price is fixed at its source, or by leaving the change out of this release.
3. `TestTheEmbeddedTableAgreesWithTheProviderPages` pins some prices read from provider pages. If it fails, confirm the new price, then update the test.
4. A merged data-only change ships as a patch release.

**On every pull request** the `price-flags` CI job does the same check against the base branch and lists the results in its job summary. It fails only on a reviewed max output equal to the context window with no explanation.

**The reviewed file.** An entry records a value someone read on the provider's own page:

```json
{"openai": {"gpt-5.4": {"max_output": 128000, "checked": "2026-11-06", "source": "https://developers.openai.com/api/docs/models/gpt-5.4"}}}
```

It wins over the sources in both directions, since they overstate (a context window listed as the max output) and understate (Cerebras's free-tier limit). A provider that publishes no separate limit gets `equals_context`, saying so. Add an entry only after reading the page.

## Staleness

The table carries `generated_at`. `kman price list` and plain `kman` show its age, and past 60 days they say `price data is N days old, upgrade kman`. Age never changes what kman enforces.
