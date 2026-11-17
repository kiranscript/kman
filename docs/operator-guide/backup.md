# Backup, restore, retention and updates

The day-two commands: `kman backup`, `kman restore`, `kman prune`, `retention` in `kman.yaml`, and the opt-in update check. [The CLI reference](../cli.md) has every flag.

## Back up

```sh
kman backup /safe/place/kman-2026-11-11.db      # while kman serve is running
```

`kman backup <file>` asks the running server for a consistent copy of the database, a SQLite `VACUUM INTO`: one snapshot, never half of a transaction, and the server keeps serving while it is made. The file is written by the command, on the machine you run it on (so it works against a remote admin URL and from `docker compose exec`), owner-only (`0600`), and **must not exist**: backup never overwrites a file. Each backup is recorded in the audit log (`backup.create`).

**A backup holds ciphertext only.** The credentials in it are sealed under the data key, and the data key is wrapped under the master key, which is not in it. So:

- the backup opens **only with the master key it was made under**, and a copy of the backup without the master key is useless to whoever steals it;
- the master key is **not** backed up by this command, and must be backed up separately, somewhere apart from the backups. [Key sources](key-sources.md) says how for each source. A backup stored beside its master key defeats the point.

Run it from cron or a systemd timer, with a new file name each time, and keep as many as you like. The backup carries the audit log, spend and keys, and everything else in the database: the events table is included, and can be large. Backup does not copy `kman.yaml` or the master key; keep those in your configuration management.

## Restore

```sh
# stop kman serve first
kman restore /safe/place/kman-2026-11-11.db
kman serve
```

`kman restore <file>` replaces the database with a backup. It works offline and checks, before it changes anything, that:

1. **the server is stopped.** If one holds the database it fails with "another process holds the database" and changes nothing;
2. **the file is a kman database**, undamaged, and not from a newer kman than this binary;
3. **the master key opens it.** The key from your `kman.yaml` (file, passphrase, systemd-creds or env) must unwrap the backup's data key. If you ran `kman rekey` after taking the backup, the backup opens only with the key from before the rekey: restore it with that key in place (see [Old copies keep the old key](upgrade.md#replace-the-master-key)). A mismatch changes nothing.

Then it sets the database it replaces aside as `kman.db.before-restore` in the data directory (replacing an earlier one), puts the backup in place `0600`, and records `system:restore` / `data.restore` in the restored database's audit log (the server is stopped, so no alert is sent for it). **Everything written after the backup is gone**: keys, spend, audit entries and events. Delete `kman.db.before-restore` when you no longer need it; it is a full copy of the database, ciphertext as well.

If the backup is from an older schema, the next start migrates it forward, after a `kman.db.pre-<NNNN>` copy, as [an upgrade does](upgrade.md#what-a-new-version-does-at-startup). Restoring onto a new machine works the same way: `kman init` there with the **same master key**, or put the key and a `kman.yaml` in place, then `kman restore`; the data directory is created if it is missing.

## Retention

kman prunes old data by itself, at startup and then once a day:

| Data | Kept |
|---|---|
| Events | 30 days |
| Hourly usage | 400 days |
| The alert outbox | 30 days after delivery; an alert not yet delivered, or given up on, is kept |
| Daily spend | forever |
| Incidents | forever |
| The audit log | forever |

When a pass deletes something it logs one line and writes one audit entry (actor `system:retention`, action `data.prune`) with the windows it applied and the counts; a pass that deletes nothing writes nothing. A pass returns the freed pages to the file system, so the database file shrinks.

To turn pruning off, set this in `kman.yaml` and restart:

```yaml
retention: keep_everything
```

`retention: default` (or nothing) is the table above. Nothing else is accepted, so a typo fails at startup instead of deleting what you meant to keep. The windows are not configurable in v0.1.x; use `kman prune` for a date of your own.

## Prune

```sh
kman prune --before 2026-01-01                 # events, hourly usage and delivered alerts older than the date
kman prune --before 2026-01-01 --audit-log     # ... and the audit log's entries older than the date
```

`kman prune --before <date>` deletes what is older than the date (`2026-01-01` is midnight UTC; a full RFC 3339 time works too) and prints the counts. It works with `retention: keep_everything`, and it is always audited (`data.prune`, with the counts and whether the audit log was included). It refuses a date in the future. Daily spend and incidents are never pruned.

**The audit log is the exception that needs asking.** It is append-only, enforced by a trigger in the database, and `--audit-log` is the one path that ever deletes from it: the prune opens a guard (`meta.audit_prune_before`) in its own transaction, deletes the entries older than the date, closes the guard, and appends its record, all or nothing. The record of the prune is newer than the date, so it stays: a pruned log always says that it was pruned, when, by whom, and how many entries went. Think before you do it; there is no undo except a [backup](#back-up).

## Update checks

kman **never contacts the network on its own** ([ADR 0008](../adr/0008-no-network-call-unless-configured.md)). Two things ask GitHub's releases API (`api.github.com`, one `GET` for the latest release, sending only `User-Agent: kman/<version>`):

- **`kman version --check`** asks once, when you run it, and says whether a newer release exists. `kman version` alone asks nothing. `--json` prints the answer as JSON. A build that is not a release (from `go build`, or between tags) cannot be compared; the command says so and names the latest release.
- **`update_check: true`** in `kman.yaml` (off unless you set it) makes `kman serve` ask once at startup and then every day. What it learns shows in plain `kman` (as "A newer kman is available: v0.2.0 …"), in `GET /admin/v1/update`, and as the metric `kman_update_available` (1 or 0; the metric exists only when the check is on). A check that fails (no network, a rate limit) is remembered and tried again the next day, and never stops kman.

The proxy variables (`HTTPS_PROXY` and friends) apply to both, for an operator behind one. Air-gapped, leave it off: plain `kman` and the dashboard-to-come show how old the binary and its price table are without the network, and [price data](../price-data.md) explains why that matters.
