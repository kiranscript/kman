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

`kman init` prints the `owner` admin token to your terminal once. In a container kman never saves it, and it is not in `docker compose logs`. Keep it somewhere safe.

## Run kman commands against the container

The admin API is published on host `127.0.0.1:8081`, so a `kman` installed on the host (see [Install](install.md)) manages the container. Tell it where the API is and which token to use:

```sh
export KMAN_ADMIN_URL=http://127.0.0.1:8081
export KMAN_ADMIN_TOKEN=kma_…          # the token init printed
kman upstream add openai --preset openai
kman key create my-app --upstream openai
```

**Plain `kman` with no arguments can mislead you here.** Its status summary looks for an install in the *host's* config directory, and the config of a container lives in the volume. Without a token or URL in the environment it says `kman is not initialised: there is no install in ~/.config/kman` and suggests `kman init`, which is not what you want. With `KMAN_ADMIN_URL` and `KMAN_ADMIN_TOKEN` set, as above, it asks the running server and gives the right answer. (Don't run `kman init` on the host to "fix" it: that makes a second, separate install.) The same goes for a [systemd](systemd.md) install.

The proxy is published on `http://127.0.0.1:8080`, so an app on the host uses `OPENAI_BASE_URL=http://127.0.0.1:8080/v1`. CI runs the commands in the Set up block above against the image and the example; it does not run the sections after it.

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

The new key must be readable by the container's user. Copies of the volume made before the rekey open only with the old key, so keep that key for as long as you keep them, and if it leaked, destroy them both and replace the real credentials at their providers: see [Upgrade, rekey and old copies](upgrade.md#replace-the-master-key).

## Upgrade

Set `KMAN_VERSION` to pin a release (such as `KMAN_VERSION=0.1.0`); without it the example runs `latest`. Then:

```sh
docker compose pull
docker compose up -d
```

When the new version has to migrate the database, kman first copies it to `kman.db.pre-<NNNN>` in the volume (`NNNN` is the schema version it migrates to, and the last two copies are kept). See [Upgrade](upgrade.md#what-a-new-version-does-at-startup). To **back up**, stop the container (`docker compose stop kman`) and copy the volume's contents, keeping `master.key` somewhere else.
