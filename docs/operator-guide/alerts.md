# Alerts

kman tells you when something happens through a **channel**: Slack, or a signed webhook that can bridge to anything else. An alert says what happened and what to type next. It never holds a secret or the content of a request, and it carries no link that acts on anything.

kman sends nothing until you add a channel, and then only to the address you gave it ([ADR 0008](../adr/0008-no-network-call-unless-configured.md)).

## Add a channel

```sh
kman channel add ops --kind slack
kman channel add pager --kind webhook --min-severity high
```

kman asks for the address without echoing it, or reads one line of standard input when it is piped, or reads the variable `--url-env` names. It is never an argument: a Slack address is itself a secret, and an argument ends up in your shell history and in process lists. The address must be `https`; plain `http` is accepted only to this machine (`localhost`, `127.0.0.1`, `::1`), for a local bridge. kman seals the address like a credential and never shows it again: `kman channel list` shows only its host.

| Flag | Default | Meaning |
|---|---|---|
| `--kind` | required | `slack`, or `webhook` |
| `--min-severity` | `medium` | The lowest severity the channel takes: `low`, `medium`, `high` or `critical` |
| `--lifecycle-alerts` | `true` | Whether the channel also takes spend and lifecycle warnings. Turn it off with `--lifecycle-alerts=false` |
| `--url-env` | | Read the address from this environment variable |

```sh
kman channel list          # each channel, and what its outbox holds
kman channel test ops      # send one test alert now, whatever its minimum severity
kman channel rm ops        # remove it, wipe its address, give up what still waits for it
```

`channel test` tells you why a channel did not answer ("the channel answered 404 Not Found", "no answer within 10s") and never quotes the address. Adding and removing a channel is audited, and it is itself alerted on, so the channels you keep hear that one was added or removed.

## What raises an alert

| Producer | Severity | When |
|---|---|---|
| **The audit log** | by action, below | Every change made through the admin API |
| **A disabled key in use** | high | A request arrived with the secret of a disabled key |
| **A canary key in use** | critical | A request arrived with the secret of a key that has no credential |
| **Spend** | medium, high at 100% | A key's spend reaches a threshold of a cap or soft budget, or the month is on course to pass it |
| **Key expiry** | medium, then high | A key is about to expire, or has |
| **A refused credential** | high | An upstream answered 401 to the credential kman presented |

The two key-use rows go beyond the producers SPEC §9 lists for the first release, and they are the only ones that react to a request rather than a change. They name the key and where the request came from, and never the request's path or anything else it said, because a stranger holding a disabled key or a canary writes that text. A key kman refuses for any other reason, such as a deleted credential, is not a canary and raises nothing.

The audit log is a producer, so the change and its alert commit in one transaction: an alert is never lost with a change that went through. The severity of an audit entry follows its action:

- **high:** `admin_token.create|rotate|revoke`, `credential.compromise`, `key.revoke`
- **medium:** `key.disable`, `key.enable`, `key.rotate`, `credential.create|update|delete`, `alert_channel.create|delete`, `backup.create`, `data.prune`
- **low:** `key.create`, `key.update`, `upstream.*`, `settings.set`, `price_override.*`, and any action this kman does not know

With the default `--min-severity medium`, a channel hears about the first two groups. A key that is used again and again alerts once per 10 minutes, and the next alert says how many uses it held back.

Retention's own pass (`data.prune`, actor `system:retention`) is alerted on like any other entry. `kman restore` and `kman rekey` run with the server stopped: they are audited, but no alert is queued or sent for them.

Not yet: incidents and the detection rules, Discord and ntfy.

## Warnings

Spend, expiry and a refused credential are **warnings**: lifecycle news rather than a change or a use. A channel's `--lifecycle-alerts` toggle (on by default) decides whether it hears them, and its `--min-severity` still applies.

- **Spend.** A key's spend reaches 50, 80 and 100 percent of each of its caps, by default. A warning counts as sent only once a channel has received it: a threshold crossed while you have no channel, or none that takes warnings at that severity, is still owed, and alerts when you add one. Each threshold alerts **once per period** (a day for a daily limit, a month for a monthly one), and several thresholds crossed between two checks make one alert, for the highest. A key can have its own percentages (`soft_budget_percents`), and a soft budget (`daily_budget_micro`, `monthly_budget_micro`) with no cap at all; `kman settings set soft_budget_defaults 25,50,90` changes the default for every other key, and `none` turns the default off. See [Soft budgets and warnings](../policy.md#soft-budgets-and-warnings).
- **A projection.** Once per month, when the spend so far plus the last 7 full days' daily average times the days left would pass the monthly cap or soft budget. It is silent once the month is already over the limit.
- **Key expiry.** A key that is active and not a canary alerts 7 days before it expires (medium), 1 day before (high), and when it has expired (high), each once. A key made for less than a week does not warn a week ahead. Extending a key's expiry (`kman key update <key> --ttl 90d`) starts the warnings again for the new date.
- **A refused credential.** A request kman forwarded got `401` from the provider, so the credential kman holds for that upstream was revoked or has expired there, and every key on it is failing. The alert names the upstream and its credentials, never a secret, comes once an hour for each upstream, and says how many it held back. The fix is at the provider first, then `kman credential update <name> --secret`.

