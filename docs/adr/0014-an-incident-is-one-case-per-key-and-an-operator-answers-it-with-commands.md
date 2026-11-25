# An incident is one case per key, and an operator answers it with commands

kman's job is to show who uses a key and where, and to make the response to a leak fast (#83). A detection rule firing, or a canary being used, is the start of that response. This ADR records how the incident that groups those firings works (#88): its states and ids, when it alerts, how quiet and expiry are measured, how an operator answers it, what a known source is, and what it may and may not reveal. The rules that fire detections (location, spike) come in later tickets; this one lands the engine and the commands, and wires canaries into it.

## The incident

- **One unresolved incident per key.** A partial unique index (`state <> 'resolved'`) enforces it. A detection for a key with an unresolved incident belongs to that incident; a detection for a key with none opens a new one, with a new id. A resolved incident is never reopened.
- **Ids are plain integers** (`1`, `2`, ...), never reused (`AUTOINCREMENT`), because an operator types them from an alert into a command (#25). Known-source ids are plain integers for the same reason. Every other id in kman is a prefixed random string; these two are the handles an operator copies by hand, so they are short.
- **States: `open` → `acknowledged` → `resolved`.** A resolved incident carries a resolution: `confirmed`, `false_positive` or `expired`. Quiet is not a state: it is `now - last_fired_at >= 1 h` on an unresolved incident, computed when read.
- **Severity is the alert vocabulary:** `low`, `medium`, `high`, `critical`. The incident's severity is the highest of its detections' and never goes down.
- **A detection is one of three things.** It *opens* the incident when the key has none. It *escalates* it when it has a higher severity than the incident, a rule the incident has not had, or arrives after the incident went quiet (an hour from its last firing). Otherwise it is a *repeat*. A firing on a quiet incident is an escalation whatever its state, acknowledged included.
- **Opens and escalations alert and are audited; repeats only count.** An open and an escalation queue an alert in the same transaction that writes them, and write an `incident.open` or `incident.escalate` audit entry by `system:detect`, carrying the incident id. A repeat increments `detection_count` and moves `last_fired_at` (so an incident that keeps firing is never quiet and never expires), and writes neither.
- **Detection rows are bounded.** Every open, escalation and repeat writes a `detections` row with its evidence, up to 100 per incident; past that a repeat is only counted in `detection_count`, and an escalation is always recorded. A leaked canary hammered a million times is one incident with a counter, not a million rows.
- **Acknowledging stops repeats alerting, and escalations still alert.** That is already the rule above: repeats never alert. What acknowledging changes is that the incident can no longer expire.
- **Expiry is a sweep.** `kman serve` runs `detect.Engine.RunSweep` at startup and every 15 minutes. An incident that is `open` (unacknowledged), not `critical`, and has not fired for 7 days is resolved as `expired` by `system:detect`, with an `incident.expire` audit entry and no alert. Acknowledged and critical incidents never expire. The sweep takes its clock as an argument and its timer is tested in a `synctest` bubble (SPEC §17).
- **Quiet and expiry are fixed.** One hour and seven days are constants (`store.IncidentQuietAfter`, `IncidentExpireAfter`), not settings, as the decision in #15 has them.

## Who writes incidents

`detect` owns the state machine and writes incidents as the actor `system:detect`, in the caller's transaction, with an audit entry for each open, escalation and expiry. It is a second writer beside `admin`, like the canary recording its own use (ADR 0013) and `SettleSecrets`: the open and the alert must commit together with the event that caused it, which a round trip through `admin`'s command pipeline (its own transaction, its own lock) cannot give from the priority lane. Everything an operator does to an incident goes through `admin` as every other command does (ADR 0004): `AckIncident`, `ResolveIncident`, `ThisWasMe`, `AddKnownSource`, `RemoveKnownSource`, and the `--incident` of the containment commands.

`detect` imports `store` only. It reaches the alert outbox through an interface (`detect.Alerter`) that `alerts` implements, and `alerts` reaches the engine for a canary through a function the composition root hands it (`alerts.Options.CanaryIncident`), so the module import rule stays as it was: `alerts` imports `store` and `events`, and neither imports the other.

Nothing in the gateway waits on any of this. A canary's use reaches the engine from the priority lane's subscriber, which is already off the request path; the gateway emits the event and goes on.

## Canaries

A canary's use is a detection of the rule `canary` at severity `critical`, raised in the same transaction that records the use on the canary's row. It opens the canary key's incident, and further uses are repeats that count. The incident carries the address, country, ASN, client and where the canary was planted as evidence.

**The canary's alert stays the canary's alert** (ADR 0013), because its policy differs from an incident's: the first use after a reset alerts even though the incident is the same one, and later uses alert once per window with a held-back count, where an incident's repeats never alert. So the engine is told the detection is `Silent`: it opens the incident and audits it but queues no alert, and the canary's alert names the incident (`incident_id` in a webhook, `kman incident show N` first among its commands). One use is one alert, not two. A canary's incident is critical, so it never expires; it is ended by `kman incident resolve`, or by `kman key revoke <canary> --incident N`.

**A known source never applies to a canary.** `detect.MatchKnownPlace` is the one function that matches a request's origin against known sources, and it takes whether the key is a canary and answers "no" for one. The canary path does not call it at all, and `AddKnownSource` refuses a source scoped to a canary, and `this-was-me` refuses `--add-known-sources` for a canary's incident. The canary's ignore list (ADR 0013) remains the only thing that silences one.

## The response

- **Acknowledge** moves `open` to `acknowledged` and records who and when. Acknowledging twice changes nothing and writes nothing; a resolved incident is a conflict.
- **Resolve** takes `--as confirmed|false-positive`, and **defaults to `confirmed`.** A false positive lets what the incident held out of the baseline back in (a later ticket), so teaching the baseline should be said out loud; "confirmed" drops it. Resolving the same way twice changes nothing; resolving another way is a conflict. `expired` is not a choice; only the sweep sets it.
- **`this-was-me` resolves as a false positive and offers the incident's places as known sources.** The places are the countries, ASNs, clients and networks (the /24 or /64 of an address) its detections saw. It is non-interactive friendly: `--add-known-sources` adds them (`--install-wide` for every key, `--kinds country,asn,client,network` to pick, `--label` to name them), and without the flag a command on a terminal shows them and asks `[y/N]`, while one with no terminal resolves and prints the command to add them (it may be run again: `this-was-me` on an incident already resolved as a false positive adds the sources). The server derives the places from the stored evidence, so the caller never has to be shown an address to add its network.
- **A network is added only with `expose_client_addresses` on.** With it off the admin API does not give out the address or network of evidence, and adding the network would give it out through the list of known sources. So `this-was-me` skips the network, says that one was not added and why, and adds the rest.
- **`--incident <id>` on `key disable|revoke|rotate` and `credential compromise`.** The incident must exist (`not_found`), be about that key (`conflict`; for a compromise, about a key on that credential) and be unresolved (`conflict`); the check runs before anything changes, so a refused command changes nothing. Every audit entry the command writes for the key carries the incident id (for a compromise, the entry of the credential and of the rotation of the incident's key; the rotations of the credential's other keys do not), so the incident's timeline shows it. **Disable leaves the incident open**: it is a pause. **Rotate, revoke and compromise resolve it as `confirmed`**, in the same transaction, with an `incident.resolve` entry carrying the id. Without `--incident` none of them touches an incident.
- **Every state change is audited with the incident id:** `incident.open`, `incident.escalate`, `incident.expire` (by `system:detect`), `incident.acknowledge`, `incident.resolve`, `incident.this_was_me`, and `known_source.create` made for an incident. Incident audit entries made by operators reach the alert outbox through the audit producer like any other (`incident.resolve` and `this_was_me` medium, `acknowledge` low); the engine's own entries do not, since the incident alert is their alert.
- **`kman incidents --follow` polls.** It prints the newest incidents, oldest first, then asks `GET /admin/v1/incidents` every 2 seconds and prints a line when an incident opens, escalates, is acknowledged or is resolved. It does not stream: incidents change rarely, a poll is the same typed client as everything else, and a bus and a stream route would be more machinery than the rate of change needs. A repeat is not a change and prints nothing. With `--open` an incident that is resolved leaves the list, so it is fetched and printed once.

## Known sources

A known source is a labelled **CIDR, country, ASN or client**, for every key (scope `''`) or one key (scope is its key id). A request matches it by kind: a CIDR contains the address (an IPv4 address mapped into IPv6 is that IPv4 address), a country or a client is equal, an ASN is the same number. Its events are recorded and shown as always; it only stops the location rules from raising a new place, and which place depends on the source's kind ([ADR 0015](0015-the-location-rules-run-in-the-transaction-that-records-a-new-place.md)): a **CIDR** containing the request's address silences every new place of that request, a new network included; a **country** silences only a new country, an **ASN** only a new ASN, and a **client** only a new client. Nothing but a CIDR silences a new network. Spike rules still apply. They are for the baseline rules; they never apply to a canary.

They are bounded so that they cannot silence everything, as a canary's ignore list is:

- a CIDR is no wider than an **IPv4 /16 or an IPv6 /32** (the ignore list's bounds), is written canonically (`203.0.113.77/24` is stored as `203.0.113.0/24`), and a bare address is a /32 or /128; an IPv4 network written as IPv6 is refused;
- a country is two capital letters; an ASN is 1 to 4294967295, with or without `AS`;
- a client is a product and a major version as events record it (lower case, at most 32 characters, containing a letter and not an address), and **`none` and `unknown` are refused**: they are what a request that does not say what it is is given, so a known source for them would silence every thief that sends no `User-Agent`;
- at most **200 per scope**, and a label of 1 to 64 characters with no control characters is required;
- the same kind and value twice in one scope is a conflict.

A known source is removed (`known-source rm`), not soft-deleted: the audit entry records what it was, and nothing else refers to its id.

## What the admin API reveals

A client address is personal data (ADR 0010). **Incident and detection evidence withholds the address and the network** unless `expose_client_addresses` is on, as events and canaries do: the evidence says `address_withheld`, the incident detail says `addresses_withheld`, and the places that could become known sources show a network as withheld. The country, the ASN and its organisation, the client and what a rule observed are always given. The incident itself, its rules and its counters hold no address. `Observed`, `Baseline` and `Detail` of evidence are for numbers and names; a rule never puts an address in them, so the one place an address lives is two fields the API knows to withhold (`store.Evidence`).

**An alert to the operator's own channels names the address**, as a canary's does (ADR 0013): the point of an alert is to say who used the key, it goes where the operator sent it, and the operator can turn the channel off. The audit entries of incident commands hold an incident's before and after (state, severity, rules, counters) and never evidence.

## Migration 0002

`incidents`, `detections` and `known_sources` are added to the unreleased `0002` (SPEC §14), with the columns of #25 plus `detection_count` (the counter), the `client` kind of known source, and `acknowledged_by`/`resolved_by` as plain text. A data directory made by an earlier build of `0002` lacks them, and `Open` refuses it with "rebuild the data directory (unreleased migration changed)". `incidents`, `detections` and `known_sources` are kept forever by retention, as the schema ticket has it.

## Considered options

- **Alerting through the incident engine for a canary, and dropping the canary's own alert.** Rejected: the canary's "first use after a reset always alerts, then once per window with a count" is shipped and tested behaviour (ADR 0013), and an incident-driven alert cannot give it (a reset does not change the incident). Two alerts per use would be worse.
- **Putting the engine in `admin`.** Rejected: the open must be in the transaction of the use that caused it, and admin's commands take their own transaction and hold the publishing lock that a stream of canary uses must not wait on.
- **A detection row for every firing, without a bound.** Rejected: a hammered canary writes as fast as it is used, forever, into a table kept forever.
- **A server-sent stream for `--follow`.** Rejected for now (see above); a poll every 2 seconds is the cost, and the typed client is the same.
- **`resolve` defaulting to a false positive.** Rejected: it teaches the baseline.
- **Auto-resolving a key's incident when the key is rotated or revoked without `--incident`.** Rejected: the commands take `--incident` so that a containment is tied to a named incident, and a key can be rotated for a reason that has nothing to do with the open case.
- **Giving out known-source CIDRs only when addresses are exposed.** Rejected: an operator who types a CIDR has it already. `this-was-me` is the one path that derives a network from client data, so it is the one that checks the setting.

## Consequences

- SPEC §9, §10, §12 and §14, the glossary, the changelog, the operator guide and the threat model describe incidents and known sources as built. `kman incidents`, `kman incident` and `kman known-source`, and the `/admin/v1/incidents` and `/known-sources` routes, are in the generated references.
- `kman key disable`, `revoke` and `rotate`, and `kman credential compromise`, gain `--incident`; `ContainKeyRequest` (disable and revoke) and the `incident_id` field of the rotate and compromise requests are in the admin API.
- A canary's incident is created by every use of the canary, not by the first only: a canary used after its incident was resolved opens a new one.
- The location rules (#89, [ADR 0015](0015-the-location-rules-run-in-the-transaction-that-records-a-new-place.md)) raise through the engine with a rule, a severity and evidence (`RaiseGroup` for one request's several), and ask `MatchKnownPlace` before raising; nothing else about incidents changed when they landed.
- Auto-disable and dry-run will set `would_disable_at` and `would_block_count` on the incident, whose columns exist and are unused.
