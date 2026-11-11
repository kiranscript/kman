# Quickstart

From nothing to a working, metered virtual key, in about five minutes. You need Linux or macOS, a terminal, and an API key from OpenAI. (Windows, Docker, packages and other providers: see [install](operator-guide/install.md) and [SDKs](sdks.md).)

kman holds your real API key. Your apps get a **virtual key** instead: one you can cap, watch and kill without touching the real one.

## Set up

Run these in order. `kman serve` stays in the foreground, so open a second terminal for the lines after it.

<!-- quickstart:begin -->

```sh
curl -fsSL https://raw.githubusercontent.com/kirank55/kman/main/install.sh | sh
kman init
kman serve
kman upstream add openai --preset openai
kman key create my-app --upstream openai
export OPENAI_BASE_URL=http://127.0.0.1:8080/v1
```

<!-- quickstart:end -->

What each line does:

1. **`install.sh`** downloads the latest release for your machine, checks its SHA-256, and verifies its signature if `cosign` or `gh` is installed. It installs `kman` to `/usr/local/bin` if you can write there, otherwise to `~/.local/bin`, and tells you if that is not on your `PATH`. Without `cosign` or `gh` it warns that the signature was not checked and carries on; read [Verify a kman release](verify.md) to check it yourself.
2. **`kman init`** asks nothing. It writes `kman.yaml` and a master key (`0600`) to `~/.config/kman/`, creates the data directory `~/.local/share/kman/`, and prints the **owner admin token** once. It also saves that token to `~/.config/kman/credentials.json` (`0600`), so the commands on this machine can use it. **Back up `master.key` apart from the data directory**: without it nothing in the data directory can be decrypted.
3. **`kman serve`** starts kman. The proxy listens on `127.0.0.1:8080` and the admin API on `127.0.0.1:8081`, and both stay on your machine.
4. **`kman upstream add openai --preset openai`** adds OpenAI as an upstream, and asks for your real OpenAI API key. Type or paste it; nothing is echoed. kman seals it under the master key and never shows it again.
5. **`kman key create my-app --upstream openai`** makes a virtual key for an app. It prints the key once, with a ready-made `export OPENAI_API_KEY=kmn_…` line. **Copy it now**: kman stores only a hash and cannot show it again. The key expires in 90 days by default (`--ttl` changes that).
6. **`export OPENAI_BASE_URL=…`** points the OpenAI SDK at kman. Paste the `export OPENAI_API_KEY=…` line from the previous step too. Any code that uses the official OpenAI SDKs now talks to kman, with the virtual key in place of the real one and no other change. [SDKs](sdks.md) has snippets for curl, Python and Node, and for Anthropic.

Plain `kman`, with no arguments, says where the install stands and names the next command at every stage. After the last step it says `kman is ready`.

## Use it

```sh
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Say hello"}]}'
```

kman checks the key, forwards the request to OpenAI with your real key, and passes the answer back as it arrives, streams included. It reads the response's usage as it passes and charges it to the virtual key.

## Cap it, watch it, kill it

<!-- quickstart-next:begin -->

```sh
kman key update my-app --policy '{"daily_cap_micro":5000000}'
kman key show my-app
kman key revoke my-app
```

<!-- quickstart-next:end -->

- The first line caps the key at $5 a day. Caps are in microdollars (a millionth of a dollar). A request that would take the key over its cap is refused with `spend_cap_reached`; a key goes over by at most one request's reservation. Periods begin at 00:00 UTC, set by the `spend_timezone` setting.
- `kman key show` prints the key's status, policy, expiry, and what it has spent today and this month.
- `kman key revoke` kills the key, and it takes effect at the key's next request, with no restart. A revoked key is final: to replace one, create a new key and give it to the app, then revoke the old one. `kman key disable` and `kman key enable` pause and resume a key instead.

More on keys, caps, allowlists and rate limits is in [Policy](policy.md).

## Something wrong?

| You see | It means |
|---|---|
| `kman is not running` | `kman serve` is not running. Start it in another terminal. |
| `kman: command not found` | `install.sh` told you where it put `kman`; add that directory to your `PATH`. |
| `invalid_key` (401) | The app is not sending the virtual key `kman key create` printed, or the key is mistyped. |
| `upstream_unavailable` (502) | kman could not reach the upstream. Check the machine's network. |
| An error from OpenAI itself | The real key you entered is wrong. Replace it with `kman credential update openai`. |

## Where next

- [Install](operator-guide/install.md) for other platforms and package managers.
- [Key sources](operator-guide/key-sources.md), [systemd](operator-guide/systemd.md) and [Docker](operator-guide/docker.md) for a real deployment, and [TLS behind a reverse proxy](operator-guide/tls.md).
- [What v0.1.0 leaves out](v0.1.0-scope.md), and what to do about it.
- [CLI reference](cli.md) and [admin API reference](admin-api/README.md).
