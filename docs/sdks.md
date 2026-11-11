# Using kman from your code

An app uses kman by changing two things and nothing else: the **API key** becomes a virtual key from `kman key create`, and the **base URL** becomes kman's proxy listener, `http://127.0.0.1:8080` by default, plus the path the provider's SDK expects (`/v1` for OpenAI). The official SDKs read both from environment variables, so for most apps no code changes at all.

`kman key create` prints the key once, with the `export` line for it. Set that and the base URL, and run the app as before:

```sh
export OPENAI_API_KEY=kmn_…                      # the line kman key create printed
export OPENAI_BASE_URL=http://127.0.0.1:8080/v1
```

Every snippet below is a file under `e2e/testdata/sdk` that CI runs through the real kman with the pinned official SDKs, so each one is known to work. The pinned versions are in `e2e/testdata/sdk/node/package.json` and `e2e/testdata/sdk/python/requirements.in`. Streaming works the same way: kman passes a stream through as it arrives.

## curl

<!-- snippet:curl -->
```sh
curl "$OPENAI_BASE_URL/chat/completions" \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model": "gpt-4o-mini", "messages": [{"role": "user", "content": "Say hello"}]}'
```
<!-- /snippet -->

## OpenAI

Python (`pip install openai`):

<!-- snippet:openai-python -->
```python
from openai import OpenAI

# Reads OPENAI_API_KEY and OPENAI_BASE_URL from the environment.
client = OpenAI()

reply = client.chat.completions.create(
    model="gpt-4o-mini",
    messages=[{"role": "user", "content": "Say hello"}],
)
print(reply.choices[0].message.content)
```
<!-- /snippet -->

Node (`npm install openai`):

<!-- snippet:openai-node -->
```js
import OpenAI from "openai";

// Reads OPENAI_API_KEY and OPENAI_BASE_URL from the environment.
const client = new OpenAI();

const reply = await client.chat.completions.create({
  model: "gpt-4o-mini",
  messages: [{ role: "user", content: "Say hello" }],
});
console.log(reply.choices[0].message.content);
```
<!-- /snippet -->

