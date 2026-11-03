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

**Child key**:
A short-lived virtual key minted from another virtual key, so a less-trusted client such as a browser can open a realtime session without holding the parent. It can only narrow its parent's policy, and its spend counts against the parent.
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

### Upstreams and credentials

**Upstream**:
The fixed service a credential's requests are forwarded to.
_Avoid_: Provider, backend, destination

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

**Committed spend**:
What a virtual key has actually been charged in the current day or month.

**Reservation**:
A temporary claim on a virtual key's spend, taken before a request is forwarded and replaced by the actual cost once the response ends.
_Avoid_: Hold, pre-authorization

**Unmetered request**:
A request whose model or price kman can't determine. Keys with a spend cap or a model allowlist refuse it.
