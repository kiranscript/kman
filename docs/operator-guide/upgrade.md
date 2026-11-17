# Upgrade, rekey and old copies

## Upgrade

An upgrade replaces the binary and restarts kman. There is no downgrade command: read [Downgrade](#downgrade) before you upgrade a machine you can't afford to lose.

1. Read the release's section of the [changelog](../../CHANGELOG.md), which becomes its release notes.
2. Verify the new release ([Verify a kman release](../verify.md)).
3. Replace the binary, and restart:

| Installed with | Upgrade with |
|---|---|
| `install.sh` | Run it again. `KMAN_VERSION=v0.1.1` pins a tag. |
| Homebrew | `brew upgrade kman` |
| `.deb` or `.rpm` | Install the new package, then `systemctl restart kman`. |
| Docker Compose | `docker compose pull && docker compose up -d`; see [Docker](docker.md#upgrade). |
| Archive | Replace the binary. |

Stop kman with Ctrl-C or SIGTERM: it stops listening, waits up to 10 seconds for requests still running, writes the last spend to the database, and exits. Then start the new one.

### What a new version does at startup

- **The database migrates forward, once.** If the new kman finds a database at an older schema, it first **copies the database to `kman.db.pre-<NNNN>`** in the data directory, then migrates. `NNNN` is the schema version the database is being migrated **to**, as four digits, not the kman release: `kman.db.pre-0002` is the database as it was before migration 0002. The copy is a consistent `VACUUM INTO` copy, `0600`, and **the last two copies are kept**. A database that is new gets no copy. v0.1.0 is at schema 0001, so a v0.1.x release that doesn't change the schema makes no copy; the first one appears with the first release that migrates. A database **newer** than the kman that opens it is refused, not touched.
- **Existing upstreams don't change.** A preset is copied once, recording its version, and a newer kman never edits an upstream you already have. `kman upstream list` marks one whose preset has a newer version, and `kman upstream sync <name>` shows the difference and applies it after you confirm. A sync copies the preset whole, undoing any edit the plan shows, and keeps the upstream's id, name, credentials and price multiplier.
- **Prices change with the binary.** The price table is embedded, and kman never fetches price data. An upgrade is how you get new prices; `kman price list` and plain `kman` say how old the table is (past 60 days they say to upgrade), and plain `kman` also says when your binary was built. Your price overrides stay and win over the table.
- **kman.yaml is read at startup only.** Edit it, then restart.
- **Retention starts pruning.** A kman with [retention](backup.md#retention) deletes events older than 30 days, hourly usage older than 400, and delivered alerts older than 30 days after delivery, at startup and daily. `retention: keep_everything` turns that off.

### Downgrade

Migrations are forward-only. To go back to an older kman after a migration, stop kman, put the old binary back, and restore the copy: `kman restore <data-dir>/kman.db.pre-<NNNN>` (a kman without that command: move `kman.db` aside, rename `kman.db.pre-<NNNN>` to `kman.db`, and delete any `kman.db-wal` and `kman.db-shm` beside it). You lose every change made since the copy (keys, spend, audit entries), and the copy opens **only with the master key that was current when it was made**: see below.

## Back up the data

`kman backup <file>` takes a consistent copy while kman runs, and `kman restore <file>` puts one back with the server stopped: see [Backup, restore, retention and updates](backup.md). A backup is ciphertext, and **the master key is what makes it readable**, so back the master key and `kman.yaml` up somewhere else. A backup stored beside the master key defeats the point.

The `kman.db.pre-<NNNN>` copies are not a backup plan: they are made only at a migration, and they sit in the data directory they are meant to protect.

## Replace the master key

`kman rekey` replaces the master key **and** the data key, with the server stopped, re-sealing every credential in one transaction. It writes an audit entry (actor `system:rekey`, action `data_key.rekey`). It refuses to run beside a running server, with the same key, or with an old key that does not unwrap, and a rekey that fails leaves the database as it was.

```sh
kman rekey                                       # a new key, same source (file or passphrase)
kman rekey --new-key-source passphrase           # move to another source
kman rekey --new-key-file /path/to/next.key      # a key from kman keygen: needed for env and systemd-creds
```

kman makes the new key itself for the `file` and `passphrase` sources; the others need `--new-key-file`. It makes none in a container, or over `KMAN_MASTER_KEY_FILE`. The new key is staged next to the old one and moved into place after the commit; if the move is interrupted, run the command again and it tells you which move finishes it. `kman.yaml` is edited in place and keeps your comments. The container and systemd forms are in [Docker](docker.md#replace-the-master-key) and [systemd](systemd.md#replace-the-master-key).

**Old copies keep the old key.** The old data key is **deleted** from the database, so a rekeyed database no longer opens with the old master key, and any credential deleted before the rekey is gone for good. But **every copy of the database made before the rekey still opens with the old master key**: the `kman.db.pre-<NNNN>` copies, a backup of the data directory, a copied volume, a snapshot. And a rekey can overwrite the old key file. So:

- keep the old key for as long as you keep any such copy, and
- if the old key leaked, **destroy those copies as well as the key**, because the old key plus the old copy gives up every credential sealed in it, and **rotate the real credentials at the providers**, since those are what the leak exposed.

Rekey's report says the same when it finishes.