kman checks spend and expiry when it starts and then every minute, so a warning follows the spend by about a minute. What it counts is what the meter has saved, which is at most 5 seconds behind. A key that is disabled or revoked, and a canary, raise no spend or expiry warning.

The alert for a threshold says the key, the period, what was spent, the limit and whether it is a cap or a soft budget, with `kman key show <id>` to copy:

````
*[MEDIUM] my-app has used 80% of its daily cap*
Key: my-app (a1b2c3d4)
Period: 2026-11-04
Spent: $8.50
Limit: $10 (daily cap)
```
kman key show a1b2c3d4
```
````

## What an alert says

The key's name and id, the severity, who did it and from where, evidence, and commands to copy and paste:

````
*[MEDIUM] Key disabled*
Key: my-app (a1b2c3d4)
Actor: owner
Action: key.disable
Target: key a1b2c3d4
From: 127.0.0.1
Audit entry: 41
```
kman key show a1b2c3d4
kman key enable a1b2c3d4
kman audit --limit 20
````

An alert holds names, ids, counts and addresses, and nothing of what an app sent or received. It does not copy what an audit entry recorded of a change. If `dashboard.url` is set in `kman.yaml`, an alert about an incident links to that incident's page; with it unset, there is no link at all.

```yaml
dashboard:
  url: https://kman.example.com
```

## The signed webhook

A webhook channel gets a signing secret, `kmwhsec_` and 64 hex digits, printed once when you add it. Each request is a `POST` of JSON:

```json
{
  "version": 1,
  "id": "17",
  "kind": "audit",
  "severity": "medium",
  "time": "2026-11-11T10:30:15Z",
  "title": "Key disabled",
  "key": {"id": "a1b2c3d4", "name": "my-app"},
  "evidence": [{"label": "Actor", "value": "owner"}],
  "commands": ["kman key show a1b2c3d4"]
}
```

`kind` is `audit`, `disabled_key`, `canary`, `budget`, `budget_projection`, `key_expiry` or `credential_refused`, and `key` is absent in an alert that is not about one key.

with these headers:

| Header | Meaning |
|---|---|
| `X-Kman-Signature` | `v1=` and the hex HMAC-SHA256, under the signing secret, of `<X-Kman-Timestamp>.<the body>` |
| `X-Kman-Timestamp` | The time of sending, in unix seconds. Refuse one that is far from your own clock to refuse a replay |
| `X-Kman-Delivery` | The alert's id in the outbox. A retry sends the same id, so drop a delivery you have seen |

A receiver in Python:

```python
import hmac, hashlib

def verify(secret: bytes, timestamp: str, body: bytes, signature: str) -> bool:
    mac = hmac.new(secret, timestamp.encode() + b"." + body, hashlib.sha256).hexdigest()
    return hmac.compare_digest("v1=" + mac, signature)
```

To lose a signing secret is to remove the channel and add it again.

## Delivery: the outbox, retries, and a channel that hangs

Every alert is written to a persisted outbox, once for each channel that takes it, and delivered by a worker of that channel's own. So:

- **Alerts survive a restart.** What was not delivered when kman stopped is delivered when it starts.
- **A hung channel delays no other.** Each attempt has a 10 second timeout, and a channel that does not answer ties up only its own worker. The other channels' alerts are neither dropped nor delayed, and the hung channel's are held, not dropped.
- **A failed attempt is retried** after 5 seconds, then 10, doubling to at most 15 minutes, honouring a `Retry-After` that asks for longer. An alert is given up after 24 hours of failing. kman retries when the channel is busy or broken (`408`, `429`, `5xx`, no answer) and gives an alert up at once when the channel refuses it (any other `4xx`, or a redirect: kman never follows one, so an alert is not sent anywhere you did not say).
- **A channel's trouble is visible.** `kman channel list` shows the alerts pending and given up for each channel and the last error. The error never holds the address.

Alerts are delivered at least once: a webhook can see the same `X-Kman-Delivery` twice, for instance when kman stops after the receiver answered and before it recorded that.

Delivered alerts are kept 30 days, and [retention](backup.md#retention) then prunes them; alerts given up on stay until you prune them yourself.
