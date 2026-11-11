# CLI reference

Generated from the help each command prints; do not edit by hand. Regenerate it with
`go test ./cmd/kman -run CLIReferenceIsCurrent -update`. CI fails when it is stale.

Every command that talks to a running kman takes `--config-dir`, the directory holding the credentials file `kman init` wrote, and every read command takes `--json`, which prints the admin API's response. `KMAN_ADMIN_URL` and `KMAN_ADMIN_TOKEN` override the credentials file. Exit codes: 0 success, 1 an error (usage errors included), 3 findings (not used by any v0.1.0 command).

| Command | What it does |
|---|---|
| [`kman init`](#kman-init) | set up an install: the config, the master key, the data directory and the owner admin token |
| [`kman serve`](#kman-serve) | run the proxy and the admin API |
| [`kman keygen`](#kman-keygen) | print a new master key and store nothing |
| [`kman rekey`](#kman-rekey) | replace the master key and the data key, with the server stopped |
| [`kman version`](#kman-version) | print the version, the commit and the build date |
| [`kman login`](#kman-login) | save an admin URL and token for the commands on this machine |
| [`kman logout`](#kman-logout) | remove the saved admin URL and token |
| [`kman admin-token`](#kman-admin-token) | create, list, rotate or revoke admin tokens |
| [`kman settings`](#kman-settings) | get or set the install's settings |
| [`kman audit`](#kman-audit) | list the audit log, or export it |
| [`kman price`](#kman-price) | list the price table, and set, remove or import price overrides |
| [`kman upstream`](#kman-upstream) | add, list, show, update, remove or sync upstreams |
| [`kman credential`](#kman-credential) | add, update or remove credentials |
| [`kman key`](#kman-key) | create, list, show, update, disable, enable or revoke virtual keys |

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

## `kman version`

Print the version, the commit and the build date.

```
usage: kman version
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
usage: kman key disable <name or key id> [--json]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
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
usage: kman key revoke <name or key id> [--json]
  -config-dir string
    	the config directory holding the credentials file (default "$HOME/.config/kman")
  -json
    	print the admin API's JSON response
```
