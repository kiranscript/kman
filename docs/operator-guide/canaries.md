# Canary keys

A canary is a key that nobody should ever use. You plant it where a leak would find it: a `.env` file in a repository, an MCP client's config, a shell profile, a CI secret. If it is ever sent to kman, something read the place you planted it, and kman raises a critical alert at once, naming the canary, where you put it, and where the request came from.

A canary has no credential, so kman forwards nothing for it: the request is refused with `invalid_key`, the same answer as for any key that does not work. Nothing in the key says it is a canary (it is `kmn_` and the standard shape, like every key), and neither does the snippet you plant.

## Make one

```sh
kman canary create deploy-env --planted "the deploy repo's .env" --format env --base-url https://kman.example.com/v1
```

It prints, once:

```
OPENAI_BASE_URL=https://kman.example.com/v1
OPENAI_API_KEY=kmn_…
```

Put those lines where you said. The key is never shown again: not by `canary list`, not by `canary show`, not in an alert or the audit log.

| `--format` | What it prints |
|---|---|
| `env` (default) | `OPENAI_API_KEY=kmn_…` lines, for a `.env` file |
| `shell` | `export OPENAI_API_KEY=kmn_…` lines, for a shell profile |
| `mcp` | an `mcpServers` block with an `env` object, to merge into an MCP client's config |
| `ci` | `gh secret set OPENAI_API_KEY --body kmn_…`, for GitHub Actions; other CI systems take the same name and value |

- `--planted <where>` is required. An alert says it, so write what you will want to read when you are woken: "the deploy repo's .env", "the staging runner's secrets".
- `--var` sets the variable the snippet uses (default `OPENAI_API_KEY`). A thief looks for keys by name, so use the name the real key has where you plant it: `ANTHROPIC_API_KEY` in an Anthropic project.
- `--base-url` adds kman's URL to the snippet, as the SDK's `OPENAI_BASE_URL` for `OPENAI_API_KEY`. A canary fires only when it reaches kman, and a thief who finds the key alone does not know where kman is, so either plant it beside the real base URL, or give it here.
- `--tag` labels it, and `--ignore` is the [ignore list](#your-own-scanners).

## See it

```sh
kman canary list
```

```
ID        NAME        STATE  PLANTED                 LAST FIRED         USES  LAST FROM
Q3mXk2aB  deploy-env  fired  the deploy repo's .env  2026-11-05T08:30Z  3     DE AS3320 (Deutsche Telekom) curl/8
```

`STATE` is `armed` until it is used and `fired` after. `LAST FROM` is the country, the AS and the client of the last use, and, only if you have turned on `expose_client_addresses` (`kman settings set expose_client_addresses on`), its address. A client address is personal data, so the admin API leaves it out by default; the canary still says that it fired and when. `kman canary show <canary>` has the rest, `kman events --key <canary>` has every request, and `kman key places <canary>` says where it has been used from.

## When it fires

The alert is critical, so every channel takes it, and it names:

- the canary, by name and key id;
- where you planted it;
- the address the request came from, its country, its AS number and the organisation that holds it, and the client (`curl/8`, `openai-python/3`), as far as kman knows;
- the method, and never the request's path or anything else the request said.

It arrives within 5 seconds under load, even if another channel is not answering (the canary deadline gate).

A canary's use also opens an [incident](incidents.md) for the canary, which the alert names (`kman incident show N`). The incident is critical, so it never expires: more uses count on it, and you end it with `kman incident resolve N`, or with `kman key revoke <canary> --incident N` when you retire the canary because of it.

The **first** use after a canary was armed always alerts. While it stays fired, further uses are counted and alert at most once every 10 minutes, and the next alert says how many it held back (and of which incident, when that alert opens a new one), so a leak that is being hammered is one message and a count, not a flood. Once you have found and fixed the leak:

```sh
kman canary reset deploy-env
```

arms it again, and its next use alerts at once. A reset keeps the history: when it last fired, how many uses were counted, and where the last came from.

## Your own scanners

A secret scanner that sweeps the repository the canary is planted in will read it, and send it to kman if it tests what it finds. Give the canary an ignore list so that your own scanner does not set it off:

```sh
kman canary update deploy-env --ignore 192.0.2.0/24 --ignore 198.51.100.7
```

The list is addresses and networks (CIDR), at most 100, none wider than an IPv4 /16 or an IPv6 /32: a list that held everything would silence the canary. A use from an address on it is still an event, so it is in `kman events`, but it does not fire the canary, count as a use or alert. `--ignore` replaces the list, and `--no-ignore` empties it. **Nothing else silences a canary:** not an address it has been used from before, and not a [known source](incidents.md#known-sources), which never apply to one.

## Keys and canaries

A canary is not a key at risk, so `kman key list` leaves it out, `kman exposure` does not count it, and it is never given an expiry (an expired key answers `key_expired`, and a canary must go on telling you it was used). `kman key show <canary>` says it is one. You end a canary with `kman key revoke <canary>`; it then answers `key_revoked` and raises nothing, and `kman canary list` shows it as revoked. `key disable`, `key enable`, `key rotate` and `key update` refuse a canary and point here, because each would change what its use looks like.

## What a thief learns

The key tells nothing: it has the same shape as every kman key, and its checksum is the same kind. A request with the key id and a wrong secret is an ordinary bad key and raises nothing; the right secret is what fires it, and only the snippet holds that. The snippet holds the key and, if you gave it, kman's address, and not the canary's name, where you planted it or the word canary. The use is answered `invalid_key`, as for any key that does not work, so a thief learns nothing from the response either. They can learn that the key is kman's from its `kmn_` prefix, which kman's [published scanner pattern](scan.md) makes public on purpose.

See [ADR 0013](../adr/0013-a-canary-is-a-key-with-no-credential-and-a-row-of-its-own.md) for why it is built this way.