Chat completions, completions, embeddings and the Responses API all work, with and without streaming. For a stream, kman asks OpenAI to report usage (`stream_options.include_usage`) even if the app said not to, because otherwise a stream could not be charged; see [One surprise on OpenAI streams](#one-surprise-on-openai-streams).

## Anthropic

The Anthropic SDKs read `ANTHROPIC_BASE_URL`, and need no `/v1`: they add it.

```sh
export ANTHROPIC_API_KEY=kmn_…
export ANTHROPIC_BASE_URL=http://127.0.0.1:8080
```

Python (`pip install anthropic`):

<!-- snippet:anthropic-python -->
```python
import anthropic

# Reads ANTHROPIC_API_KEY and ANTHROPIC_BASE_URL from the environment.
client = anthropic.Anthropic()

message = client.messages.create(
    model="claude-sonnet-5-5",
    max_tokens=1024,
    messages=[{"role": "user", "content": "Say hello"}],
)
print(message.content[0].text)
```
<!-- /snippet -->

Node (`npm install @anthropic-ai/sdk`):

<!-- snippet:anthropic-node -->
```js
import Anthropic from "@anthropic-ai/sdk";

// Reads ANTHROPIC_API_KEY and ANTHROPIC_BASE_URL from the environment.
const client = new Anthropic();

const message = await client.messages.create({
  model: "claude-sonnet-5-5",
  max_tokens: 1024,
  messages: [{ role: "user", content: "Say hello" }],
});
console.log(message.content[0].text);
```
<!-- /snippet -->

## One surprise on OpenAI streams

To meter a streamed chat or completion, kman adds `stream_options: {"include_usage": true}` to the request when the upstream has `inject_usage` on (the OpenAI preset does). OpenAI then ends the stream with **one extra final chunk whose `choices` is an empty list** and whose `usage` holds the token counts. The official SDKs handle it. Code that reads `chunk.choices[0]` on every chunk without checking does not, and breaks on that last chunk:

```python
for chunk in stream:
    if chunk.choices:                    # the last chunk has none
        print(chunk.choices[0].delta.content or "", end="")
```

kman does not strip the chunk, since that would mean rewriting the stream. An app that sets `include_usage: false` is overridden: otherwise that would be a way out of metering. Requests to OpenAI and Anthropic upstreams also go out with `Accept-Encoding: identity`, so the answers arrive uncompressed and kman can read their usage.

## Other providers

Each preset names the variable an SDK reads its key from, and the base URL to give it. This table is generated from the presets, with kman at its default address.

<!-- presets -->
| Preset | Upstream | Key variable | Base URL |
|---|---|---|---|
| `openai` | OpenAI (`api.openai.com`) | `OPENAI_API_KEY` | `http://127.0.0.1:8080/v1` |
| `anthropic` | Anthropic (`api.anthropic.com`) | `ANTHROPIC_API_KEY` | `http://127.0.0.1:8080` |
| `openrouter` | OpenRouter (`openrouter.ai`) | `OPENROUTER_API_KEY` | `http://127.0.0.1:8080/api/v1` |
| `gemini` | Google Gemini (OpenAI-compatible) (`generativelanguage.googleapis.com`) | `GEMINI_API_KEY` | `http://127.0.0.1:8080/v1beta/openai` |
| `groq` | Groq (`api.groq.com`) | `GROQ_API_KEY` | `http://127.0.0.1:8080/openai/v1` |
| `mistral` | Mistral (`api.mistral.ai`) | `MISTRAL_API_KEY` | `http://127.0.0.1:8080/v1` |
| `deepseek` | DeepSeek (`api.deepseek.com`) | `DEEPSEEK_API_KEY` | `http://127.0.0.1:8080` |
| `xai` | xAI (Grok) (`api.x.ai`) | `XAI_API_KEY` | `http://127.0.0.1:8080/v1` |
| `together` | Together AI (`api.together.xyz`) | `TOGETHER_API_KEY` | `http://127.0.0.1:8080/v1` |
| `fireworks` | Fireworks AI (`api.fireworks.ai`) | `FIREWORKS_API_KEY` | `http://127.0.0.1:8080/inference/v1` |
| `cerebras` | Cerebras (`api.cerebras.ai`) | `CEREBRAS_API_KEY` | `http://127.0.0.1:8080/v1` |
| `stripe` | Stripe (`api.stripe.com`) | `STRIPE_SECRET_KEY` | `http://127.0.0.1:8080` |
| `github` | GitHub (`api.github.com`) | `GITHUB_TOKEN` | `http://127.0.0.1:8080` |
| `slack` | Slack (`slack.com`) | `SLACK_BOT_TOKEN` | `http://127.0.0.1:8080/api` |
| `twilio` | Twilio (`api.twilio.com`) | `TWILIO_AUTH_TOKEN` | `http://127.0.0.1:8080` |
| `sendgrid` | SendGrid (`api.sendgrid.com`) | `SENDGRID_API_KEY` | `http://127.0.0.1:8080` |
| `resend` | Resend (`api.resend.com`) | `RESEND_API_KEY` | `http://127.0.0.1:8080` |
| `postmark` | Postmark (`api.postmarkapp.com`) | `POSTMARK_SERVER_TOKEN` | `http://127.0.0.1:8080` |
| `cloudflare` | Cloudflare (`api.cloudflare.com`) | `CLOUDFLARE_API_TOKEN` | `http://127.0.0.1:8080/client/v4` |
| `linear` | Linear (`api.linear.app`) | `LINEAR_API_KEY` | `http://127.0.0.1:8080` |
| `notion` | Notion (`api.notion.com`) | `NOTION_TOKEN` | `http://127.0.0.1:8080` |
<!-- /presets -->

For a provider that is not a preset, build an upstream by hand with `kman upstream add <name> --host <host> --path "METHOD /path" …` (see the [CLI reference](cli.md#kman-upstream-add)). Only the OpenAI and Anthropic profiles are metered: a key with a spend cap or a model allowlist cannot send a request to any other upstream. See [Policy](policy.md).
