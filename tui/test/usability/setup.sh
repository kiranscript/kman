#!/usr/bin/env bash
# Run once per participant, then source the printed file in each test terminal.
set -euo pipefail
umask 077
repo=$(cd "$(dirname "$0")/../../.." && pwd)
lab=$(mktemp -d /tmp/kman-usability.XXXXXXXX)
mkdir "$lab/bin"
(cd "$repo" && go build -o "$lab/bin/kman" ./cmd/kman)

# Keep the normal CLI commands and recovery instructions usable in both shells.
# The admin socket is unique to this install and the proxy gets a free loopback port.
{
  printf 'export KMAN_LAB=%q\n' "$lab"
  printf 'export XDG_CONFIG_HOME=%q XDG_DATA_HOME=%q\n' "$lab/config" "$lab/data"
  printf 'export PATH=%q:"$PATH"\n' "$lab/bin"
  printf 'export KMAN_ADMIN_URL=%q KMAN_MASTER_KEY_FILE=%q\n' "unix:$lab/admin.sock" "$lab/master.key"
  echo 'unset KMAN_ADMIN_TOKEN KMAN_MASTER_KEY KMAN_PROXY_LISTEN KMAN_ADMIN_LISTEN KMAN_ALLOW_PLAIN_HTTP'
} > "$lab/env"
source "$lab/env"
kman keygen > "$KMAN_MASTER_KEY_FILE"
kman init --no-save-token --admin-listen "$KMAN_ADMIN_URL" --proxy-listen 127.0.0.1:0 |
  awk '/^[[:space:]]+kma_[A-Za-z0-9_-]+$/ {print $1}' > "$lab/owner-token"
test -s "$lab/owner-token"
printf 'source %q\n' "$lab/env"
