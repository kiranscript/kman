# kman

**A self-hosted proxy that shows you every place your API keys are used, catches a leak when it happens, and lets you kill it in one command.**

An API key for OpenAI or Anthropic, pasted into an app, a CI job or a laptop, is a bearer token for your credit card. When it leaks you usually learn about it from the bill. kman sits between your apps and the provider and holds the real key. Each app gets its own **virtual key**, and kman records, for every request, which key was used, from where and by what. When a key turns up somewhere new, or a planted decoy is touched, you are told, and one command ends the leak without touching the real key or any other app.

```
 your app ──(kmn_… virtual key)──▶  kman  ──(real key)──▶  OpenAI, Anthropic, …
                                      │
              who used which key, from where, and by what · canaries · incidents
```

- **See where a key is used.** `kman key places my-app` lists every country, network provider (ASN), network and client (`openai-python/3`, `curl/8`) a key has been used from, with when it first and last appeared, counting refused requests too. `kman events --follow` shows requests as they happen. [Watching](docs/operator-guide/incidents.md).
- **Catch a leak.** `kman canary create` makes a decoy key to plant in a repository, a wiki or a CI secret; any use of it is a critical alert naming where it was planted and where the request came from. A real key's first use from a new country, ASN, network or client, after its learning period, opens an incident. `kman incidents` lists them; `kman incident this-was-me` clears a false alarm. [Canary keys](docs/operator-guide/canaries.md).
- **Kill it in one command.** `kman key rotate my-app --incident 3` replaces a key's secret and resolves the incident; `kman key disable` and `kman key revoke` stop it from the next request; `kman credential compromise` replaces a leaked real key and rotates every virtual key on it in one step.
- **Spend caps bound the loss.** kman prices each response as it passes and refuses a request that would take a key over its daily or monthly cap, so a leaked key can only spend what you allowed. A request kman can't meter is refused on a capped key, never forwarded unmetered.
- **Drop-in.** The official OpenAI and Anthropic SDKs, curl, and anything that takes a base URL and a key work with no other change. Streaming works. [Snippets](docs/sdks.md).
- **Private and sealed.** Real keys are encrypted under a master key that is never kept in the data directory, never shown after entry, and never in a log. Both listeners bind to `127.0.0.1`, and kman makes no network call you didn't ask for: no telemetry, no update checks. Client addresses stay out of the admin API unless you turn them on. One static binary, signed releases, and every change audited with who made it.

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

Run `kman tui` to browse keys, exposure, audit activity, and upstreams in a terminal. The terminal view is read-only; use the CLI commands to make changes.

v0.1.0 is **Contain**: virtual keys, spend caps, allowlists, rate limits, and a kill switch, in front of sealed credentials. It is a command-line tool and an admin API. It had **no dashboard, no alerts, no leak detection, no address locks, and no realtime support.** Since then the watching has landed (see the [changelog](CHANGELOG.md) for each): key places, the live event feed, the calling client, canary keys, incidents with the response loop, and the location rules, along with alerts for soft budgets and expiry, address locks, `kman scan`, backup and restore. Still to come are the rate and spend baselines and their spike rules, auto-disable, Discord and ntfy alerts, and realtime support. [What v0.1.0 leaves out](docs/v0.1.0-scope.md) lists each as it stood at the release.

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
