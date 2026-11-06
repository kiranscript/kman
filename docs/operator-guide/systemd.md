# Run kman as a systemd service (.deb and .rpm)

The `.deb` and `.rpm` packages are best-effort: each release attaches them for amd64 and arm64, covered by the signed `checksums.txt` and the release attestations ([verify them](../verify.md)). They install two files, `/usr/bin/kman` and `/usr/lib/systemd/system/kman.service`. Their scripts create nothing: systemd makes the user and the directories when the service starts.

The unit runs `kman serve`:

- as a **dynamic user** (`DynamicUser=yes`), allocated when the service starts;
- with the data directory in **`/var/lib/kman`** (`StateDirectory=kman`) and the config in **`/etc/kman`** (`ConfigurationDirectory=kman`);
- with the master key as an **encrypted credential** (`LoadCredentialEncrypted=master.key:/etc/kman/master.key.cred`), which systemd decrypts for this service only, so the key is never on disk in the clear;
- with `kman.yaml` passed in as a credential too (`LoadCredential=kman.yaml:/etc/kman/kman.yaml`), because `kman init` writes it `0600` as root and the dynamic user could not read it otherwise;
- sandboxed: no capabilities, a read-only system, a system-call filter, and no core dumps.

It needs systemd 250 or later (Debian 12, Ubuntu 22.04, RHEL 9, Fedora 36 and later).

## Install

Download the package for your machine from the [releases page](https://github.com/kirank55/kman/releases), check it against `checksums.txt`, and install it:

```sh
apt install ./kman_0.1.0_amd64.deb        # Debian, Ubuntu
dnf install ./kman-0.1.0-1.x86_64.rpm     # Fedora, RHEL
```

## Set up

Run these as root. They make a master key and encrypt it with `systemd-creds` straight away, set kman up with that key, and start the service. CI runs these exact commands against every change to the packages.

<!-- systemd:begin -->

```sh
mkdir -p -m 0700 /etc/kman
kman keygen | systemd-creds encrypt --name=master.key - /etc/kman/master.key.cred
systemd-run --pipe --wait --collect --quiet \
  -p LoadCredentialEncrypted=master.key:/etc/kman/master.key.cred \
  /usr/bin/kman init --config-dir /etc/kman --data-dir /var/lib/kman --key-source systemd-creds --no-save-token
systemctl enable --now kman
```

<!-- systemd:end -->

`kman init` runs as a transient service here, because that is how it gets the decrypted key. It prints the `owner` admin token once; pass it to the CLI as `KMAN_ADMIN_TOKEN`. When the service first starts, systemd moves `/var/lib/kman` to `/var/lib/private/kman`, links it back, and hands it to the dynamic user.

**Back up the master key.** `systemd-creds` binds `master.key.cred` to this machine (its TPM, or `/var/lib/systemd/credential.secret`), so the file is no backup. To keep a copy you can restore elsewhere, save the key as you make it, somewhere apart from the data directory: `kman keygen | tee /path/to/offline/master.key | systemd-creds encrypt …`.

Check the service with `systemctl status kman` and `journalctl -u kman`.

## Replace the master key

`kman rekey` needs the service stopped, and runs as a transient service to get the old key, as `kman init` did:

```sh
systemctl stop kman
(umask 077; kman keygen > /root/master.key.next)
systemd-run --pipe --wait --collect --quiet \
  -p LoadCredentialEncrypted=master.key:/etc/kman/master.key.cred \
  /usr/bin/kman rekey --config-dir /etc/kman --new-key-file /root/master.key.next
systemd-creds encrypt --name=master.key /root/master.key.next /etc/kman/master.key.cred
systemctl start kman
```

Then back up `/root/master.key.next` apart from the data directory and remove it from there. Backups made before the rekey open only with the old key, so keep that key for as long as you keep them, and if it leaked, destroy them both.

## Change the settings

Edit `/etc/kman/kman.yaml` and run `systemctl restart kman`; serve reads it only at startup. For TLS, pass the certificate and key in as credentials too, with `systemctl edit kman`:

```ini
[Service]
LoadCredential=proxy.crt:/etc/kman/proxy.crt
LoadCredential=proxy.key:/etc/kman/proxy.key
```

and point `kman.yaml` at them under `/run/credentials/kman.service/`.

## Upgrade and remove

Install the new package and run `systemctl restart kman`. kman copies the database to `kman.db.pre-<version>` before it migrates.

Removing the package leaves `/etc/kman` and `/var/lib/private/kman` alone. Delete them yourself if you mean to.
