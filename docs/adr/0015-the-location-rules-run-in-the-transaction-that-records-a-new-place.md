# The location rules run in the transaction that records a new place

A leaked key turns up somewhere it has never been. #89 is the rules that notice that: after a key's learning period, its first use from a new **country**, **ASN**, **network** or **client** is a detection on its incident (#88, [ADR 0014](0014-an-incident-is-one-case-per-key-and-an-operator-answers-it-with-commands.md)). This ADR records how they are driven, what "new" and "learning" mean, and what they may and may not reveal.

## Driven by the event writer's transaction

- **The place tally already knows what is new.** The event writer adds each batch to `baseline_places` in one transaction (#84). A place the key did not have is an `INSERT`, and that is exactly "new". `AddEventsFresh` returns those inserts (`store.NewPlace`: key, kind, value, label, the first event that showed it, and how many places of that kind the key had before the batch), and the writer's SQLite sink hands them to each `events.Observer` before it commits. `detect.Engine` is the observer: it raises the detections in the same transaction as the places. A place is therefore new **exactly once**, and a crash cannot record the place and lose the detection: both commit, or neither does and the writer retries the whole batch with the place still new.
- **Nothing on the request path waits.** The writer runs on its own goroutine, 5 seconds after the request (SPEC §14); the gateway emits its event and goes on. The detection costs the writer's transaction a few reads and, for a detection, the incident's writes. The dispatcher is woken after the commit (`AfterCommit`).
- **A bus subscriber was rejected.** A subscriber would see an event, ask the store whether the place was new, and race the writer that records it (the subscriber may run before the place is stored, or twice for one place), and it would open a second transaction, so the place and the detection could part. The writer's transaction has the answer for free.
- **`detect` gets no new import rule.** The `Observer` interface lives in `events`, which already imports `store`; `detect` satisfies it structurally, and `cmd/kman` hands the engine to `events.SQLite(st, engine)`. `events` imports no `detect`.
- **An observer's error fails the batch** (the writer retries it, then loses it past its backlog, as for any store failure), so the rules only return an error for the store. What cannot be read (a key's policy that does not parse, a setting that is not a number) is logged and replaced by the default: events are never held up by a policy.

## What "new" means

A place raises a rule when all of these hold:

