#!/bin/sh
# Records every packet a kman session sends off the machine (SPEC §1 invariant
# 6, ADR 0008). Linux only; needs unprivileged user namespaces.
#
#   scripts/record-network.sh ./kman              # nothing configured: expect no packets
#   scripts/record-network.sh ./kman --control    # also use an upstream: expect its lookups
#
# It runs the session in a private network namespace whose only way out is a
# dummy interface (packets sent there go nowhere, and are recorded), so no
# traffic leaves the machine and nothing needs root. Loopback is not recorded:
# kman's own listeners and a local mock upstream live there.
set -eu

if [ "${KMAN_RECORD_INNER:-}" != 1 ]; then
	kman=$(realpath "$1")
	shift
	exec env KMAN_RECORD_INNER=1 KMAN_BIN="$kman" unshare --user --map-root-user --net sh "$0" "$@"
fi

work=$(mktemp -d)
trap 'kill $sniffer $serve 2>/dev/null; rm -rf "$work"' EXIT
sniffer= serve=
export HOME="$work/home" XDG_CONFIG_HOME= XDG_DATA_HOME=
mkdir -p "$HOME"
kman=$KMAN_BIN

ip link set lo up
ip link add out0 type dummy
ip link set out0 arp off
ip link set out0 up
ip addr add 10.99.99.1/24 dev out0
ip route add default via 10.99.99.2 dev out0

python3 -I - "$work/packets" <<'PY' &
import socket, struct, sys
s = socket.socket(socket.AF_PACKET, socket.SOCK_RAW, socket.htons(3))
s.bind(("out0", 0))
out = open(sys.argv[1], "a", buffering=1)
while True:
    frame, (_, _, kind, _, _) = s.recvfrom(65535)
    if kind == socket.PACKET_OUTGOING and len(frame) >= 34 and frame[12:14] == b"\x08\x00":
        proto = frame[23]
        dst = socket.inet_ntoa(frame[30:34])
        port = struct.unpack("!H", frame[36:38])[0] if proto in (6, 17) else 0
        out.write(f"{ {6: 'tcp', 17: 'udp', 1: 'icmp'}.get(proto, proto) } {dst}:{port}\n")
PY
sniffer=$!
touch "$work/packets"
sleep 1

run() { "$kman" "$@" >"$work/out" 2>&1 || { echo "kman $* failed:"; cat "$work/out"; exit 1; }; }

run keygen
run init
"$kman" serve >"$work/serve.log" 2>&1 &
serve=$!
sleep 1.5
run
run version
run settings get
run price list
run key list
run upstream list
run admin-token list
run audit
run price set some-model --input 1 --output 2 --max-output 100
run price list
if [ "${1:-}" = "--control" ]; then
	printf %s sk-planted | run upstream add openai --preset openai
	key=$("$kman" key create control --upstream openai --json | python3 -I -c 'import json,sys; print(json.load(sys.stdin)["secret"])')
	# This request makes kman look up and dial api.openai.com: the recorder must see it.
	python3 -I - "$key" <<'PY2' || true
import sys, urllib.request
req = urllib.request.Request("http://127.0.0.1:8080/v1/models", headers={"Authorization": "Bearer " + sys.argv[1]})
try:
    urllib.request.urlopen(req, timeout=15)
except Exception:
    pass
PY2
fi
sleep 1
kill "$serve"; wait "$serve" || true
run rekey
"$kman" serve >"$work/serve.log" 2>&1 &
serve=$!
sleep 1.5
run
kill "$serve"; wait "$serve" || true
sleep 1

echo "packets sent off the machine: $(wc -l <"$work/packets")"
sort "$work/packets" | uniq -c
