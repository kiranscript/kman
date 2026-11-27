# kman-tui

A terminal client for a running kman, built with [Ink](https://github.com/vadimdemedes/ink) (React for the terminal). It is a thin client of the admin API. It shows who is using each virtual key and where: the open incidents (a key turning up from somewhere new, or a canary being used), the keys that would be easy to abuse if they leaked, and the countries, networks and clients each key was used from. It also shows the recent requests with the suspicious ones flagged, each key's spend against its caps, the upstreams and the audit log, and can disable, enable, revoke and create virtual keys, and answer an incident: acknowledge it, say it was you, resolve it, contain its key, or arm its canary again. [ADR 0009](../docs/adr/0009-the-tui-is-a-separate-thin-client.md) says why it is a separate package and what it may and may not do.

It does not run the server. Start kman first (`kman serve`), then this.

## Run it

It needs Node 22 or later and a terminal of at least 80×24.

```sh
cd tui
npm ci
npm run build
node dist/cli.js
```

Or, without building, `npm run dev`. `--config-dir <dir>` points it at another install.

It finds the admin address and token the way the kman CLI does: the credentials file in the config directory (written by `kman init` or `kman login`), overridden by `KMAN_ADMIN_URL` and `KMAN_ADMIN_TOKEN`. The admin address can be `http://`, `https://` or `unix:/path/to/socket`. It talks only to that address and sends nothing anywhere else.

## Layout

The screen follows [lazygit](https://github.com/jesseduffield/lazygit): numbered panels down the left (Status, Keys, Upstreams, Audit, Requests), the details of the selected line in the main panel on the right, and the keys that work right now along the bottom. The focused panel has a green border and is the tall one; the others shrink to the one line that is selected. The selected line is a blue bar, and a panel's bottom border says where it is (`2 of 7`). Below 100 columns the main panel goes under the panels instead of beside them, and on a short terminal (under 30 rows) the panels that are not focused shrink to a single line each, so the detail keeps the room. Status says how many incidents are open in its title (`2 incidents`) and names the install (`owner @ host:port`) in its top border; its bottom border counts the keys, upstreams and incidents that need a look. The left column is as wide as its lists need (44 to 48 cells), and the rest goes to the main panel. In Keys, `active` is dim, so disabled, revoked and expired keys stand out.

## Keys

| Key | Does |
|---|---|
| `1`–`5`, `←` `→`, `h` `l`, `Tab` | focus a panel: Status, Keys, Upstreams, Audit, Requests |
| `↑` `↓`, `j` `k`, `PgUp` `PgDn`, `g` `G` | move |
| `Enter` | on a row marked as an incident, in Status or Keys: open the incident (see [Answering an incident](#answering-an-incident)) |
| `Enter` (Status) | open the selected key from Needs attention |
| `Enter` | on any other row of Keys, Upstreams or Audit: hand the keyboard to the main panel, so the arrow keys, `PgUp`/`PgDn`, `g`/`G` scroll it; `Esc` gives it back (on Status, `Enter` opens the selected key or upstream) |
| `J` `K` | scroll the main panel without leaving the list, when its content is taller than it (the bottom border says so) |
| `/`, `Esc` | filter keys by name, id, tag or upstream, and clear the filter |
| `n` | create a key |
| `d`, `e` | disable, enable the selected key (asks y/n) |
| `x` | revoke the selected key (asks for its name to be typed) |
| `v` | on a key, in Keys or Status: its requests (see [Recent requests](#recent-requests)) |
| `c` | the addresses the selected key was used from, grouped, newest first (see below) |
| `r` | refresh now (it also refreshes every 3 seconds) |
| `?` | help |
| `q`, `Ctrl+C` | quit |

A created key's secret is shown once, on a screen of its own. Only Enter closes it, `c` asks the terminal to copy it (OSC 52, which not every terminal allows), and it is gone from memory when it is closed. The screen also gives the SDK base URL on the kman host, including the bound proxy port. Older servers only supply the SDK path.

Colour uses the terminal's own 16 colours. `NO_COLOR` turns it off and keeps bold and inverse. The text on the selected line is drawn in the default foreground, so it stays readable on the bar. Every status has a mark and a word as well as a colour:

| Mark | Means | Mark | Means |
|---|---|---|---|
| `!` | danger | `●` | active |
| `▲` | warning | `○` | disabled |
| `·` | note | `×` | revoked |
| `✓` | ok | `◆` | new audit entry |

`◌` in the footer means not connected. None of these marks is a character that a terminal may draw as a two-column emoji.

## What needs attention

Status puts the leak first. Its summary counts the open incidents and the keys that are exposed (`! 2 open incidents · ▲ 2 exposed`), and Needs attention lists, worst first:

1. **Open incidents**, whatever their colour: a key used from a new country, ASN, network or client after its learning period, or a canary being used. High and critical ones are red until someone acknowledges them, and come before the others. A canary is not in the key list, so its incident has a row of its own, and its detail gives the commands that answer it.
2. **Whatever else is red**: an expired key, a key at its cap, an upstream with no credential.
3. **Exposed keys**, from `kman exposure`: open to every address, never expiring, or unused for 30 days.
4. **Expiry** within 7 days.
5. **Spend**: near a cap (with the percentage and whether the cap is daily or monthly), unmetered requests, and last, no cap at all.

If incidents or exposure fail to load, the TUI keeps the last answer and marks it old, or says it is unknown. It never reports that there is nothing to review while it cannot tell. A kman too old to have them leaves them out.

With the detail beside the list, an incident or exposure warning has the kman command that answers it under it, such as `kman incident show 12`.

## Answering an incident

`Enter` on a row marked as an incident, in Status or Keys, opens it in the main panel (below 100 columns it takes the whole body), and `Esc` goes back. In Keys, `J` and `K` still scroll the key's detail. The view shows the incident's severity and state, what fired (what was seen against what is usual, and whether the request was refused), where the requests came from, whether addresses were withheld, and the timeline of actions. `↑` `↓`, `j` `k`, `PgUp` `PgDn` and `g` `G` scroll it. Opening an incident never acknowledges it.

| Key | Does | Asks first | The incident |
|---|---|---|---|
| `a` | acknowledge (while open) | nothing | stays unresolved |
| `m` | this was me | which kinds of place to add as known sources, none ticked, and for this key or every key | resolved as a false positive |
| `R` | resolve | confirmed or false positive, with no default, then y/N | resolved |
| `d`, `e` | disable, enable the key | y/N | stays open |
| `o` | rotate the key, with no grace, and show the new secret once | the key's name | resolved as confirmed |
| `x` | revoke the key | the key's name | resolved as confirmed |
| `z` | arm a canary again | y/N | unchanged |

A canary offers only acknowledge, resolve and reset. A fired canary stays silent until it is reset, so reset is still offered once its incident is resolved. "This was me" picks places by kind (country, ASN, client, network), so a ticked box adds every value of its kind; if the incident sees something else while the dialog is open, the boxes are unticked and it asks again. With nothing ticked no known source is added. A network cannot be added while `expose_client_addresses` is off.

Every dialog shows the kman command that does the same. Disabling, rotating and revoking from this view are audited with the incident's id; kman records no incident on an enable or a canary reset. `d` and `x` in the Keys panel are not linked to an incident ([ADR 0014](../docs/adr/0014-an-incident-is-one-case-per-key-and-an-operator-answers-it-with-commands.md)).

Action keys are ignored for a moment after the view or one of its dialogs appears. They are off while kman does not answer, and the footer says why. If someone else resolves the incident, the TUI says who, closes the dialog that no longer applies, and keeps the view open.

## Where a key is used

A key's detail opens with its unresolved incident, if it has one (`! incident #12 · Enter to answer`, with the kman command under it), then its status and other warnings and then **Where it is used**: each country, ASN, network and client it was used from, most recently seen first, with when it was last seen and how many requests kman forwarded and refused from there. An AS's organisation is cut only when even a line of its own is too short for it. Refused requests are what a locked key turning up somewhere else looks like, so they are marked. These are the places `kman key places` lists, which kman keeps after the events are pruned. Networks are left out unless `expose_client_addresses` is on, as for the addresses below. After that come the policy (where the key may be used from first), and last the spend: today and this month against the caps, with what is left after charges and in-flight reservations.

## Recent requests

`5` shows the requests through kman, newest first, from the events it keeps: time, key, country and AS number, client and result, with the incident's number where the request is flagged for one. A wider screen adds the AS's organisation, the operation and the model; as it narrows the model goes first, then the operation, then the organisation. A client address column appears only while `expose_client_addresses` is on. A request that presented no key kman knows is `(no key)`, with no country or AS. Under the selected row are its whole operation and model, and why it is flagged. Below a table 110 columns wide the panels go above it, a line each, so it has the whole width.

| Key | Does |
|---|---|
| `↑` `↓`, `j` `k`, `PgUp` `PgDn` | move; past the end loads older requests, up to 5,000 held (`kman events` lists older ones) |
| `g`, `G` | the newest; the oldest loaded |
| `f` | all, flagged (`!` or `▲`), refused |
| `Enter` | open the request's incident, or else its key |
| `Esc` | from one key's requests (`v`), back to all keys |

New requests arrive on the 3-second poll, about 5 seconds after they are made. The selection stays on the same request as they do, and the title says how many are new above it. A key's detail lists its last five. If the requests stop loading, the rows are kept and marked old. A kman without the events route has no panel.

### What the flags mean

Events carry no flag, so the TUI works it out:

- `!` the request used a canary key, or its key has an unresolved incident, it is no older than the incident, and it looks like one of its detections: every one of country, AS number and client that the detection recorded is the same, what was new among them. A new network also needs the address to be in that network, which can only be told with `expose_client_addresses` on. A detection that recorded only a client counts only for a new client.
- `▲` otherwise, the request was refused, or presented no key kman knows.

What the flags cannot tell:

- Your own traffic is flagged if it looks the same as the suspicious request.
- Nothing the rules never raised is flagged: what a key did while learning, or from a known source.
- An incident's flags go when it is resolved.
- An incident's detail lists at most 100 detections, and only those are compared.

## Which addresses used a key

`c` on a key opens a table of the client addresses it was used from: requests, refused requests, when each was last seen, country and ASN (when kman has GeoIP databases), and cost, from the events kman keeps (30 days by default). It is a read, so it works while a change is running, but not while kman is unreachable.

A client address is personal data, so the admin API gives them out only when the install turns them on. Until then the dialog says so and shows the command:

```sh
kman settings set expose_client_addresses on
```

Each change of that setting is audited. See [ADR 0010](../docs/adr/0010-client-addresses-are-opt-in-on-the-admin-api.md).

## Test it

```sh
npm run typecheck
npm test
```

The tests run against a fake admin API and need no server. One file, `test/live.test.ts`, runs the client against a real kman and is skipped unless it is pointed at one, which checks that the types in `src/api.ts` still match the real API:

```sh
KMAN_TUI_LIVE_URL=http://127.0.0.1:8081 KMAN_TUI_LIVE_TOKEN=kma_… npm test
```

The server needs one upstream with a credential. The test creates a key, disables, enables and revokes it, and leaves it revoked. To check incidents it also adds an upstream of its own on loopback and raises incidents on new keys and a canary, so it must run on the same machine as kman. Use a throwaway install. CI does exactly that.

For a repeatable operator walkthrough with a disposable install and a local mock provider, use the [facilitator guide](test/usability/README.md) and [participant worksheet](test/usability/worksheet.md).