1. **The key already had a place of that kind** (`Prior > 0`). The first country a key is ever seen from has nothing to be different from. This is not an optimisation: without it, installing a GeoIP database on an install with weeks of events would make the first request of every key "a new country, high", and a client was not recorded before #95, so every upgraded key would raise "new client" once. A kind with no baseline is learnt, not alarmed on. A refused request counts toward a key's places (#93), so a key that was only ever refused from a place of the kind has a baseline of that kind.
2. **It is a standard key.** A canary's use is its own critical rule (ADR 0013, 0014); the engine never raises these for a canary. A key that is gone has no incident to join and is skipped.
3. **The learning period is over at the time of the request.** The period starts at the key's **first forwarded request or realtime session** (not at its creation, and not at a refusal) and lasts 7 days by default. It is stored in `key_learning` (one row per key, set by the same transaction that stores the first forwarded event, and filled by migration `0002` from the events already stored), so it survives the 30-day retention of events. A key that has only been refused has no period: its places are recorded and the rules wait for a first forwarded request. This is a decision, not an accident: nothing is known of how the key is used, and the refusals are in `kman events` and in `kman key places`. The length is the key's policy `learning_period` if it has one, otherwise the install's `learning_period` setting. `0` is allowed for both: no learning period, the rules watch from the first forwarded request on (the first place of each kind is still the baseline).
4. **The key's policy has not turned the rule off** (`"detect": {"new_country": false}`). Rules are on unless a key says otherwise. `canary` cannot be turned off, canaries cannot be updated at all, and the policy refuses a rule that does not exist.
5. **No known source covers the request.** `detect.MatchKnownSource` is called with the request's address, country, ASN and client, once per request, so a CIDR that is a CI runner's network silences a new country from it, and a known client silences the request it calls from (the function's contract in ADR 0014). It is called with `canary=false` because canaries never get here.

The **overflow row** (`PlaceOther`, past 1,000 places of a kind) is not a place: it is not returned as new, so a key used from thousands of clients stops raising `new_client` and still raises the other rules. The 1,000th is the limit of what a key's baseline can tell.

`none` and `unknown` are clients like any other for these rules: a thief who sends no `User-Agent` is a new client for a key whose app sends one. They are refused as *known sources* (ADR 0014) because a source for them would silence all such thieves, which is the opposite decision about the same fact.

A **refused request** counts: its places are recorded (#93) and it raises the rules, with a detail that says it was refused and why, so an address lock that stops a leak still tells the operator.

Severities are fixed, as in #15: `new_country` high, `new_asn` medium, `new_network` low, `new_client` medium.

## One alert for one request

A thief's first request is usually new in several ways at once: a country, its ASN, its network and its client. Four detections are right for the incident's timeline and four alerts would be wrong for the operator. The places of one request are raised as a group (`Engine.RaiseGroup`): each moves the incident as `Raise` does (the incident takes the highest severity and every rule), and the group alerts once, for the highest-severity detection that opened or escalated the incident, naming the incident as it stands after all of them. A group that only repeats alerts nothing. The incident engine's rules are otherwise unchanged: an opening or escalation alerts, a repeat counts.

## What the alert and the evidence say

The detection's evidence is the request's origin (address, network, country, ASN and its organisation, client) and:

- **Observed**: what is new, with its label: `new country DE`, `new ASN AS3320 (Deutsche Telekom AG)`, `new client curl/8`, `new network` (the network itself is `Network`, which the API withholds).
- **Baseline**: the places the key normally uses, of that kind: its three busiest by forwarded requests, and how many more (`US (412 requests), FR (88 requests), and 2 more`). "Normally" means the places it had when it finished learning, so a place an earlier detection added is not usual; a key that had none then is compared with all it has. For a new network, `Baseline` is only a count (`3 usual networks`), because a rule never puts an address in `Baseline`.
- **Usual** (new, `store.Evidence.Usual`): for a new network, the networks themselves. It is an address, so the admin API withholds it with `Address` and `Network` unless `expose_client_addresses` is on ([ADR 0010](0010-client-addresses-are-opt-in-on-the-admin-api.md)), and the alert to the operator's own channel gives it, as it gives the request's address.
- **Detail**: for a refused request, that it was refused and the code.

The alert's layout for a location rule puts the new place first, then "Usually used from", then where the request came from, and its commands are `kman incident show N`, `ack N`, `key disable <key> --incident N`, `key rotate <key> --incident N` and `incident this-was-me N`. All text that came from a database file or a header is cleaned of control characters and bounded before it is stored (`detect`) and again when it is rendered (`alerts`).

## Settings and policy

- **The install's learning period** is the `learning_period` setting (`kman settings set learning_period 7d`, `none` for 0; the admin API's `learning_period_seconds`), 0 to 90 days, default 7 days. It is read by `detect` in the transaction that raises a rule, so a change applies at once, to keys still learning too.
- **A key's policy** takes `"learning_period": "3d"` (also `36h`, `1h30m`, `0s`, stored in its shortest form, at most 90 days) and `"detect": {"new_country": false, "new_asn": false, "new_network": false, "new_client": false}`. Only a rule turned off is kept. A policy that cannot be read when a rule runs is logged and the defaults apply.

## Consequences

- Migration `0002` gains `key_learning`; the stale-schema guard (`unreleased` in `store/migrate.go`) lists it. A data directory made by an earlier build of `0002` is refused with "rebuild the data directory (unreleased migration changed)".
- `store.AddEvents` is `AddEventsFresh` without the result; `events.SQLite(st)` with no observer is unchanged.
- A key's place counted in the learning period is quiet forever after: a thief who is in the baseline (the key leaked during learning) is invisible to these rules. That is inherent to a baseline; canaries and the spike rules (later) are what catch it.
- `kman detection test` (a later ticket) replays the same scenarios: the replay harness feeds a scripted run through the real event sink and the engine on a fake clock, and compares with golden files (`internal/detect/testdata/replay`; `go test ./internal/detect -run Replay -update` rewrites them).
- The baseline-hold-out of SPEC §10 (events in an open incident stay out of the baseline until the incident is resolved) is not part of this ticket: a place an incident's events added is recorded and no longer new. It is not counted as usual in the next alert for the reason above.

## Considered options

- **A detect subscriber on the bus plus a store lookup.** Rejected, above: it races the writer and splits the place from the detection.
- **Deriving the learning start from `baseline_places` (the earliest `first_seen` with `count > 0`).** Rejected: a place's `first_seen` is the earliest of its refused and forwarded events, so a refusal before the first forwarded request would start the clock early, and a place can be pruned from nothing but it cannot be re-derived once events are pruned. A row per key is smaller than the argument.
- **Starting the clock at the key's creation.** Rejected: a key made and not used for a month would be "learned" with no baseline, and the glossary's "after a key is created" is not what #89 says ("its first forwarded request").
- **Raising a kind that has no baseline.** Rejected, above: it floods on a GeoIP database or an upgrade.
- **One alert per detection.** Rejected: one request, four messages.
- **Matching known sources by the kind of the place** (a known country only silences "new country"). Rejected: a CI runner's CIDR is in a new country, a new ASN and a new network at once, and the operator said the whole source is legitimate; ADR 0014 matches a source against the request.
