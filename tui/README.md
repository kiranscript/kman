# kman-tui

A terminal client for a running kman, built with [Ink](https://github.com/vadimdemedes/ink) (React for the terminal). It is a thin client of the admin API. It shows who is using each virtual key and where: the open incidents (a key turning up from somewhere new, or a canary being used), the keys that would be easy to abuse if they leaked, and the countries, networks and clients each key was used from. It also shows each key's spend against its caps, the upstreams and the audit log, and can disable, enable, revoke and create virtual keys. [ADR 0009](../docs/adr/0009-the-tui-is-a-separate-thin-client.md) says why it is a separate package and what it may and may not do.

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

The screen follows [lazygit](https://github.com/jesseduffield/lazygit): numbered panels down the left (Status, Keys, Upstreams, Audit), the details of the selected line in the main panel on the right, and the keys that work right now along the bottom. The focused panel has a green border and is the tall one; the others shrink to the one line that is selected. The selected line is a blue bar, and a panel's bottom border says where it is (`2 of 7`). Below 100 columns the main panel goes under the panels instead of beside them, and on a short terminal (under 30 rows) the panels that are not focused shrink to a single line each, so the detail keeps the room. Status names the install (`owner @ host:port`) in its top border and counts both keys and upstreams that need attention.

## Keys

| Key | Does |
|---|---|
| `1`–`4`, `←` `→`, `h` `l`, `Tab` | focus a panel: Status, Keys, Upstreams, Audit |
| `↑` `↓`, `j` `k`, `PgUp` `PgDn`, `g` `G` | move |
| `Enter` (Status) | open the selected key from Needs attention |
| `Enter` | on Keys, Upstreams or Audit: hand the keyboard to the main panel, so the arrow keys, `PgUp`/`PgDn`, `g`/`G` scroll it; `Esc` gives it back (on Status, `Enter` opens the selected key or upstream) |
| `J` `K` | scroll the main panel without leaving the list, when its content is taller than it (the bottom border says so) |
| `/`, `Esc` | filter keys by name, id, tag or upstream, and clear the filter |
| `n` | create a key |
| `d`, `e` | disable, enable the selected key (asks y/n) |
| `x` | revoke the selected key (asks for its name to be typed) |
| `c` | the addresses the selected key was used from, grouped, newest first (see below) |
| `r` | refresh now (it also refreshes every 3 seconds) |
| `?` | help |
| `q`, `Ctrl+C` | quit |

A created key's secret is shown once, on a screen of its own. Only Enter closes it, `c` asks the terminal to copy it (OSC 52, which not every terminal allows), and it is gone from memory when it is closed. The screen also gives the SDK base URL on the kman host, including the bound proxy port. Older servers only supply the SDK path.

Colour uses the terminal's own 16 colours. `NO_COLOR` turns it off and keeps bold and inverse. Every status has a glyph and a word as well as a colour.

## What needs attention

Status puts the leak first. Its third line counts the open incidents and the keys that are exposed (`✖ 2 open incidents · ▲ 2 exposed`), and Needs attention lists, worst first:

1. **Open incidents**: a key used from a new country, ASN, network or client after its learning period, or a canary being used. High and critical ones are red until someone acknowledges them. A canary is not in the key list, so its incident has a row of its own, and its detail gives the commands that answer it.
2. **Exposed keys**, from `kman exposure`: open to every address, never expiring, or unused for 30 days.
3. **Expiry**: expired, or expiring within 7 days.
4. **Spend**: at or near a cap (with the percentage and whether the cap is daily or monthly), unmetered requests, and last, no cap at all.

With the detail beside the list, an incident or exposure warning has the kman command that answers it under it, such as `kman incident show 12`.

## Where a key is used

A key's detail opens with its warnings and then **Where it is used**: each country, ASN, network and client it was used from, most recently seen first, with when it was last seen and how many requests kman forwarded and refused from there. Refused requests are what a locked key turning up somewhere else looks like, so they are marked. These are the places `kman key places` lists, which kman keeps after the events are pruned. Networks are left out unless `expose_client_addresses` is on, as for the addresses below. After that come the policy (where the key may be used from first), and last the spend: today and this month against the caps, with what is left after charges and in-flight reservations.

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

The server needs one upstream with a credential. The test creates a key, disables, enables and revokes it, and leaves it revoked, so use a throwaway install. CI does exactly that.

For a repeatable operator walkthrough with a disposable install and a local mock provider, use the [facilitator guide](test/usability/README.md) and [participant worksheet](test/usability/worksheet.md).
