# kman

**A self-hosted proxy that holds your real API keys and gives your apps virtual keys you can limit, watch and kill.**

An API key for OpenAI or Anthropic, pasted into an app, a CI job or a laptop, is a bearer token for your credit card. When it leaks it is worth everything the account can spend, and the only fix is to rotate it everywhere at once. kman sits between your apps and the provider. The real key stays sealed inside kman. Each app gets its own **virtual key** that can be capped, rate-limited, allowed only certain models and routes, expire on its own, and be revoked in one command without touching the real key or any other app.

```
 your app ──(kmn_… virtual key)──▶  kman  ──(real key)──▶  OpenAI, Anthropic, …
                                      │
                              limits · spend · audit
```

- **Drop-in.** The official OpenAI and Anthropic SDKs, curl, and anything that takes a base URL and a key work with no other change. Streaming works. [Snippets](docs/sdks.md).
- **Spend caps that hold.** kman reads each response's usage as it passes, prices it, and refuses a request that would take a key over its daily or monthly cap. A request kman can't meter is refused on a capped key, never forwarded unmetered.
- **Killable.** Disable or revoke a key and it is refused from its next request, with no restart. Every key expires; none lasts for ever.
- **Credentials stay sealed.** Real keys are encrypted under a master key that is never kept in the data directory, never shown after entry, and never in a log.
- **Private by default.** Both listeners bind to `127.0.0.1`, and kman makes no network call you didn't ask for. No telemetry, no update checks.
- **One static binary,** signed releases with provenance and SBOMs, and every change audited with who made it.

## Quickstart

On Linux or macOS, with an OpenAI API key to hand. `kman serve` stays in the foreground, so open a second terminal for the lines after it. [The quickstart](docs/quickstart.md) explains each line; CI runs exactly these.

<!-- quickstart:begin -->

```sh
curl -fsSL https://raw.githubusercontent.com/kirank55/kman/main/install.sh | sh
kman init
kman serve
kman upstream add openai --preset openai
kman key create my-app --upstream openai
export OPENAI_BASE_URL=http://127.0.0.1:8080/v1
```

<!-- quickstart:end -->

Then paste the `export OPENAI_API_KEY=kmn_…` line `kman key create` printed, and your app is talking to OpenAI through kman. Plain `kman` says where the install stands and what to run next.

## What v0.1.0 is, and is not

v0.1.0 is **Contain**: virtual keys, spend caps, allowlists, rate limits, and a kill switch, in front of sealed credentials. It is a command-line tool and an admin API. It has **no dashboard, no alerts, no leak detection, no address locks, and no realtime support.** Alerts with soft-budget and expiry warnings, address locks, `kman scan`, backup and restore have landed since: see the [changelog](CHANGELOG.md). [What v0.1.0 leaves out](docs/v0.1.0-scope.md) lists each, and says what to do in its place (for example, `kman key rotate` for a leaked key, and `kman credential compromise` for a leaked real credential).

kman is for people who run their own apps against LLM providers: a solo developer, a small team. It is **not** a hosted service, a multi-user system, or a defence against someone who already controls the machine it runs on. [The threat model](THREAT-MODEL.md) is explicit about what it protects and what it doesn't, and it draws on [a security review](docs/security-review-v0.1.0.md) of this release.

## Documentation

| | |
|---|---|
| [Quickstart](docs/quickstart.md) | Nothing to a working, metered key in five minutes. |
| [Using kman from your code](docs/sdks.md) | curl, OpenAI and Anthropic SDKs, and every preset's variable and base URL. |
| [Keys, policy and spend](docs/policy.md) | Caps, allowlists, rate limits, expiry, what is charged, the errors an app sees. |
| [Operator guide](docs/operator-guide/README.md) | Install per platform, key sources, systemd, Docker and Compose, TLS behind a reverse proxy, upgrade. |
| [What v0.1.0 leaves out](docs/v0.1.0-scope.md) | And what to do meanwhile. |
| [CLI reference](docs/cli.md), [admin API reference](docs/admin-api/README.md) | Generated; CI fails if they are stale. |
| [Price data](docs/price-data.md) | Where prices come from, overrides, staleness. |
| [Threat model](THREAT-MODEL.md), [Security policy](SECURITY.md), [Verify a release](docs/verify.md) | |
| [Changelog](CHANGELOG.md), [Spec](SPEC.md), [Glossary](GLOSSARY.md), [Decisions](docs/adr) | |

## Install

`install.sh` for Linux and macOS, release archives (with macOS and Windows notes), Homebrew, `.deb` and `.rpm`, and a Docker image: [Install](docs/operator-guide/install.md). Every release is built by one workflow from a tag, signed, attested, and rebuildable: [verify it](docs/verify.md).

## Security

Please report a vulnerability privately: see [SECURITY.md](SECURITY.md).
