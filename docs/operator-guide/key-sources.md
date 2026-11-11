# Key sources

kman seals every upstream credential under a **data key**, and the data key under a **master key**. The master key is the one secret that decrypts everything in the data directory, so where it lives is the most important choice you make. A **key source** is where kman reads it from at every start. `kman init --key-source` picks one, and `key_source` in `kman.yaml` records it.

**The master key never lives in the data directory.** `kman serve` refuses to start if the key file is inside it (symlinks followed), because a backup, a copied volume or a snapshot of the data directory would then carry the key that decrypts it. (`allow_key_in_data_dir: true` in `kman.yaml` overrides this, which defeats the point; don't.) Keep a copy of the master key **apart from the data directory**, in a password manager or somewhere offline: without it, nothing in the data directory can be decrypted, and nothing can bring it back.

| Source | The key lives in | Good for | Costs |
|---|---|---|---|
| `file` (default) | `master.key` in the config directory, `0600` | A laptop, a small server | Anyone who can read the file can read every credential. |
| `systemd-creds` | An encrypted systemd credential, which systemd decrypts for the service alone | A Linux server run by systemd | Needs systemd 250 or later; the credential is tied to the machine. |
| `passphrase` | `master.key.sealed` in the config directory, sealed with a passphrase (argon2id) | A machine you start by hand | Asks for the passphrase on every start, on a terminal, so it can't restart unattended. |
| `env` | The `KMAN_MASTER_KEY` environment variable | Container platforms that inject secrets as variables | The key sits in the process's environment; kman warns every time it reads it. |

## file

`kman init` makes the key and writes it to `master.key` (one line of base64) in the config directory. To keep the key somewhere else, such as a mounted secret, point `KMAN_MASTER_KEY_FILE` at a file made with `kman keygen`:

```sh
kman keygen > /path/to/master.key
KMAN_MASTER_KEY_FILE=/path/to/master.key kman init
KMAN_MASTER_KEY_FILE=/path/to/master.key kman serve
```

`KMAN_MASTER_KEY_FILE` overrides `master_key_file` in `kman.yaml`, for the `file` source. kman does not check the key file's mode, so check it yourself: it should be readable by kman's user alone. (A Docker secret is `0444` by design: see [Docker](docker.md).)

## systemd-creds

kman reads the key from `master.key` in `$CREDENTIALS_DIRECTORY`, which systemd fills from `LoadCredentialEncrypted=` for the service alone, so the key is never on disk in the clear. The packaged unit does this. See [systemd](systemd.md) for the commands. `systemd-creds` binds the credential to the machine (its TPM, or `/var/lib/systemd/credential.secret`), so the `.cred` file **is not a backup**: save the key itself, offline, as you make it.

## passphrase

```sh
kman init --key-source passphrase
```

asks for a passphrase twice and writes `master.key.sealed` to the config directory: the master key, sealed with the passphrase using argon2id. `kman serve` asks for the passphrase on every start and needs a terminal on its standard input, so it can't be started by a service manager without one. Forget the passphrase and the sealed key is useless.

## env

```sh
export KMAN_MASTER_KEY="$(kman keygen)"     # make it once, and keep a copy somewhere safe
kman init --key-source env
kman serve                                  # every start needs the same variable
```

reads the key from `KMAN_MASTER_KEY`, one line of base64 as `kman keygen` prints it. The key is in the environment of whatever starts kman, so anything that can see it there can read it: `docker inspect` on a container, an orchestrator's configuration, any program you start from that shell. kman warns each time it reads it, and child processes inherit it.

## In a container

kman refuses to make a key in a container (it would land on the container's filesystem or on the data volume), and never saves the admin token there. `kman init` needs a key from outside: a secret file named by `KMAN_MASTER_KEY_FILE`, `KMAN_MASTER_KEY`, or a systemd credential. [Docker](docker.md) shows the Compose way.

## Changing the key source, or the key

`kman rekey` replaces the master key and the data key with the server **stopped**, and can move the install to another source with `--new-key-source`. It re-seals every credential in one transaction, and a rekey that fails leaves the database as it was. The details, and what to do with old copies, are in [Upgrade, rekey and old copies](upgrade.md#replace-the-master-key).

`kman keygen` prints a new master key and stores nothing.

## Process hardening

When kman unlocks the master key it turns off core dumps for the process (on Linux it also makes the process non-dumpable, which stops same-user `ptrace` and reads of `/proc/<pid>/mem`), so the key can't leak that way, and it wipes the key from memory after use. On Windows it does neither. `--allow-debug` on `init`, `serve` and `rekey` skips that hardening, for debugging kman itself; don't use it in production. Credentials are held in memory (they have to be, to be sent to the upstream) and wiped when they are replaced or deleted, but copies made while building a request header live in Go strings that kman cannot wipe: see [Threat model](../../THREAT-MODEL.md).
