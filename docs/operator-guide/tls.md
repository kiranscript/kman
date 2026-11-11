# TLS and reverse proxies

By default kman serves plain HTTP on `127.0.0.1`, which is safe because nothing off the machine can reach it. The moment an app on **another machine** talks to kman, its virtual key crosses a network, and that needs TLS. There are two ways to get it.

Neither is exercised by kman's CI beyond the listener's own TLS tests: the proxy configurations below are written to match what kman does, and you should try them on your own set-up before you rely on them.

## Option 1: a reverse proxy in front (recommended)

Run kman on loopback and let Caddy, nginx or a cloud load balancer terminate TLS and forward to `127.0.0.1:8080`. kman's own listeners stay private, and you get the proxy's certificate renewal.

In `kman.yaml`, name the proxy as a **trusted proxy**, so that kman reads the client's address from the `X-Forwarded-For` header that proxy adds, instead of seeing every request come from the proxy:

```yaml
trusted_proxies:
  - 127.0.0.1
```

(An address, or a network such as `10.0.0.0/8`.) Restart kman after editing `kman.yaml`; it reads the file only at startup. kman believes `X-Forwarded-For` only on a connection from a trusted proxy, and reads the list **from the right**: the first address no trusted proxy added. Entries may carry ports, and several header lines count as one list. With no `trusted_proxies`, a request's address is its connection's, and the invalid-key throttle (see [Keys, policy and spend](../policy.md#rate-limits)) would count your whole fleet as one address. `trusted_proxies` applies to the proxy listener, not the admin one.

kman strips `X-Forwarded-*`, `Forwarded`, `Via` and `X-Real-Ip` from what an app sends before it forwards to the upstream, so a proxy in front of kman doesn't tell the provider the app's real address.

### Caddy

```
kman.example.com {
	reverse_proxy 127.0.0.1:8080 {
		flush_interval -1
	}
}
```

`flush_interval -1` sends each chunk of a streamed response on as it arrives. Caddy sets `X-Forwarded-For` itself.

### nginx

```nginx
server {
    listen 443 ssl;
    server_name kman.example.com;
    ssl_certificate     /etc/ssl/kman.example.com.pem;
    ssl_certificate_key /etc/ssl/kman.example.com.key;

    client_max_body_size 32m;          # kman refuses over 32 MiB itself

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_buffering off;           # streams must not wait for a full buffer
        proxy_read_timeout 3600s;      # a long generation is one long response
    }
}
```

The two lines that matter for LLM traffic are `proxy_buffering off` (without it a stream reaches the app in lumps, or all at the end) and a long `proxy_read_timeout`.

### Keep the admin listener private

Don't put the admin API (`127.0.0.1:8081`) on the public internet. It changes everything, and a stolen admin token is a stolen install. If you must reach it from another machine, put it behind TLS and a network restriction too, or leave it on loopback and use `ssh -L 8081:127.0.0.1:8081 server` from where you work.

## Option 2: TLS in kman

kman can serve TLS itself, per listener, from PEM files:

```yaml
tls:
  proxy:
    cert_file: /etc/kman/proxy.crt
    key_file: /etc/kman/proxy.key
  admin:
    cert_file: /etc/kman/admin.crt
    key_file: /etc/kman/admin.key
```

Each listener needs both files, or neither. TLS 1.2 is the minimum. kman reads the certificate at startup, so restart it after a renewal. To listen off loopback, also set `proxy_listen` (`host:port`) and `admin_listen`:

```yaml
proxy_listen: 0.0.0.0:8443
```

**Plain HTTP is refused on a non-loopback admin address**, unless `tls.admin` is set or `allow_plain_http: true` says that something in front of kman terminates TLS (the Compose example does this inside its own container network). kman places no such rule on the proxy listener: **it will listen off loopback in plain HTTP without a warning**, so check what you set. The admin listener can instead be a `0600` Unix socket: `admin_listen: unix:/run/kman/admin.sock`.

Point the CLI at a TLS admin listener with `KMAN_ADMIN_URL=https://…`; `kman login --admin-url https://host:8081` warns when you give it plain HTTP to a remote host.

## What a proxy in front changes for kman

- **Limits.** kman refuses a request body over 32 MiB (`request_too_large`), gives up on a body that stops arriving for 30 seconds or takes over 5 minutes, caps the request bodies it holds in memory at 512 MiB across all requests (`rate_limited` past that), closes idle connections after 2 minutes, and limits a request head to 64 KiB. Set your proxy's limits at or above these, not below.
- **Redirects.** kman never follows an upstream redirect; within the upstream it rewrites `Location` to a path so the app stays on kman.
- **Health checks.** kman has no health endpoint in v0.1.0. A check that needs one can request any path and treat a kman error response (`invalid_key`, 401) as "up".
