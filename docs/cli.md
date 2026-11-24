# CLI reference

Generated from the help each command prints; do not edit by hand. Regenerate it with
`go test ./cmd/kman -run CLIReferenceIsCurrent -update`. CI fails when it is stale.

Every command that talks to a running kman takes `--config-dir`, the directory holding the credentials file `kman init` wrote, and every read command takes `--json`, which prints the admin API's response. `KMAN_ADMIN_URL` and `KMAN_ADMIN_TOKEN` override the credentials file. Exit codes: 0 success, 1 an error (usage errors included), 3 findings: `kman scan` found a key, or `kman exposure --strict` found a key to fix.

| Command | What it does |
|---|---|
| [`kman init`](#kman-init) | set up an install: the config, the master key, the data directory and the owner admin token |
| [`kman serve`](#kman-serve) | run the proxy and the admin API |
| [`kman keygen`](#kman-keygen) | print a new master key and store nothing |
| [`kman rekey`](#kman-rekey) | replace the master key and the data key, with the server stopped |
| [`kman restore`](#kman-restore) | replace the database with a backup, with the server stopped |
| [`kman version`](#kman-version) | print the version, the commit and the build date; --check asks GitHub for a newer release |
| [`kman geoip`](#kman-geoip) | download the DB-IP Lite GeoIP databases that country locks and events read (the one command that reaches DB-IP) |
| [`kman scan`](#kman-scan) | find kman keys in files, offline, and say which key each is |
| [`kman tui`](#kman-tui) | browse keys, exposure, audit and upstreams in a terminal |
| [`kman login`](#kman-login) | save an admin URL and token for the commands on this machine |
| [`kman logout`](#kman-logout) | remove the saved admin URL and token |
| [`kman admin-token`](#kman-admin-token) | create, list, rotate or revoke admin tokens |
| [`kman settings`](#kman-settings) | get or set the install's settings |
| [`kman audit`](#kman-audit) | list the audit log, or export it |
| [`kman incidents`](#kman-incidents) | list incidents, the open case for each key whose detection rules fired, or follow them as they change |
| [`kman incident`](#kman-incident) | show, acknowledge or resolve an incident, or say this was me |
| [`kman known-source`](#kman-known-source) | add, list or remove known sources: networks, countries, ASNs and clients you say are legitimate |
| [`kman events`](#kman-events) | list recent requests through kman, or follow them live: which key, from where, what operation, the status and any refusal |
| [`kman price`](#kman-price) | list the price table, and set, remove or import price overrides |
| [`kman upstream`](#kman-upstream) | add, list, show, update, remove or sync upstreams |
| [`kman credential`](#kman-credential) | add, update or remove credentials |
| [`kman key`](#kman-key) | create, list, show, update, disable, enable or revoke virtual keys |
| [`kman canary`](#kman-canary) | create, list, show, update or reset canary keys, which raise a critical alert when anyone uses them |
| [`kman backup`](#kman-backup) | write a consistent copy of the database, while kman runs |
| [`kman prune`](#kman-prune) | delete events, hourly usage and delivered alerts older than a date, and the audit log's entries too if asked |
| [`kman exposure`](#kman-exposure) | list the keys that would be easy to abuse if they leaked, each with the command that fixes it |
| [`kman channel`](#kman-channel) | add, list, test or remove alert channels |

## `kman init`

Set up an install: the config, the master key, the data directory and the owner admin token.

```
usage: kman init [flags]
  -admin-listen string
    	the admin listener, host:port or unix:/path (default $KMAN_ADMIN_LISTEN, else 127.0.0.1:8081)
  -allow-debug
    	skip the process hardening that keeps the master key out of core dumps and debuggers
  -allow-plain-http
    	let the admin listener serve plain HTTP off loopback (default $KMAN_ALLOW_PLAIN_HTTP, else false)
  -config-dir string
    	the config directory (default ~/.config/kman, or %APPDATA%\kman on Windows)
  -data-dir string
    	the data directory (default ~/.local/share/kman, or %LOCALAPPDATA%\kman on Windows)
  -key-source string
    	where the master key comes from: file, systemd-creds, passphrase or env (default file)
  -no-save-token
    	print the owner admin token without saving it to the credentials file
  -proxy-listen string
    	the proxy listener, host:port (default $KMAN_PROXY_LISTEN, else 127.0.0.1:8080)
```

## `kman serve`

Run the proxy and the admin API.

```
usage: kman serve [flags]
  -allow-debug
    	skip the process hardening that keeps the master key out of core dumps and debuggers
  -config-dir string
    	the config directory holding kman.yaml (default ~/.config/kman, or %APPDATA%\kman on Windows)
```

## `kman keygen`

Print a new master key and store nothing.

```
usage: kman keygen
```

## `kman rekey`

Replace the master key and the data key, with the server stopped.

```
usage: kman rekey [flags]
  -allow-debug
    	skip the process hardening that keeps the master key out of core dumps and debuggers
  -config-dir string
    	the config directory holding kman.yaml (default ~/.config/kman, or %APPDATA%\kman on Windows)
  -new-key-file string
    	a new master key made by kman keygen. Without it, kman makes one for the file and passphrase sources; the others need it
  -new-key-source string
    	where the new master key will come from: file, systemd-creds, passphrase or env (default the current source)
```

## `kman restore`

Replace the database with a backup, with the server stopped.

### `kman restore file`

```
usage: kman restore <file> [--config-dir dir] [--allow-debug]

Replaces the database with a backup made by kman backup. The server must be stopped, and the master key must be the one the backup was made under: it holds ciphertext only. The database it replaces is kept as kman.db.before-restore. What was written after the backup is gone.
  -allow-debug
    	skip the process hardening that keeps the master key out of core dumps and debuggers
  -config-dir string
    	the config directory holding kman.yaml (default ~/.config/kman, or %APPDATA%\kman on Windows)
```

## `kman version`

Print the version, the commit and the build date; --check asks GitHub for a newer release.

```
usage: kman version [--check] [--json]

Prints the version, the commit and the build date. With --check it also asks the GitHub releases API for the latest release, once: the only time this command uses the network. kman never checks by itself unless update_check: true is set in kman.yaml.
  -check
    	ask GitHub whether a newer release exists
  -json
    	print JSON
```

## `kman geoip`

Download the DB-IP Lite GeoIP databases that country locks and events read (the one command that reaches DB-IP).

### `kman geoip update`

```
usage: kman geoip update [--config-dir dir] [--source url]
  -config-dir string
    	the config directory holding kman.yaml (default ~/.config/kman, or %APPDATA%\kman on Windows)
  -source string
    	the address to download DB-IP Lite from, which serves dbip-country-lite-YYYY-MM.mmdb.gz and dbip-asn-lite-YYYY-MM.mmdb.gz: a mirror, for a network that cannot reach DB-IP (default "https://download.db-ip.com/free")
```

## `kman scan`

Find kman keys in files, offline, and say which key each is.

```
usage: kman scan [--json] <path>...

Looks for kman keys in files, and in a directory's files and subdirectories. A path of - reads standard input,
so `git log -p | kman scan -` searches history. It works offline, with no server and no login.

A match is confirmed by its checksum, and a secret is never printed: a finding is the file, the line and the key id.
Exit codes: 0 nothing found, 3 a key was found, 1 an error or a file it could not read.
  -json
    	print the findings as JSON
```

## `kman tui`

Browse keys, exposure, audit and upstreams in a terminal.

```
usage: kman tui [--demo] [--config-dir <dir>]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -demo
    	browse built-in sample data instead of a server's; needs no install or server, and changes nothing
```

## `kman login`

Save an admin URL and token for the commands on this machine.

```
usage: kman login [--admin-url url] [--config-dir dir]

Reads an admin token from the terminal, or from stdin when piped, checks it with the server, and saves both to the credentials file.
  -admin-url string
    	the admin API: http://host:port, https://host:port or unix:/path/to/socket (default "http://127.0.0.1:8081")
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
```

## `kman logout`

Remove the saved admin URL and token.

```
usage: kman logout [--config-dir dir]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
```

## `kman admin-token`

Create, list, rotate or revoke admin tokens.

### `kman admin-token create`

```
usage: kman admin-token create <name> [--json]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

### `kman admin-token list`

```
usage: kman admin-token list [--json]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

### `kman admin-token rotate`

```
usage: kman admin-token rotate <name> [--json]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

### `kman admin-token revoke`

```
usage: kman admin-token revoke <name> [--json]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

## `kman settings`

Get or set the install's settings.

### `kman settings get`

```
usage: kman settings get [<name> ...] [--json]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

### `kman settings set`

```
usage: kman settings set <name> <value> [<name> <value> ...] [--json]

Durations take a number of days (90d) or hours and minutes (36h, 1h30m).
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

## `kman audit`

List the audit log, or export it.

```
usage: kman audit [--limit n] [--json]
       kman audit export [--after id]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
  -limit int
    	how many of the newest entries to list, up to 1000 (default 50)
```

### `kman audit export`

```
usage: kman audit export [--after id]

Writes every audit entry as JSON Lines, oldest first.
  -after int
    	start after this entry id
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

## `kman incidents`

List incidents, the open case for each key whose detection rules fired, or follow them as they change.

```
usage: kman incidents [--open] [--key <name or key id>] [--limit n] [--follow] [--json]

Lists incidents, the newest first: the open case kman keeps for a key when a detection rule fires, with its state, severity, the rules that fired and how many detections it has. A key has at most one unresolved incident. An incident is open until someone acknowledges it (kman incident ack) and resolved by kman incident resolve, this-was-me, or a containment command given --incident. It is quiet after an hour without a firing, and an unacknowledged one quiet for 7 days expires; acknowledged and critical (canary) incidents never do. With --follow it prints the newest incidents, oldest first, and then a line each time an incident opens, escalates (a higher severity or a new rule), is acknowledged or is resolved, until interrupted. A repeat of a rule the incident already has only counts, and is not a change.
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -follow
    	keep printing changes until interrupted
  -json
    	print the admin API's JSON response
  -key string
    	only this key's incidents, by name or key id
  -limit int
    	how many incidents to list, from 1 to 500; with --follow, how many to start from (default 20)
  -open
    	only incidents that are not resolved
```

## `kman incident`

Show, acknowledge or resolve an incident, or say this was me.

### `kman incident show`

```
usage: kman incident show <id> [--json]

Shows an incident: its key, state, severity and rules, each detection with what it saw, and the actions taken, which are its timeline. It also lists the places the incident saw, which kman incident this-was-me can add as known sources.
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

### `kman incident ack`

```
usage: kman incident ack <id> [--json]

Acknowledges an incident: its repeats stop alerting, and an escalation (a higher severity or a new rule) still does.
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

### `kman incident resolve`

```
usage: kman incident resolve <id> [--as false-positive|confirmed] [--json]

Ends an incident. confirmed (the default) says it was real: what it held out of the key's baseline is dropped. false-positive says it was not: that is the operator's call, and kman incident this-was-me also offers to add its places as known sources. Rotating or revoking a key, or a credential compromise, given --incident, resolves it as confirmed.
  -as string
    	how it ended: confirmed or false-positive (default "confirmed")
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

### `kman incident this-was-me`

```
usage: kman incident this-was-me <id> [--add-known-sources] [--install-wide] [--kinds country,asn,client,network] [--label <text>] [--json]

Says you caused the incident: it is resolved as a false positive. With --add-known-sources the places it saw (its countries, ASNs, clients and networks) are also added as known sources, so they stop counting as new: for the key, or for every key with --install-wide; --kinds picks which. On a terminal, without --add-known-sources, kman shows the places and asks. A network is added only with the expose_client_addresses setting on. A known source never silences a canary, so none is added for a canary's incident.
  -add-known-sources
    	also add the places the incident saw as known sources
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -install-wide
    	add them for every key, not just this one
  -json
    	print the admin API's JSON response
  -kinds string
    	add only these kinds, separated by commas: country, asn, client, network
  -label string
    	the label for the known sources (default: says which incident they came from)
```

## `kman known-source`

Add, list or remove known sources: networks, countries, ASNs and clients you say are legitimate.

### `kman known-source add`

```
usage: kman known-source add <cidr|country|asn|client> <value> --label <text> [--key <name or key id>] [--json]

Says a place is legitimate: use from it never counts as a new location. It is labelled, and applies to every key, or to one with --key. cidr is a network such as 203.0.113.0/24, no wider than an IPv4 /16 or an IPv6 /32, or one address; country is an ISO code such as DE; asn is a number such as 3320 or AS3320; client is what kman records for a caller, such as curl/8 (none and unknown are refused). A known source never silences a canary.
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
  -key string
    	apply it to this key only, by name or key id (default: every key)
  -label string
    	what it is, such as "CI runners" (required)
```

### `kman known-source list`

```
usage: kman known-source list [--key <name or key id>] [--json]

Lists the known sources, the ones for every key first. With --key, only those that apply to that key.
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
  -key string
    	only those that apply to this key, by name or key id
```

### `kman known-source rm`

```
usage: kman known-source rm <id> [--json]

Removes a known source: use from that place counts as new again.
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

## `kman events`

List recent requests through kman, or follow them live: which key, from where, what operation, the status and any refusal.

```
usage: kman events [--key <name or key id>] [--refused] [--code <refusal code>] [--since <duration>] [--limit n] [--follow] [--json]

Lists the requests kman saw, the newest first: when, which key, what called it (CLIENT: its User-Agent reduced to a product and major version, such as openai-python/3 or curl/8, or none), from where, what operation and model, the status, and the refusal code of a request kman refused. A request that presented no key kman could identify is listed too, as (no key), so probing shows. It never shows a request's content, a header, a query string or a secret. The ADDRESS is shown only with the expose_client_addresses setting on; country and AS always are. With --follow it prints the last few events, oldest first, and then each new one as it happens, until interrupted. Events are stored in batches every 5 seconds, so the list can trail the live feed; --json prints one event per line with --follow.
  -code string
    	only events with this refusal code, such as invalid_key
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -follow
    	keep printing new events until interrupted
  -json
    	print the admin API's JSON response
  -key string
    	only this key's events, by name or key id
  -limit int
    	how many events to list, from 1 to 1000; with --follow, how many to start from (default 20)
  -refused
    	only requests kman refused
  -since string
    	only events from this long ago, such as 30m, 24h or 7d
```

## `kman price`

List the price table, and set, remove or import price overrides.

### `kman price list`

```
usage: kman price list [--preset name] [--model part-of-id] [--json]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
  -model string
    	only models whose id contains this
  -preset string
    	only this preset's entries, or global for the global ones
```

### `kman price set`

```
usage: kman price set <model> --input dollars --output dollars --max-output tokens [--upstream name] [optional prices] [--json]

Prices are dollars per million tokens. Without --upstream it applies to the whole install. It replaces the model's whole
built-in entry, its long-context bands, service tiers and per-call fees included, until kman price rm removes it.
  -audio-cached value
    	the cached audio input price (default: the audio input price)
  -audio-input value
    	the audio input price (default: the input price)
  -audio-output value
    	the audio output price (default: the output price)
  -cache-write value
    	the cache write price (default: the input price)
  -cache-write-1h value
    	the one-hour cache write price (default: the cache write price)
  -cached value
    	the cached input price (default: the input price)
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -input value
    	the input price (required)
  -json
    	print the admin API's JSON response
  -max-output int
    	the most tokens one response can output, which sizes reservations (required)
  -output value
    	the output price (required)
  -upstream string
    	price it on this upstream only, where the upstream's price multiplier doesn't apply
```

### `kman price rm`

```
usage: kman price rm <model> [--upstream name] [--json]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
  -upstream string
    	remove the override on this upstream, not the install-wide one
```

### `kman price import`

```
usage: kman price import <file|-> [--json]

The file holds {"overrides": [{"model": ..., "input_micro": ..., "output_micro": ..., "max_output": ...}, ...]}, with prices in
microdollars per million tokens, as kman price list --json prints them. It is all or nothing, and overrides it doesn't name stay.
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

## `kman upstream`

Add, list, show, update, remove or sync upstreams.

### `kman upstream add`

```
usage: kman upstream add [<name>] --preset <preset> [settings] [--credential-env VAR | --no-credential] [--json]
       kman upstream add <name> --host <host> --path "METHOD /path" ... [settings] [--credential-env VAR | --no-credential] [--json]

A preset fills in everything, and copies once: a newer kman never changes the upstream, kman upstream sync does.

The secret is asked for without echo, or read as one line of stdin when piped, or from the variable --credential-env names. It is never an argument, which would end up in shell history and process lists.
  -basic
    	present the secret as HTTP Basic auth, in the form user:password
  -basic-username string
    	present the secret as the password of HTTP Basic auth with this username
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -credential-env string
    	read the secret from this environment variable
  -credential-name string
    	the credential's name (default the upstream's)
  -header string
    	present the secret in a header, as "Name: value {key}"
  -host string
    	the host name or IP address
  -inject-usage
    	ask OpenAI-style streams for their usage
  -json
    	print the admin API's JSON response
  -no-credential
    	add the upstream with no credential, to add one later
  -no-inject-usage
    	leave OpenAI-style streams as they are
  -no-tls
    	connect over plain HTTP
  -path value
    	allow a route, as "METHOD /path", repeated: * is any method or one segment, and a last segment of ** is the rest of the path
  -port int
    	the port (default 443 with TLS, 80 without)
  -preset string
    	the preset to copy
  -price-multiplier string
    	what built-in prices and install-wide overrides are multiplied by here, such as 1.1 for a regional uplift (default 1)
  -profile string
    	openai, anthropic or generic (the default for a custom upstream)
  -sdk-base-path string
    	appended to kman's proxy URL to make an SDK's base URL
  -sdk-env string
    	the variable an SDK reads its key from
  -server-name string
    	the TLS server name, when it is not the host
  -tls
    	connect over TLS (the default)
```

### `kman upstream list`

```
usage: kman upstream list [--json]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

### `kman upstream show`

```
usage: kman upstream show <name> [--json]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

### `kman upstream update`

```
usage: kman upstream update <name> [--name new] [settings] [--json]

Only the flags given change. --path replaces the whole allowlist.
  -basic
    	present the secret as HTTP Basic auth, in the form user:password
  -basic-username string
    	present the secret as the password of HTTP Basic auth with this username
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -header string
    	present the secret in a header, as "Name: value {key}"
  -host string
    	the host name or IP address
  -inject-usage
    	ask OpenAI-style streams for their usage
  -json
    	print the admin API's JSON response
  -name string
    	a new name
  -no-inject-usage
    	leave OpenAI-style streams as they are
  -no-tls
    	connect over plain HTTP
  -path value
    	allow a route, as "METHOD /path", repeated: * is any method or one segment, and a last segment of ** is the rest of the path
  -port int
    	the port (default 443 with TLS, 80 without)
  -price-multiplier string
    	what built-in prices and install-wide overrides are multiplied by here, such as 1.1 for a regional uplift (default 1)
  -profile string
    	openai, anthropic or generic (the default for a custom upstream)
  -sdk-base-path string
    	appended to kman's proxy URL to make an SDK's base URL
  -sdk-env string
    	the variable an SDK reads its key from
  -server-name string
    	the TLS server name, when it is not the host
  -tls
    	connect over TLS (the default)
```

### `kman upstream rm`

```
usage: kman upstream rm <name> [--json]

Removes its credentials and price overrides too. Refused while a virtual key that is not revoked depends on one.
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

### `kman upstream sync`

```
usage: kman upstream sync <name> [--yes] [--json]

Shows how the upstream differs from the newer preset this kman holds, and applies that after a confirmation.
This replaces any edit made since the last copy. --json prints the plan and applies nothing, unless --yes is given too.
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
  -yes
    	apply without asking
```

## `kman credential`

Add, update or remove credentials.

### `kman credential add`

```
usage: kman credential add <name> --upstream <upstream> [--credential-env VAR] [--json]

The secret is asked for without echo, or read as one line of stdin when piped, or from the variable --credential-env names. It is never an argument, which would end up in shell history and process lists.
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -credential-env string
    	read the secret from this environment variable
  -json
    	print the admin API's JSON response
  -upstream string
    	the upstream's name or id
```

### `kman credential update`

```
usage: kman credential update <name> [--name new] [--secret] [--credential-env VAR] [--json]

With no --name, it replaces the secret. The credential keeps its id, so the virtual keys on it are not touched.

The secret is asked for without echo, or read as one line of stdin when piped, or from the variable --credential-env names. It is never an argument, which would end up in shell history and process lists.
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -credential-env string
    	read the secret from this environment variable
  -json
    	print the admin API's JSON response
  -name string
    	a new name
  -secret
    	replace the secret too, when also renaming
```

### `kman credential rm`

```
usage: kman credential rm <name> [--json]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

### `kman credential compromise`

```
usage: kman credential compromise <name> [--credential-env VAR] [--incident <id>] [--json]

For a real credential that leaked. First revoke it at the provider and make a new one there: that is what stops the damage. Then this replaces the credential's secret and rotates every virtual key on it at once, with no grace, and lists the apps that need new keys. The credential keeps its id; the keys keep theirs, with their spend, policy and history. With --incident the compromise and the rotation of the incident's key are recorded on that incident's timeline, and it is resolved as confirmed.

The secret is asked for without echo, or read as one line of stdin when piped, or from the variable --credential-env names. It is never an argument, which would end up in shell history and process lists.
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -credential-env string
    	read the secret from this environment variable
  -incident int
    	the incident this is done for, by id: it must be unresolved and about a key on this credential
  -json
    	print the admin API's JSON response
```

## `kman key`

Create, list, show, update, disable, enable or revoke virtual keys.

### `kman key create`

```
usage: kman key create [<name>] --upstream <upstream> [--credential <credential>] [--ttl <duration>] [--note <text>] [--tag <tag>]... [--policy <json>] [--json]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -credential string
    	the credential's name or id, when the upstream has more than one
  -json
    	print the admin API's JSON response
  -note string
    	free text about the key
  -policy string
    	the key's policy as JSON (default: the empty policy)
  -tag value
    	a tag; repeat it for several
  -ttl string
    	how long the key stays valid, such as 90d or 36h (default: the default_key_ttl setting)
  -upstream string
    	the upstream's name or id (required)
```

### `kman key list`

```
usage: kman key list [--json]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

### `kman key show`

```
usage: kman key show <name or key id> [--json]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

### `kman key places`

```
usage: kman key places <name or key id> [--kind country|asn|network|client] [--json]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
  -kind string
    	only places of this kind: country, asn, network or client
```

### `kman key clients`

```
usage: kman key clients <name or key id> [--json]

Lists the client addresses a key was used from, the most recently seen first, with what each did. It needs the expose_client_addresses setting: kman settings set expose_client_addresses true.
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

### `kman key update`

```
usage: kman key update <name or key id> [--name <name>] [--note <text>] [--tag <tag>]... [--no-tags] [--ttl <duration>] [--policy <json>] [--json]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
  -name string
    	a new name, or empty for none
  -no-tags
    	remove every tag
  -note string
    	a new note, or empty for none
  -policy string
    	the new policy as JSON, which replaces the old one
  -tag value
    	a tag; repeat it for several
  -ttl string
    	a new expiry, this long from now, such as 90d or 36h
```

### `kman key disable`

```
usage: kman key disable <name or key id> [--incident <id>] [--json]

With --incident the action is recorded on that incident's timeline, and the incident stays open: disabling is a pause, and kman incident resolve ends it.
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -incident int
    	the incident this is done for, by id: it must be about this key and unresolved
  -json
    	print the admin API's JSON response
```

### `kman key enable`

```
usage: kman key enable <name or key id> [--json]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

### `kman key revoke`

```
usage: kman key revoke <name or key id> [--incident <id>] [--json]

With --incident the action is recorded on that incident's timeline and the incident is resolved as confirmed.
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -incident int
    	the incident this is done for, by id: it must be about this key and unresolved
  -json
    	print the admin API's JSON response
```

### `kman key rotate`

```
usage: kman key rotate <name or key id> [--grace <duration>] [--incident <id>] [--json]

Replaces the key's secret and keeps the key: its id, spend, policy and history stay. The new secret is printed once. With --grace the old secret keeps working that long, at most 30 days, so an app can be moved over without a gap; with none, the old secret stops at the next request. A second rotation inside a grace ends the older secret at once. With --incident the rotation is recorded on that incident's timeline and resolves it as confirmed.
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -grace string
    	how long the old secret keeps working, such as 24h; 0 ends it at once (default "0")
  -incident int
    	the incident this is done for, by id: it must be about this key and unresolved
  -json
    	print the admin API's JSON response
```

## `kman canary`

Create, list, show, update or reset canary keys, which raise a critical alert when anyone uses them.

### `kman canary create`

```
usage: kman canary create [<name>] --planted <where> [--format env|mcp|shell|ci] [--var <NAME>] [--base-url <url>] [--tag <tag>]... [--ignore <address>]... [--json]

Makes a canary: a key in the standard format that forwards nothing and raises a critical alert when anyone uses it.
It prints the snippet to plant, once. Plant it where a leak would find it, and say where with --planted so the alert can.
  -base-url string
    	kman's proxy URL, so the snippet carries it and a thief who finds only the snippet can reach kman
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -format string
    	the snippet to plant: env (a .env line), mcp (an MCP server config's env block), shell (an export line) or ci (a gh secret set command) (default "env")
  -ignore value
    	an address or network (CIDR) whose use does not fire the canary, such as your own scanner; repeat it for several
  -json
    	print the admin API's JSON response
  -planted string
    	where it will be planted, such as "the deploy repo's .env" (required)
  -tag value
    	a tag; repeat it for several
  -var string
    	the variable the snippet sets (default OPENAI_API_KEY)
```

### `kman canary list`

```
usage: kman canary list [--json]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

### `kman canary show`

```
usage: kman canary show <name or key id> [--json]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

### `kman canary update`

```
usage: kman canary update <name or key id> [--planted <where>] [--tag <tag>]... [--no-tags] [--ignore <address>]... [--no-ignore] [--json]

--tag and --ignore replace the canary's tags and its ignore list.
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -ignore value
    	an address or network (CIDR) whose use does not fire the canary; repeat it for several
  -json
    	print the admin API's JSON response
  -no-ignore
    	empty the ignore list
  -no-tags
    	remove every tag
  -planted string
    	a new place it was planted
  -tag value
    	a tag; repeat it for several
```

### `kman canary reset`

```
usage: kman canary reset <name or key id> [--json]

Arms a canary again after it fired, so its next use raises an alert.
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

## `kman backup`

Write a consistent copy of the database, while kman runs.

### `kman backup file`

```
usage: kman backup <file>

Writes a consistent copy of the database while kman runs. The copy holds ciphertext only: it opens only with the master key, which is not in it, so back the master key up separately and keep it apart from the copy. The file is made here, owner-only, and must not exist. Restore it with kman restore, with the server stopped.
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
```

## `kman prune`

Delete events, hourly usage and delivered alerts older than a date, and the audit log's entries too if asked.

```
usage: kman prune --before <date> [--audit-log] [--json]

Deletes the events, the hourly usage and the alerts delivered before the date. Daily spend, key places and incidents are kept forever. With --audit-log it also deletes the audit log's entries before the date, the one thing that ever deletes from the log; the prune itself is recorded in the log, and that record stays. Retention does the same for events, hourly usage and alerts by itself (see retention in kman.yaml); this is for a date you choose.
  -audit-log
    	also delete the audit log's entries before the date
  -before string
    	delete what is older than this date: 2026-01-01 (midnight UTC) or 2026-01-01T15:04:05Z
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

## `kman exposure`

List the keys that would be easy to abuse if they leaked, each with the command that fixes it.

```
usage: kman exposure [--strict] [--json]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
  -strict
    	exit 3 when there are findings
```

## `kman channel`

Add, list, test or remove alert channels.

### `kman channel add`

```
usage: kman channel add <name> --kind slack|webhook [--url-env VAR] [--min-severity medium] [--lifecycle-alerts=false] [--json]

The address is asked for without echo, or read as one line of stdin when piped, or from the variable --url-env names. It is never an argument, which would end up in shell history and process lists, and a Slack address is itself a secret. kman seals it and never shows it again. A webhook also gets a signing secret, printed once.
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
  -kind string
    	slack, or webhook for a signed webhook
  -lifecycle-alerts
    	also take spend and lifecycle warnings (default true)
  -min-severity string
    	the lowest severity the channel takes: low, medium (the default), high or critical
  -url-env string
    	read the address from this environment variable
```

### `kman channel list`

```
usage: kman channel list [--json]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

### `kman channel test`

```
usage: kman channel test <name> [--json]

Sends one test alert to the channel now, whatever its minimum severity.
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```

### `kman channel rm`

```
usage: kman channel rm <name> [--json]

Removes the channel, wipes its address, and gives up the alerts still waiting for it.
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```
