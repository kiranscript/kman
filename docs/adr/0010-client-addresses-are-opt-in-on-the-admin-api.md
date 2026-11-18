# Client addresses are opt-in on the admin API

Every event records the client address a request came from (SPEC §9), and an operator wants to see it: which addresses used a key, so a leaked key stands out and an address lock can be written from what is real. Showing that means adding a read path to the admin API, which until now gave out no address but the audit log's.

- **The addresses are stored either way; the API gives them out only when `expose_client_addresses` is on.** It is an install setting, off by default, changed with `kman settings set` and audited like every setting, so turning it on is itself a record someone can read. With it off, `GET /admin/v1/keys/{id}/clients` answers 409 and says how to turn it on; the CLI and both terminal UIs show that message in place of the table.
- **The view is grouped by address, not a request log.** One row per address, newest first, at most 50 (the key's total is always given): requests, refused requests, first and last seen, country, ASN and cost. That answers "who is using this key" without handing out a per-request history, and it reads the events table by its `(key_id, ts)` index.
- **It is read-only and has no new write path.** Nothing here changes a key, so ADR 0004's rules hold: the CLI, the dashboard and `tui/` are clients of the same endpoint.

## Considered options

- **Always on.** Rejected. A client address identifies a person or an office, and an admin token that can read keys would then read who used them. The audit log already carries addresses for admin actions; this would add every request's.
- **A flag on each request, or a second admin token scope.** Rejected for now. A token with a narrower scope is a larger feature (scopes exist nowhere else); a setting is one switch for the install and costs nothing when off.
- **A per-request list.** Left for later. The events table can answer it, and it would need paging and its own retention story. The grouped view is the smaller step.

## Consequences

- Turning the setting on does not reveal anything that was not already stored; it only lets the admin API say it. Retention (30 days of events by default) bounds how far back the table reaches. A key's older addresses are gone with the events.
- A request that came in on the Unix socket has no address and is not counted.
- `tui/` and `kman tui` both show the table, so a hand-written list of the endpoint's shape (`tui/src/api.ts`) exists beside the OpenAPI document, as ADR 0009 describes. `tui/test/live.test.ts` checks it against a real kman, with the setting on or off.
