# Run kman with Docker Compose

The image is `ghcr.io/kirank55/kman`: distroless, running as UID 65532, for linux/amd64 and linux/arm64. [Verify it](../verify.md) before you run it.

The Compose example is [`examples/compose/compose.yaml`](../../examples/compose/compose.yaml). It:

- mounts the master key as a secret, and points `KMAN_MASTER_KEY_FILE` at it, so the key never lands on the volume;
- keeps kman's config and data directories on the `kman` volume;
- runs with a read-only root filesystem, no capabilities, and `no-new-privileges`;
- publishes the proxy on host `127.0.0.1:8080` and admin on host `127.0.0.1:8081` only.

## Set up

In an empty directory, download the example:

```sh
curl -fsSLO https://raw.githubusercontent.com/kirank55/kman/main/examples/compose/compose.yaml
```

Then make a master key, set kman up, and start it. CI runs these exact commands against every change to the image or the example.

<!-- compose:begin -->

```sh
docker run --rm ghcr.io/kirank55/kman keygen > master.key
docker compose run --rm kman init
docker compose up -d
```

<!-- compose:end -->

`kman init` prints the `owner` admin token to your terminal once. In a container kman never saves it, and it is not in `docker compose logs`. Keep it, and pass it to the CLI as `KMAN_ADMIN_TOKEN`.

**Back up `master.key` apart from the volume.** The volume holds only ciphertext, and without the key nothing in it can be decrypted. On a shared host, make the key readable by the container's user only: `sudo chown 65532:65532 master.key && sudo chmod 0400 master.key`.

## What the example sets, and why

- **Listeners.** Inside the container kman has to listen on every interface for a published port to reach it. The Compose file says so explicitly (`KMAN_PROXY_LISTEN`, `KMAN_ADMIN_LISTEN` and `KMAN_ALLOW_PLAIN_HTTP`), and `kman init` writes those choices to `kman.yaml`. The port mappings are what keep kman off the network: both publish on `127.0.0.1` only. To reach the proxy from other machines, put a TLS reverse proxy in front of it rather than publishing `8080` more widely.
- **The volume** is mounted on `/home/nonroot`, the image's home directory, which holds kman's default config directory (`~/.config/kman`) and data directory (`~/.local/share/kman`). Docker gives a new named volume the owner of the directory it is mounted on, which here is UID 65532.
- **`/tmp`** is a tmpfs, for SQLite's temporary files on a read-only root.

## Replace the master key

`kman rekey` needs the server stopped. In a container it never makes a key, so make the new one on the host and mount it for the one run:

```sh
docker compose stop kman
docker run --rm ghcr.io/kirank55/kman keygen > master.key.next
docker compose run --rm -v "$PWD/master.key.next:/next.key:ro" kman rekey --new-key-file /next.key
mv master.key.next master.key
docker compose up -d
```

The new key must be readable by the container's user. Backups made before the rekey open only with the old key, so keep that key for as long as you keep them, and if it leaked, destroy them both.

## Upgrade

Set `KMAN_VERSION` to pin a release (such as `KMAN_VERSION=0.1.0`); without it the example runs `latest`. Then:

```sh
docker compose pull
docker compose up -d
```

kman copies the database to `kman.db.pre-<version>` before it migrates.
