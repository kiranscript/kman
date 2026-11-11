# kman

kman is a self-hosted proxy that holds real upstream API keys and hands apps virtual keys that can be limited, watched, and killed, so a leaked key is worth less and is easy to contain.

## Language

### Keys and tokens

**Virtual key**:
The secret an app sends to kman in place of a real upstream key. It is shown once; kman keeps only its hash.
_Avoid_: Token, API key (ambiguous with the real key)

**Key id**:
The non-secret handle inside a virtual key, used to look it up and to refer to it safely in logs, alerts, and UIs.
_Avoid_: Key name (a name is a separate, operator-chosen label)

**Canary key**:
A virtual key whose only purpose is to raise an alert when anyone uses it. Nothing in the key itself distinguishes it from a standard virtual key.
_Avoid_: Honeytoken, decoy key, canary token

**Address lock**:
A virtual key's list of the networks (`allowed_cidrs`) and countries (`allowed_countries`) it may be used from. A request from anywhere else is refused before its body is read, and each lock that is set must hold. A key with neither is open to every address.
_Avoid_: Allowlist (that word is for models and routes), IP restriction

**Child key**:
A short-lived virtual key minted from another virtual key, so a less-trusted client such as a browser can open a realtime session without holding the parent. It can only narrow its parent's policy, and its spend counts against the parent. Unlike other virtual keys, a child key is recognisable as one.
_Avoid_: Ephemeral token (that is the provider's own concept)

**Realtime session**:
One long-lived connection, over WebSocket or brokered WebRTC, through which an app talks to an upstream's realtime API. It is metered and capped while it runs, not only at the end.
_Avoid_: Stream (a streamed HTTP response is a different thing), call

**Admin token**:
A named secret an operator uses to manage kman. An install can have several, and the proxy never accepts one as a virtual key.
_Avoid_: Admin key, master key (the master key is a different secret)

### Operating kman

**Operator**:
The person who runs a kman install and manages its upstreams, credentials, and virtual keys.
_Avoid_: Admin, user (an app using a virtual key is not an operator)

**Data directory**:
Where kman keeps its database. Anything in it may end up backed up, synced, or shared, so the master key never lives there.
_Avoid_: State dir

**Config directory**:
Where kman keeps its settings, the master key file, and the CLI's saved admin token, apart from the data directory.

**Admin API**:
The single interface through which every change to a running kman is made. The CLI and the dashboard are both clients of it.
_Avoid_: Management API, control plane

**Audit log**:
The append-only record of every change made to a kman install, each entry naming its actor.

**Actor**:
Who made a change: an admin token, by name, or kman itself acting on a detection.
_Avoid_: User, admin (too vague)

**Dashboard**:
The web interface, served by kman itself, for watching keys and responding to leaks.
_Avoid_: Web UI, console, TUI (the TUI no longer exists)

**Exposure**:
How easy a working virtual key would be to abuse if it leaked: open to every address, never expiring, or unused for 30 days. `kman exposure` lists each finding with the command that fixes it.
_Avoid_: Risk score, key at risk (that is the state of a key, and an exposure finding is one rule it breaks)

**Scan**:
Searching files or standard input for virtual keys that ended up where they should not be, offline and by the key's checksum. It reports where a key is and which key id to look up, never the secret.
_Avoid_: Leak check (a scan cannot say whether a key is still live)

**Backup**:
A consistent copy of the database, taken while kman runs. It holds ciphertext only and opens only with the master key it was made under, which is not in it.
_Avoid_: Export, dump, snapshot

**Restore**:
Replacing the database with a backup, offline. Everything written after the backup is gone, and the database it replaced is kept aside.
_Avoid_: Import, recover

**Retention**:
How long kman keeps the data that grows: events, hourly usage and delivered alerts are pruned past their windows, and daily spend, incidents and the audit log are kept forever. `kman prune --before` prunes by a date of the operator's own.
_Avoid_: Cleanup, expiry (that is a key's)

### Upstreams and credentials

**Upstream**:
The fixed service a credential's requests are forwarded to.
_Avoid_: Provider, backend, destination

**Preset**:
A built-in template for a well-known upstream: where it is and how it authenticates. An upstream copies its preset once, when it is added, and changes only when the operator syncs it.
_Avoid_: Provider profile

**Credential**:
The real secret for one upstream, held by kman and never shown to apps.
_Avoid_: Real key, provider key

**Master key**:
The secret that unlocks every credential kman holds. It is supplied by a key source and never stored alongside kman's data.
_Avoid_: Encryption key (too vague), admin token (a different secret)

**Data key**:
The key that encrypts credentials. It is stored only in wrapped form, encrypted under the master key.

**Key source**:
Where kman obtains the master key at startup: a file, systemd-creds, an environment variable, or a passphrase.

**Rekey**:
Replacing the master key and the data key together, so every credential is re-encrypted.
_Avoid_: Rotation (rotation is for virtual keys and credentials)

**Credential compromise**:
The response to a leaked credential: kman replaces the credential and rotates every virtual key that uses it in one step.

### Lifecycle

**Rotation**:
Replacing a virtual key's secret. The key itself, its key id, and everything attached to it (spend, baseline, incidents, audit history) stay the same.
_Avoid_: Reissue, regenerate (both suggest a new key)

**Grace period**:
The window after a rotation during which the previous secret is still accepted alongside the new one.

**Maximum key lifetime**:
The install-wide limit on how long any virtual key may stay valid. No key can be set to outlive it.
_Avoid_: TTL (that is the default expiry, not the limit)

**Key at risk**:
A virtual key whose settings or state would make a leak more costly: open to any address, without a spend cap, close to its cap, or about to expire.

**Retired secret**:
A virtual key's old secret after its grace period has ended. It no longer works, but kman still recognises it so that any later use raises an alert.

### Detection

**Event**:
The record of one request or realtime session through kman: which key sent it, from where, what it was allowed to do, and what it cost. Never its content.
_Avoid_: Log line, request log

**Baseline**:
What normal use of one virtual key looks like: its usual request rate, spend, and the places it is used from.

**Learning period**:
The time after a key is created during which kman builds its baseline, and the rules that compare against the baseline only record.

**Detection**:
One firing of one rule against one virtual key, with its evidence.
_Avoid_: Alert (an alert is a message sent about an incident)

**Incident**:
The open case for one virtual key that groups its detections until someone resolves it. It is quiet when nothing has fired for a while, and it can expire if nobody acknowledges it.
_Avoid_: Alert, event

**Alert**:
A message kman sends to a channel when an incident opens or escalates, or when a spend or lifecycle warning is due.
_Avoid_: Notification, detection

**Warning**:
An alert about lifecycle news rather than a change or a use: a soft budget crossed, a month on course to pass its limit, a key about to expire, or an upstream that refused kman's credential. A channel takes warnings only if its lifecycle toggle is on.

**Channel**:
Where alerts are sent: a Slack address or a signed webhook, with the lowest severity it takes. Its address is sealed like a credential and never shown again.
_Avoid_: Destination, integration, sink

**Alert outbox**:
The persisted queue of alerts waiting to be delivered, one entry per channel. An alert is queued in the transaction of the change that raised it, so it is never lost with it, and delivery retries with backoff until it succeeds or is given up on.
_Avoid_: Notification queue

**Priority lane**:
The path that takes the use of a disabled key or a canary key from the gateway to the alert outbox without waiting behind ordinary events. Its alerts say where the request came from and never what it said.

**Known source**:
A labelled address range, country, or network that the operator has said is legitimate, either for every key or for one key. Use from a known source never counts as a new location.
_Avoid_: Allowlist (an allowlist refuses everything else; a known source refuses nothing)

**Auto-disable**:
kman disabling a virtual key by itself when an incident looks like an attack. It never revokes. A key can have it off, on, or in dry-run.

**Dry-run**:
The auto-disable mode that only records when kman would have disabled the key and how many requests it would have blocked.

### Spend

**Spend cap**:
A hard daily or monthly limit on a virtual key's spend. Requests that would exceed it are refused.

**Soft budget**:
A spend threshold that raises a warning and refuses nothing. A key can have one with or without a spend cap.
_Avoid_: Soft cap (it never caps)

**Hourly usage**:
One key's requests, refusals, errors, tokens and cost for one hour, counted as events arrive. It outlives the events it counts (400 days by default), and it is where "last used" comes from.

**Committed spend**:
What a virtual key has actually been charged in the current day or month.

**Reservation**:
A temporary claim on a virtual key's spend, taken before a request is forwarded and replaced by the actual cost once the response ends.
_Avoid_: Hold, pre-authorization

**Price table**:
The built-in record of what each model costs and how much it can output in one response. It changes only with a kman release.
_Avoid_: Price list, prices file

**Price override**:
An operator's correction or addition to the price table, for the whole install or for one upstream.

**Price multiplier**:
A per-upstream factor on built-in prices and install-wide price overrides, for a regional uplift. It doesn't touch that upstream's own price override or a cost the provider reports.

**Unmetered request**:
A request whose model or price kman can't determine. Keys with a spend cap or a model allowlist refuse it.
