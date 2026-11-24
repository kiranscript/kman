# Incidents and known sources

When something about a key looks wrong, kman opens an **incident**: one case per key, which groups everything that fired for it until you close it. What fires one is a [canary](canaries.md) being used, or a [location rule](#location-rules): a key turning up from a new country, ASN, network or client. The rate and spend rules will raise detections into the same incidents.

An incident has a plain number, such as `7`, which every alert and command uses.

## How an incident behaves

- **One unresolved incident per key.** A new detection for a key joins its open incident. Only once you resolve it (or it expires) does the next detection open a new one.
- **It alerts when it opens and when it escalates.** It escalates when a detection has a higher severity (`low`, `medium`, `high`, `critical`), comes from a rule the incident has not had, or arrives after an hour of silence. A repeat of a rule it already has only adds to a counter, so a leak that keeps firing is one alert and a count, not a flood. [Alert channels](alerts.md) take incident alerts by their `--min-severity` like any other.
- **Quiet** means an hour without a firing. It is for display, and `kman incidents` says so.
- **It expires if nobody looks.** An incident that you have not acknowledged, that is not critical, and has been quiet for 7 days is resolved as `expired` by `kman serve` (it checks every 15 minutes). **Acknowledged incidents and critical ones, which is every canary's, never expire.**
- **Its states** are `open`, `acknowledged` and `resolved`, and a resolved incident says how it ended: `confirmed`, `false positive` or `expired`.

## See them

```sh
kman incidents              # newest first
kman incidents --open       # only those not resolved
kman incidents --follow     # print changes as they happen, until you press Ctrl-C
kman incident show 7        # one incident: its detections and what was done about it
```

```
ID  KEY              STATE         SEVERITY  RULES   DETECTIONS  OPENED             LAST FIRED
7   deploy-env (Q3…) open          critical  canary  3           2026-11-05T08:30Z  2026-11-05T08:41Z
```

`incident show` prints the timeline: each detection with what it saw (country, AS and its organisation, client, and what a rule observed against the baseline), and each action taken on the incident, with who did it and when. The kman's own steps are by `system:detect`.

**Client addresses are personal data**, so `incident show` and the admin API leave out the address and network of what a detection saw, and say so, unless you turn on `kman settings set expose_client_addresses true` ([ADR 0010](../adr/0010-client-addresses-are-opt-in-on-the-admin-api.md)). The country, the AS, the client and what a rule observed are always shown. An *alert* to your own channel does name the address: it is how you know who used the key.

`--follow` asks kman every 2 seconds and prints a line each time an incident opens, escalates, is acknowledged or is resolved. A repeat is not a change and prints nothing. `--json` prints one incident per line.

## Respond

| You want to | Run |
|---|---|
| Say you have seen it | `kman incident ack 7`: repeats already do not alert, and acknowledging stops it expiring. An escalation still alerts. |
| Stop the key for now | `kman key disable <key> --incident 7`: the incident stays open, with the disable on its timeline. |
| Replace the key's secret | `kman key rotate <key> --incident 7`: resolves it as confirmed. |
| End the key | `kman key revoke <key> --incident 7`: resolves it as confirmed. |
| Replace a leaked credential | `kman credential compromise <credential> --incident 7`: resolves it as confirmed. |
| Close it as real | `kman incident resolve 7` (the default is `--as confirmed`) |
| Close it as not a problem | `kman incident resolve 7 --as false-positive` |
| Say it was you | `kman incident this-was-me 7` |

`--incident` checks that incident 7 exists, is about that key (for a compromise, a key on that credential) and is not resolved, before it changes anything. Every audit entry the command writes carries the number, so `kman incident show 7` lists it, and `kman audit` shows it. Without `--incident` the same commands do exactly what they did before and leave incidents alone.

**Resolve says what you decided.** *Confirmed* means it was real. *False positive* means it was not, and (once kman learns baselines) it lets what the incident held out of the baseline back in, so it is not the default.

### This was me

```sh
kman incident this-was-me 7
kman incident this-was-me 7 --add-known-sources
kman incident this-was-me 7 --add-known-sources --install-wide --kinds country,asn --label "Berlin office"
```

It resolves the incident as a false positive. With `--add-known-sources` it also adds what the incident saw (its countries, its ASNs, its clients, and the network of an address) as [known sources](#known-sources), for that key, or for every key with `--install-wide`. `--kinds` picks from `country`, `asn`, `client` and `network`, and `--label` names them (the default says which incident they came from).

Without the flag, at a terminal, kman lists what it saw and asks `[y/N]`; in a script it asks nothing, resolves, and prints the command to add them. You can run it again on an incident already resolved as a false positive to add them later.

Two limits are deliberate. A **network** is added only with `expose_client_addresses` on (kman says it skipped one), because adding it would show it to anyone who can list known sources. And a **canary's incident takes no known sources**: `this-was-me` still resolves it if you tested your own canary, but `--add-known-sources` is refused.

## Location rules

A leaked key turns up somewhere it has never been. kman keeps the **places** each key is used from (`kman key places`), and once a key has been **learned**, the first use from a place it has never been used from is a detection on its incident:

| Rule | Fires on | Severity |
|---|---|---|
| `new_country` | a country the key has never been used from | high |
| `new_asn` | an autonomous system it has never been used from | medium |
| `new_network` | an IPv4 /24 or IPv6 /64 it has never been used from | low |
| `new_client` | a [client](../../GLOSSARY.md) (`openai-python/3`, `curl/8`, `none`) it has never been used by | medium |

Severities are fixed. They compare against the key's places, not against rate or spend.

- **Learning period.** A key's learning period starts at its **first forwarded request** (not when it was created, and not at a refusal) and lasts **7 days**. While it lasts, kman records the places and the rules say nothing. Change it for every key with `kman settings set learning_period 3d` (`none` for no learning period; at most 90 days), and for one key with `"learning_period": "3d"` in its [policy](../policy.md). A key that has only ever been refused has not started learning: nothing is raised for it until a forwarded request has started the period and it has passed.
- **A refused request counts.** A request an address lock refused, from a place the key has never been used from, raises the rule too, and the detection says it was refused and why. A lock that stops a leak still tells you the key turned up.
- **The first place of a kind is the baseline.** A key that has no country yet (it was used before you installed a GeoIP database) has nothing to differ from, so its first country is learnt, not raised. The same goes for a client, which kman began to record in a later release.
- **Known sources never trigger them.** A request from a [known source](#known-sources) (its address inside a known CIDR, or its country, ASN or client equal to one) raises nothing, even if it is new in another way. The request's places are still recorded, so they are not new the next time either.
- **Turn a rule off for one key** in its policy: `kman key update my-app --policy '{"detect":{"new_client":false}}'` (`new_country`, `new_asn`, `new_network`, `new_client`). A canary has no rules to turn off and is not watched by these: its use is its own critical rule.
- **One alert for one request.** A request that is new in several ways at once (a thief's first request usually is) is one alert for the highest severity, and its incident lists every rule that fired.

The alert says which key, what is new with its label (an AS's organisation), the places the key normally uses (its busiest three of that kind and how many more), where the request came from, and how to contain it:

```
[HIGH] Incident opened
Key: deploy-bot (a1b2c3d4)
Rules: new_country, new_network
New place: new country FR
Usually used from: US (412 requests), DE (88 requests)
From: 203.0.113.9
Network: 203.0.113.0/24
Country: FR
ASN: AS64500 Example Net
Client: openai-python/3
Detail: the request was refused (address_not_allowed)
kman incident show 7
kman incident ack 7
kman key disable a1b2c3d4 --incident 7
kman key rotate a1b2c3d4 --incident 7
kman incident this-was-me 7
```

If the place is yours, `kman incident this-was-me 7 --add-known-sources` makes it a known source so it is not raised again. Networks are addresses and so personal data: the alert to your own channel names them, but `kman incident show` and the admin API leave out the address, the network and (for a new network) the networks the key normally uses unless `expose_client_addresses` is on. Everything else, such as the country, the AS and the client, is always shown.

What these rules cannot do: a place that was in the key's places while it was learning is normal to them, so a key that leaked in its first week is not caught by a location rule (a [canary](canaries.md) is the answer to that), and a client is the caller's own claim, which a thief can copy. [ADR 0015](../adr/0015-the-location-rules-run-in-the-transaction-that-records-a-new-place.md) has the reasoning.

## Known sources

A known source says a place is legitimate: use from it never counts as a new location to the location rules. It matches the whole request, so a CI network's addresses stay quiet even from a new country. It is a labelled **CIDR, country, ASN or client**, for every key or for one.

```sh
kman known-source add cidr 203.0.113.0/24 --label "CI runners"
kman known-source add country DE --label "Berlin office" --key deploy-bot
kman known-source add asn AS3320 --label "Telekom" --key deploy-bot
kman known-source add client curl/8 --label "our health check"
kman known-source list
kman known-source list --key deploy-bot    # every key's, and this key's
kman known-source rm 4
```

| Kind | What matches | Limits |
|---|---|---|
| `cidr` | the address is inside the network (a bare address is a /32 or /128) | no wider than an IPv4 /16 or an IPv6 /32 |
| `country` | the request's country | two capital letters, such as `DE` |
| `asn` | the request's AS number | a number, with or without `AS` |
| `client` | the request's [client](../../GLOSSARY.md), as events record it | a product and major version such as `curl/8`; `none` and `unknown` are refused |

These bounds are what keeps a known source from silencing everything: a `/8` of addresses or "every request with no `User-Agent`" would blind the rules. There are at most 200 for each scope (every key, or one key), and each needs a label. Adding and removing one is audited.

A known source is for the *baseline* rules (the location rules). It never makes the rate and spend rules ignore a place, and it **never silences a canary**: nothing does except the canary's own [ignore list](canaries.md#your-own-scanners).

## See also

- [ADR 0014](../adr/0014-an-incident-is-one-case-per-key-and-an-operator-answers-it-with-commands.md): why it works this way.
- [Alerts](alerts.md), [canary keys](canaries.md), [rotating keys and replacing a leaked credential](rotation.md).
- [CLI reference](../cli.md) and [admin API reference](../admin-api/README.md).
