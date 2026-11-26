# Finding leaked and exposed keys

Two commands help before a leak and after one. `kman scan` searches files for kman keys, so you find the ones that ended up in a repository or a log. `kman exposure` lists the keys that would be worth the most to a thief, with the command that fixes each.

## `kman scan`

```sh
kman scan path/to/repo ~/.config /var/log/myapp
git log -p | kman scan -
```

It reads every regular file under each path you name (a path of `-` is standard input), and reports each kman key it finds:

```
path/to/repo/deploy/.env:3:16: virtual key kmn_Ab3dE6gH_…
path/to/repo/web/app.js:9:22: child key (short-lived) ek_kmn_Zy9xW8vU_…

2 kman keys found in 214 files.
Each key id above names a key. For each, run kman key show <key id> to see it, and kman key revoke <key id> to end it.
  kman key show Ab3dE6gH
  kman key show Zy9xW8vU
```

- **It works offline.** There is no server to ask and no login: it runs on a laptop or a CI runner that never had kman installed. It cannot say whether a key is still live; the key id it prints is what you look up on the machine that runs kman.
- **A match is confirmed by its checksum.** The [published pattern](../v0.1.0-scope.md#looking-for-leaked-keys) finds anything shaped like a key, and the checksum inside every key (ADR 0001) decides whether it is one. A string that only looks like a key is not reported, and nothing a key's secret could be mistaken for is either.
- **It never prints a secret.** A finding is the file, the line and column, the kind, and the key's prefix and id. The id is not secret: it names the key to revoke. A finding in `--json` holds the same.
- **A child key is lower severity.** A child key (`ek_kmn_…`) lives 60 seconds and one session, so it is reported at `low` severity as "child key (short-lived)"; a virtual key is `high`. Both count as a finding.
- **Admin tokens (`kma_…`) are not matched.** The pattern is for keys an app holds. Look for a leaked admin token with `grep -r kma_`, and rotate it with `kman admin-token rotate`.
- **What it reads.** Every regular file, binary files included (the checksum keeps false matches away). It does not follow symlinks. A file it cannot read is named on standard error, and then the exit code is 1 unless it also found a key: a scan that missed a file did not find nothing. To search history, pipe it in: `git log -p --all | kman scan -`.

`--json` prints `{"scanned": <files>, "findings": [...]}`; each finding has `source`, `line`, `column`, `kind`, `key_id`, `severity`, `label` and `redacted`.

Exit codes: **0** nothing found, **3** a key was found, **1** an error (or a file that could not be read, with nothing found).

Run it in CI to stop a key reaching a repository:

```sh
kman scan . || { echo "a kman key is in this tree"; exit 1; }
```

If it finds a key that was ever live, treat it as leaked: `kman key rotate <key id> --grace 0` (or `kman key revoke <key id>`) ends the old secret, as [Rotating keys](rotation.md) explains.

## Other secret scanners

**No secret scanner detects kman keys by default**, so a key pushed to a public repository is not flagged for you. `kman scan` is the offline check that knows them. If you already run gitleaks, Betterleaks or trufflehog, a config teaches it the [published pattern](../v0.1.0-scope.md#looking-for-leaked-keys): download [gitleaks.toml](../../examples/scanners/gitleaks.toml) or [trufflehog.yaml](../../examples/scanners/trufflehog.yaml) and pass it to the scanner.

```sh
curl -fsSLO https://raw.githubusercontent.com/kiranscript/kman/main/examples/scanners/gitleaks.toml
curl -fsSLO https://raw.githubusercontent.com/kiranscript/kman/main/examples/scanners/trufflehog.yaml

gitleaks git --config gitleaks.toml path/to/repo
betterleaks git path/to/repo --config gitleaks.toml
trufflehog git file://path/to/repo --config=trufflehog.yaml
```

- **gitleaks and Betterleaks** report `kman-virtual-key`, tagged `high`, and `kman-child-key`, tagged `low`. The config keeps their default rules. If you have a config of your own, copy the two `[[rules]]` into it.
- **trufflehog** reports a detector named `CustomRegex`, with `kman-virtual-key` or `kman-child-key` as its name. The finding is always unverified, so a run limited to verified results (`--results=verified`) leaves it out. If you have a config of your own, add the two entries to its `detectors`.
- **These configs do not check the checksum**, so a string that is only shaped like a key is reported too. Run `kman scan` on what they find to confirm it and to get the key id.
- **These scanners print the whole key** in what they report, unlike `kman scan`. Treat their reports and CI logs as secret, or use the scanner's redaction option.

## `kman exposure`

```sh
kman exposure
kman exposure --strict       # exit 3 when there is anything to fix
kman exposure --json
```

It looks at the keys that work now (not disabled, revoked or expired), and lists each one that:

| Finding | It means | The fix it prints |
|---|---|---|
| **open to every address** | The policy has no `allowed_cidrs` and no `allowed_countries`, so a stolen key works from anywhere ([Address locks and GeoIP](geoip.md)). | `kman key update <key> --policy '…'`: the key's own policy with `allowed_cidrs` added. Replace the `<network/bits>` placeholder with where the key is used from; the command is refused until you do. |
| **never expires** | The key has no expiry. kman refuses to make one (`never` is not a TTL), so a key like this did not come from kman's own commands. | `kman key update <key> --ttl <duration>`: the default key TTL, or what is left of the maximum key lifetime. If none is left, `kman key revoke <key>`: create a replacement with `kman key create`. |
| **unused for 30 days** | The key forwarded no request for 30 days, or it has never been used and is older than 30 days (once kman has been recording usage for 30 days; see below). A request kman refused is not a use. | `kman key disable <key>`, which you can undo with `kman key enable`. |

A key that breaks more than one rule is listed once per rule. The last use comes from kman's hourly usage, so it is as precise as an hour.

**Right after an upgrade, a key that was never recorded is not called unused.** Traffic from before kman recorded hourly usage left nothing to find, so a key with no usage record at all is reported as unused only when the key is older than 30 days *and* hourly usage reaches back at least 30 days (some key has a usage row that old). Until then it is left out rather than guessed about, and the finding appears by itself once kman has been recording for 30 days. A key that has a usage record is judged by its last use, as above. If you lower [retention](backup.md#retention) for hourly usage under 30 days, a key with no record is never reported. It needs a running kman and an admin login, like every command except `scan`, and it changes nothing.

`--json` prints the admin API's response: `{"findings": [...], "keys_checked": N}`, each finding with `key_id`, `name`, `rule` (`open_to_every_address`, `never_expires` or `unused`), `message`, `fix` and, for `unused`, `last_used_at`.

Exit codes: **0** after listing, however many findings, and with `--strict` **3** when there is at least one finding and **0** when there are none. Run it from cron:

```sh
kman exposure --strict || mail -s "kman keys to fix" you@example.com
```
