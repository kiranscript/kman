# Embedding a web dashboard in a Go binary (kman#6)

Researched 2026-11-02. Go toolchain used for measurements: go1.27.3 linux/amd64, `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"`.

## Question

What is the best way to ship a web dashboard inside kman's Go single binary, and how should it authenticate operators safely when it is served on localhost?

kman context: one static binary (`CGO_ENABLED=0`), admin API on `127.0.0.1:8081` or a Unix socket, bearer admin token (`kma_` + 32 random bytes, SHA-256 stored). The dashboard serves one operator from the same binary.

## Answer

1. **Render on the server and vendor htmx into `embed.FS`. Don't build an SPA.** `go install` and `go build` keep working with no Node or JS toolchain. SSE comes from a plain `net/http` handler. Tests run as ordinary Go `httptest` tests. Pick `templ` or `html/template` based on one surprising cost: in a minimal binary, `html/template` adds about **2.4-3.3 MB** because text/template's reflection turns off the linker's method dead-code elimination. On a pure-Go SQLite binary, `templ` adds about **0.15 MB**. Recommendation: **templ, with its generated `*_templ.go` files committed** (the approach templ itself documents). Fall back to `html/template` if you want zero third-party dependencies and can absorb the ~3 MB.
2. **Auth: use a server-side session cookie, never the admin token itself in the browser.** Get the cookie from either a one-time login link minted by the CLI (Syncthing's `syncthing browser` pattern) or by pasting the admin token once. Use `HttpOnly; SameSite=Strict; Path=/`, a short TTL, and kill all sessions when the admin token rotates. On every dashboard route, add Go's stdlib `http.CrossOriginProtection` for CSRF and a **Host allowlist** (`localhost`, `127.0.0.1` and `[::1]` with the port) for DNS rebinding. Add `frame-ancestors 'none'` against clickjacking. The bearer-token admin API stays as it is: it uses no ambient credentials, so it cannot be CSRF'd.

## 1. Rendering approach

### Measured binary-size cost

Each row adds one thing to a baseline program. Sizes are stripped binaries.

| Program | Size (bytes) | Delta vs its baseline |
|---|---:|---:|
| `net/http` + `CrossOriginProtection` (baseline A) | 5,841,056 | — |
| A + `html/template` | 9,093,280 | **+3.25 MB** |
| A + `html/template` + embedded htmx 2.0.11 + SSE ext | 9,240,736 | +3.40 MB |
| A + `github.com/a-h/templ` v0.3.1100 runtime | 6,287,520 | +0.45 MB |
| A + embedded React 19.3.1 Vite build (`dist/`, 267 KB) + `http.FileServerFS` | 6,221,984 | +0.38 MB |
| `modernc.org/sqlite` v1.61.1 + `net/http` (baseline B) | 10,260,640 | — |
| B + `html/template` | 13,381,792 | **+3.12 MB** |
| B + templ runtime | 10,412,192 | +0.15 MB |
| Prometheus `promhttp` (baseline C) | 9,253,024 | — |
| C + `html/template` | 11,628,704 | +2.38 MB |

Why `html/template` costs so much: text/template calls `reflect.Value.MethodByName` with a non-constant name ([src/text/template/exec.go](https://github.com/golang/go/blob/master/src/text/template/exec.go)). The linker then marks the function `REFLECTMETHOD` and "give[s] up on static analysis, and mark[s] all exported methods of all reachable types as reachable" ([src/cmd/link/internal/ld/deadcode.go](https://github.com/golang/go/blob/master/src/cmd/link/internal/ld/deadcode.go)). This is a one-time cost. If some other kman dependency already triggers `REFLECTMETHOD`, the marginal cost falls to about zero, so re-measure on the real binary. (cobra v1.10.2 and Prometheus `promhttp` do *not* trigger it: both still grew by more than 2 MB in the test.)

Front-end payload sizes (raw / gzip -9), measured:

| Asset | Raw | gzip |
|---|---:|---:|
| htmx 2.0.11 `htmx.min.js` | 52,182 | 16,838 |
| htmx-ext-sse 2.2.4 | 2,853 | 1,089 |
| htmx 4.0.1 `htmx.min.js` (branch `four`) | 37,448 | 13,339 |
| htmx 4 `hx-sse.min.js` | 6,532 | 2,635 |
| Vite 8.3.5 template, React 19.3.1 (JS only) | 222,447 | 68,356 |
| Vite 8.3.5 template, Svelte 5.57.3 | 30,766 | 12,223 |
| Vite 8.3.5 template, Preact 11.1.1 | 18,093 | 7,361 |
| Vite 8.3.5 template, Solid 1.9.18 | 15,031 | 5,690 |
| Tailscale web client (React, prod) | 88,448 zstd JS + 7,710 zstd CSS | — |

The Tailscale numbers come from the [web-client-prebuilt tree](https://github.com/tailscale/web-client-prebuilt/tree/main/build/assets). The `node_modules` for the Vite templates ran 66-96 MB each.

Takeaway: on bytes alone, an SPA is not the costly option. Embedded assets go in roughly raw (`embed.FS` stores files uncompressed). The real costs of an SPA are toolchain, process, and a second language. They are not binary size.

### Build toolchain: does `go install` still work without Node?

- `go install pkg@version` builds from the **module zip**, which "creates a .zip file of the contents of the directory" at that version ([Go Modules Reference, VCS / module zip files](https://go.dev/ref/mod#zip-files)). `go generate` "is never run automatically by go build, go test, and so on" (`go help generate`; [cmd/go docs](https://pkg.go.dev/cmd/go#hdr-Generate_Go_files_by_processing_source)). A gitignored `dist/` therefore doesn't exist in the zip, and the build fails at compile time. Verified locally: `main.go:5:12: pattern all:dist: no matching files found`.
- `//go:embed` patterns "must not match files outside the package's module" or nested modules. The `all:` prefix is needed to include `.`/`_` files ([pkg.go.dev/embed](https://pkg.go.dev/embed)).
- **SPA consequence:** you must commit the built bundle, or publish it as a separate Go module. Tailscale does the latter. `client/web` imports `github.com/tailscale/web-client-prebuilt`, whose whole job is `//go:embed build`, and a GitHub Action refreshes it ([assets.go](https://github.com/tailscale/tailscale/blob/main/client/web/assets.go), [embed.go](https://github.com/tailscale/web-client-prebuilt/blob/main/embed.go)). The web client source pins `node 22.23.4` / `yarn 1.22.19`, with Vite 5, React 18, Tailwind and vitest ([package.json](https://github.com/tailscale/tailscale/blob/main/client/web/package.json)). That is a sustainable model for a big team. For kman it would add a JS supply chain (28-93 npm packages even for a bare template) to a security-sensitive key vault.
- **templ consequence:** the same rule applies to `*_templ.go`, and templ documents the answer: "It's common practice to commit generated `*_templ.go` files … so that your codebase is always in a state where it can be built and run without needing to run `templ generate`, e.g. by running `go install`". CI then runs `templ generate && git diff --exit-code` ([templ CI/CD docs](https://github.com/a-h/templ/blob/main/docs/docs/09-developer-tools/06-cicd.md)). That keeps generated code in the repo, but it's all Go, and the templ CLI is a `go install`-able Go tool.
- **html/template + htmx:** nothing to generate. `htmx.min.js` is a vendored ~50 KB file.

### Dev loop

- templ: `templ generate --watch --proxy="http://localhost:8080" --cmd="go run ."` regenerates, restarts and reloads the browser through a proxy on `127.0.0.1:7331` ([templ live reload](https://github.com/a-h/templ/blob/main/docs/docs/09-developer-tools/03-live-reload.md)).
- html/template: parse from `os.DirFS` in dev and `embed.FS` in release. That's one `fs.FS` switch with no extra tool, but templates fail at execute time, not compile time.
- SPA: Vite HMR is the best UI dev loop, but it needs two processes plus a proxy. Tailscale's dev mode runs `yarn install` and then proxies asset requests to the Vite dev server ([assets.go](https://github.com/tailscale/tailscale/blob/main/client/web/assets.go)). You also maintain a JSON API contract and client state that duplicates server state.

### Real-time updates (SSE for live detections)

- Server: a stdlib handler that writes `text/event-stream` and calls `http.NewResponseController(w).Flush()`. Use `SetWriteDeadline` to keep a long-lived stream from hitting the server's `WriteTimeout` ([net/http ResponseController](https://pkg.go.dev/net/http#ResponseController)). This is the same for every UI choice.
- htmx 2: the `sse` extension (`sse-connect`, `sse-swap`, `hx-trigger="sse:<name>"`, `sse-close`) wraps `new EventSource(url,{withCredentials:true})` and swaps server-rendered HTML fragments straight into the DOM ([htmx SSE extension](https://htmx.org/extensions/sse/), [source md](https://github.com/bigskysoftware/htmx/blob/master/www/content/extensions/sse.md)). The detection row is rendered by the same Go template as the full page, so there's no JSON or client model.
- htmx 4.0.0 (released 2026-08-28; 4.0.1 on 2026-11-01): `hx-sse` is fetch-based. It has reconnect with jitter and backoff, `Last-Event-ID` resume, and pause-on-background ([announcement](https://github.com/bigskysoftware/htmx/blob/four/www/src/content/announcements/2026-08-28-htmx-4.0.0-is-released.md), [hx-sse source](https://github.com/bigskysoftware/htmx/blob/four/src/ext/hx-sse.js)). htmx 2 "will continue to be supported indefinitely", and 4.x stays on npm `next` until early 2027 (same announcement). Either works. Since the file is vendored, the choice is just which file to copy.
- Connection limit: without HTTP/2, browsers cap SSE at **6 connections per browser+host across all tabs** ("Won't fix" in Chrome and Firefox) ([MDN EventSource](https://developer.mozilla.org/en-US/docs/Web/API/EventSource)). Browsers don't speak cleartext h2c, so a plain-HTTP localhost dashboard is subject to this cap. Use one multiplexed event stream per page, not one per widget.
- Prefer SSE to WebSockets here. WebSocket handshakes "aren't subject to CORS restrictions", which is why Caddy rejects WebSocket upgrades on its admin endpoint ([caddy admin.go](https://github.com/caddyserver/caddy/blob/master/admin.go)). SSE is a plain GET that the Host/Origin/cookie defences already cover.

### Testability

- Server-rendered: handler tests with `httptest` assert on HTML. templ documents expectation tests with goquery and snapshot tests ([templ testing](https://github.com/a-h/templ/blob/main/docs/docs/04-core-concepts/03-testing.md)). CSRF, Host and auth middleware are tested in the same Go test run. Run `html/template` tests that execute every template so runtime template errors become test failures. A small Playwright smoke test is optional.
- SPA: it needs a JS test runner (Tailscale uses vitest + jsdom) plus API contract tests, and Node in CI.

### Rendering comparison summary

| | html/template + htmx | templ + htmx | Embedded SPA (React/Svelte/Solid) |
|---|---|---|---|
| Binary delta (measured) | +2.4-3.3 MB (reflection/deadcode), +~55 KB assets | +0.15-0.45 MB, +~55 KB assets | +0.3-0.4 MB (assets ~15-270 KB raw) |
| `go install` without Node | Yes | Yes, if `*_templ.go` committed | Only if built bundle committed or in a prebuilt module |
| Extra toolchain | None | `templ` CLI (Go) | Node, npm/yarn, Vite, 28-93+ npm packages |
| Type safety | Runtime template errors | Compile-time | TS, but split across a JSON API boundary |
| Dev loop | Restart or DirFS reload | `templ --watch` proxy live reload | Best (HMR), two processes |
| SSE live detections | ext swaps HTML | same | Hand-written EventSource + client state |
| Tests | Go only | Go only | Go + vitest/Playwright |
| Deps risk | stdlib | one pre-1.0 Go module (v0.3.x) | npm supply chain in a secrets vault |

## 2. Operator auth on localhost

### Threats specific to a localhost admin UI

1. **CSRF from any website.** A page can make the browser POST to `http://127.0.0.1:8081` with ambient cookies.
2. **Same-site CSRF from other localhost ports.** Ports don't separate sites, so `http://localhost:3000` is *same-site* with `http://localhost:8081`. `SameSite=Lax/Strict` cookies **are sent**. jub0bs' Grafana CVE-2022-21703 PoC hosts the attack page on `localhost:8081` to hit Grafana on `localhost:3000`, and concludes SameSite "is powerless against cross-origin, same-site attacks" ([write-up](https://jub0bs.com/posts/2022-02-08-cve-2022-21703-writeup/), [GHSA-cmf4-h3xc-jw8w](https://github.com/advisories/GHSA-cmf4-h3xc-jw8w)).
3. **Cookies aren't isolated by port.** "If a cookie is readable by a service running on one port, the cookie is also readable by a service running on another port of the same server" ([RFC 6265 §8.5](https://www.rfc-editor.org/rfc/rfc6265#section-8.5)). Any local dev server the operator visits on `localhost:*` receives kman's cookie and can overwrite it.
4. **DNS rebinding.** `evil.example` re-resolves to `127.0.0.1`, so the attacker's page is now *same-origin* with the dashboard. Origin and Fetch-Metadata checks pass. Only a **Host header check** catches it. Cookies for `localhost` won't be sent to `evil.example`, so rebinding mainly exposes unauthenticated endpoints such as the login form, static files and health checks.
5. **Clickjacking** of destructive buttons such as revoke or rotate.
6. Browser mitigation exists but isn't enough. Chrome 142 gates public→loopback requests behind a Local Network Access permission prompt ([Chrome blog](https://developer.chrome.com/blog/local-network-access)). The spec itself says services "should not rely on a UA that behaves as specified in this document" and must defend against CSRF on their own ([WICG LNA §5.3](https://wicg.github.io/local-network-access/)). Filippo likewise points to Private Network Access (LNA's predecessor) for rebinding, not to CSRF middleware ([words.filippo.io/csrf](https://words.filippo.io/csrf/), footnote 1).

### CSRF: tokens vs Fetch Metadata

- Go 1.25+ ships `http.CrossOriginProtection`. Safe methods (GET/HEAD/OPTIONS) always pass. Otherwise `Sec-Fetch-Site` must be `same-origin` or `none`, so **`same-site` is rejected**, which covers the localhost-other-port case. The fallback when the header is absent is `Origin` host == `Host`, and requests with neither header pass as non-browser ([pkg.go.dev/net/http#CrossOriginProtection](https://pkg.go.dev/net/http#CrossOriginProtection); source `net/http/csrf.go`). The design rationale is in [Filippo Valsorda, "Cross-Site Request Forgery" (2025-08-13)](https://words.filippo.io/csrf/).
- `Sec-Fetch-Site` is sent to "potentially trustworthy" origins, which include `localhost`, `127.0.0.0/8` and `::1`. That means plain-HTTP loopback gets the strong check ([OWASP CSRF Cheat Sheet, Fetch Metadata](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html#fetch-metadata-headers)). OWASP now lists Go's `CrossOriginProtection` as a built-in implementation and says a fallback to origin verification "is a mandatory requirement". Go's fallback provides that.
- CSRF tokens (synchronizer or HMAC double-submit) are still valid, but they require instrumenting every form and turn CSRF into a token-leak problem (Filippo, ibid.). With htmx they'd go in `hx-headers`. They aren't needed on top of `CrossOriginProtection` for a modern-browser, single-operator UI.
- Rule: no state change on GET. Revoke, rotate and test-fire are always POST/DELETE.

### Session cookie vs token in the browser

| | Session cookie (HttpOnly, SameSite=Strict) | Admin/bearer token held in JS (`Authorization` header) |
|---|---|---|
| CSRF | Needs defence (`CrossOriginProtection`) | Immune: no ambient credential |
| DNS rebinding | Cookie not sent to the rebinding host. Host check still needed for unauthenticated routes | Immune for authenticated calls |
| XSS impact | Attacker acts while the tab is open, but cannot read the cookie | **Exfiltrates the long-lived master admin token** |
| Other localhost ports | Cookie sent to them (RFC 6265 §8.5). Mitigate with short TTL and server-side revocation | Not exposed |
| Works with plain links, full-page nav, htmx SSE | Yes | No. EventSource can't set headers (htmx 4's fetch-based `hx-sse` can) |
| Revocation | Server-side session table: logout, TTL, rotate-all | Rotating the admin token breaks the CLI too |

A session cookie wraps a *derived, revocable* credential, so the master token never lives in browser storage. Use it.

### How the reference projects do it

**Syncthing** (GUI on `127.0.0.1:8384` by default):
- *Host check.* When the GUI address is loopback and `insecureSkipHostcheck` is false, `localhostMiddleware` returns 403 "Host check error" unless `Host` is `localhost`, `localhost.`, `*.localhost` or a loopback IP ([lib/api/api.go](https://github.com/syncthing/syncthing/blob/main/lib/api/api.go)). It was added in v0.14.6 because it "protects against most forms of DNS rebinding attack against the GUI" ([FAQ](https://docs.syncthing.net/users/faq.html); [config `insecureSkipHostcheck`](https://docs.syncthing.net/users/config.html)).
- *CSRF.* Every `/rest` call needs header `X-CSRF-Token-<deviceID>` matching a cookie token (max 25 tokens, 1 h). Requests with a valid `X-API-Key` or `Authorization: Bearer` skip CSRF ([lib/api/api_csrf.go](https://github.com/syncthing/syncthing/blob/main/lib/api/api_csrf.go)).
- *Session.* Cookie `sessionid-<shortID>`, with `Secure` only under HTTPS and no HttpOnly or SameSite set ([lib/api/tokenmanager.go](https://github.com/syncthing/syncthing/blob/main/lib/api/tokenmanager.go)). The per-instance cookie name avoids collisions between instances on different ports (see RFC 6265 §8.5).
- *CLI→browser login.* `syncthing browser` POSTs `/rest/auth/logintoken` with the API key. That gives a single-use token valid for **10 s** (max 5). It then opens `URL?token=…`, and the server consumes the token, sets the session cookie and redirects to strip the query ([cmd/syncthing/main.go](https://github.com/syncthing/syncthing/blob/main/cmd/syncthing/main.go), [lib/api/api_auth.go](https://github.com/syncthing/syncthing/blob/main/lib/api/api_auth.go)). **This is the closest analogue for kman.**

**Grafana** (binds all interfaces by default; `http_addr` empty = `0.0.0.0`):
- Login cookie `grafana_session`. `cookie_samesite` defaults to `lax` and `cookie_secure` defaults to `false`. Idle lifetime is 7 d and max lifetime 30 d, with token rotation ([configuration docs](https://grafana.com/docs/grafana/latest/setup-grafana/configure-grafana/)).
- CSRF is Origin-vs-Host only, and only when the login cookie is present (`csrf_always_check` off by default), with no Fetch Metadata. It **ignores the port**: Grafana's own test "port does not have to match" accepts `Origin: http://localhost:3000` against `Host: localhost:80` ([pkg/middleware/csrf/csrf.go](https://github.com/grafana/grafana/blob/main/pkg/middleware/csrf/csrf.go), [csrf_test.go](https://github.com/grafana/grafana/blob/main/pkg/middleware/csrf/csrf_test.go)). As a result, a same-host-other-port page passes this check. Other Grafana layers that might also apply weren't checked.
- DNS rebinding: `enforce_domain` redirects when Host doesn't match, but is **off by default**.
- Lesson: this is the default to avoid for a key vault. It also shows why SameSite alone failed (CVE-2022-21703).

**Tailscale web client** (React SPA embedded via a prebuilt module):
- CSRF: its own middleware does exactly the Filippo algorithm. It allows GET/HEAD/OPTIONS, allows `Sec-Fetch-Site: same-origin`, rejects any other value, and otherwise requires Origin host == Host (or `OriginOverride` behind a proxy), denying a missing Origin ([client/web/web.go](https://github.com/tailscale/tailscale/blob/main/client/web/web.go)).
- DNS rebinding: in manage mode, `requireTailscaleIP` redirects any request whose Host isn't the bare Tailscale IP (or `100.100.100.100`). The code comment says this "protects against DNS rebinding attacks" (ibid.).
- Session: cookie `TS-Web-Session`, `HttpOnly`, `SameSite=Strict`, 30 d, not `Secure` because it is served over HTTP on WireGuard-encrypted IPs ([web.go](https://github.com/tailscale/tailscale/blob/main/client/web/web.go), [auth.go](https://github.com/tailscale/tailscale/blob/main/client/web/auth.go)).
- Headers: `X-Frame-Options: DENY`, `CSP default-src 'self'` with hashed inline script, and `Cross-Origin-Resource-Policy: same-origin`.
- Defaults: read-only until a "check mode" re-authentication ([KB 1325](https://tailscale.com/kb/1325/device-web-interface)).

**Caddy admin API** (`localhost:2019`, no auth, a JSON API rather than a UI):
- Host check: when the listen address is loopback, allowed origins default to `localhost:PORT`, `::1:PORT` and `127.0.0.1:PORT`, and `Host` must match ("DNS rebinding mitigation").
- Origin check: `Origin` is checked whenever it, or `Sec-Fetch-Mode`, is present, or when `enforce_origin` is set. It also rejects `Sec-Fetch-Mode: no-cors`, `Origin: null` and **all WebSocket upgrades**.
- Unix sockets skip Host/Origin checks entirely, because "browsers do not allow access to unix sockets" and DNS is irrelevant there, so enforcing them is "security theater" ([admin.go](https://github.com/caddyserver/caddy/blob/master/admin.go); [options docs](https://caddyserver.com/docs/caddyfile/options#admin); [API docs](https://caddyserver.com/docs/api)). The docs recommend a permissioned Unix socket if untrusted code runs on the host.
- kman's admin API already matches Caddy's posture, plus a bearer token.

## 3. Recommendation for kman

**Build:** use templ (commit `*_templ.go`, plus a CI `templ generate && git diff --exit-code` check) or `html/template`. Vendor `htmx.min.js` and the SSE extension under `internal/dashboard/static/` and embed them. Precompress them only if you care about 30-40 KB. Don't use npm or Vite, and don't make `go generate` a build requirement. Run one SSE endpoint per page, multiplexing detection, spend and key-state events as named SSE events that each carry a server-rendered fragment.

**Serving:** mount the dashboard under `/ui/` on the admin TCP listener. If `admin_listen` is `unix://`, browsers can't reach it, so offer an opt-in `dashboard_listen: 127.0.0.1:<port>`.

**Auth flow:**
1. `kman dashboard` (or `kman ui --web`) authenticates to the admin API with the admin token, over the socket or TCP. It calls `POST /admin/v1/dashboard/login-tokens` to get a single-use nonce (random 32 bytes, about 30-60 s, store the hash), then opens `http://127.0.0.1:8081/ui/login?t=<nonce>`.
2. The server consumes the nonce, creates a session (random 32 bytes, SHA-256 stored in memory or SQLite, audit-logged), sets the cookie, and returns a 303 redirect without the query. This is the Syncthing pattern.
3. Fallback: a login form where the operator pastes the admin token. Make it a POST behind `CrossOriginProtection` (this also covers login CSRF), compare in constant time against the stored hash, and rate-limit attempts.
4. Cookie: `kman_session_<instance-id>=…; HttpOnly; SameSite=Strict; Path=/`, with `Secure` when served over TLS. Idle TTL about 12 h and absolute TTL about 7 d (both configurable). Logout deletes the server-side session. **Admin-token rotation revokes all sessions.**

**Middleware on dashboard routes, outermost first:**
- Host allowlist (DNS rebinding). Default to `localhost:P`, `127.0.0.1:P` and `[::1]:P`, matched case-insensitively, plus a configurable `dashboard_allowed_hosts` for Docker or reverse proxies (the Caddy `origins` / Syncthing `insecureSkipHostcheck` analogue). Skip it on Unix sockets.
- `http.NewCrossOriginProtection()`, with `AddTrustedOrigin` only for configured proxy origins.
- Session check.
- Response headers: `Content-Security-Policy: default-src 'self'; frame-ancestors 'none'` (htmx 4 has `hx-csp` for nonce gating), `X-Frame-Options: DENY`, `Cross-Origin-Resource-Policy: same-origin`, `Referrer-Policy: no-referrer`, and `Cache-Control: no-store` on pages that show key metadata.

The bearer-only admin API keeps working as today. Don't accept the dashboard cookie on `/admin/v1/*`. That keeps the CSRF surface confined to `/ui/`.

**Non-loopback binds:** refuse to serve the dashboard over plain HTTP on a non-loopback address unless the operator sets an explicit flag. Recommend TLS via a reverse proxy, and set `Secure` when `X-Forwarded-Proto: https` comes from a trusted proxy.

### Trade-offs accepted

- **No SPA-grade interactivity.** Charts and tables are server-rendered fragments. Rich client widgets would need small vendored JS. This is fine for one operator.
- **Generated code in the repo (templ)** and a pre-1.0 dependency. Alternatively, `html/template` adds about 3 MB and gives up compile-time checking.
- **Cookie leakage to other localhost ports (RFC 6265 §8.5).** HttpOnly doesn't stop a local server on another port from receiving the cookie when the operator browses to it. Short TTLs and revocation limit the damage. kman already accepts that same-user local processes can read `KEMAN_ADMIN_TOKEN` from the environment (keman SECURITY.md), so this doesn't widen the trust boundary much. To close it, keep the session ID in `sessionStorage` and send it as a header. That costs plain-link navigation and the htmx 2 SSE extension.
- **Legacy browsers.** Pre-2023 browsers without `Sec-Fetch-Site` fall back to a scheme-agnostic Origin==Host check. That is acceptable for a localhost tool.
- **Six-connection SSE cap** on HTTP/1.1. Use one stream per tab.

## Open questions / not verified

- Whether `__Host-` cookie prefixes are accepted on `http://localhost` in all three engines. MDN says `Secure`'s HTTPS requirement "is ignored when the Secure attribute is set by localhost" ([MDN Set-Cookie](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Set-Cookie)), but per-engine prefix behaviour wasn't tested. The recommendation doesn't depend on it.
- Whether other Grafana layers (e.g. JSON content-type enforcement) block the cross-port localhost case despite the port-ignoring Origin check. Only the CSRF middleware was read.
- The real kman binary's marginal `html/template` cost depends on its final dependency set. Re-measure once the deps are chosen.
- Firefox and Safari plans for Local Network Access weren't checked. Only Chromium's rollout is cited.

## Sources

- Go: [net/http CrossOriginProtection](https://pkg.go.dev/net/http#CrossOriginProtection), [net/http ResponseController](https://pkg.go.dev/net/http#ResponseController), [embed](https://pkg.go.dev/embed), [Go Modules Reference (zip files, go install)](https://go.dev/ref/mod), [cmd/go generate](https://pkg.go.dev/cmd/go#hdr-Generate_Go_files_by_processing_source), [linker deadcode.go](https://github.com/golang/go/blob/master/src/cmd/link/internal/ld/deadcode.go), [text/template exec.go](https://github.com/golang/go/blob/master/src/text/template/exec.go)
- CSRF/cookies/specs: [Filippo Valsorda, Cross-Site Request Forgery](https://words.filippo.io/csrf/), [OWASP CSRF Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html), [RFC 6265 §8.5](https://www.rfc-editor.org/rfc/rfc6265#section-8.5), [MDN Set-Cookie](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Set-Cookie), [MDN EventSource](https://developer.mozilla.org/en-US/docs/Web/API/EventSource), [WICG Local Network Access](https://wicg.github.io/local-network-access/), [Chrome LNA blog](https://developer.chrome.com/blog/local-network-access), [jub0bs CVE-2022-21703 write-up](https://jub0bs.com/posts/2022-02-08-cve-2022-21703-writeup/), [GHSA-cmf4-h3xc-jw8w](https://github.com/advisories/GHSA-cmf4-h3xc-jw8w)
- Syncthing: [api.go](https://github.com/syncthing/syncthing/blob/main/lib/api/api.go), [api_csrf.go](https://github.com/syncthing/syncthing/blob/main/lib/api/api_csrf.go), [api_auth.go](https://github.com/syncthing/syncthing/blob/main/lib/api/api_auth.go), [tokenmanager.go](https://github.com/syncthing/syncthing/blob/main/lib/api/tokenmanager.go), [cmd/syncthing/main.go](https://github.com/syncthing/syncthing/blob/main/cmd/syncthing/main.go), [FAQ](https://docs.syncthing.net/users/faq.html), [config](https://docs.syncthing.net/users/config.html)
- Grafana: [configuration](https://grafana.com/docs/grafana/latest/setup-grafana/configure-grafana/), [csrf.go](https://github.com/grafana/grafana/blob/main/pkg/middleware/csrf/csrf.go), [csrf_test.go](https://github.com/grafana/grafana/blob/main/pkg/middleware/csrf/csrf_test.go)
- Tailscale: [client/web/web.go](https://github.com/tailscale/tailscale/blob/main/client/web/web.go), [auth.go](https://github.com/tailscale/tailscale/blob/main/client/web/auth.go), [assets.go](https://github.com/tailscale/tailscale/blob/main/client/web/assets.go), [package.json](https://github.com/tailscale/tailscale/blob/main/client/web/package.json), [web-client-prebuilt](https://github.com/tailscale/web-client-prebuilt), [KB 1325](https://tailscale.com/kb/1325/device-web-interface)
- Caddy: [admin.go](https://github.com/caddyserver/caddy/blob/master/admin.go), [API docs](https://caddyserver.com/docs/api), [Caddyfile global options: admin](https://caddyserver.com/docs/caddyfile/options#admin)
- templ: [CI/CD (commit generated files)](https://github.com/a-h/templ/blob/main/docs/docs/09-developer-tools/06-cicd.md), [live reload](https://github.com/a-h/templ/blob/main/docs/docs/09-developer-tools/03-live-reload.md), [testing](https://github.com/a-h/templ/blob/main/docs/docs/04-core-concepts/03-testing.md)
- htmx: [SSE extension (2.x)](https://htmx.org/extensions/sse/), [htmx 4.0.0 announcement](https://github.com/bigskysoftware/htmx/blob/four/www/src/content/announcements/2026-08-28-htmx-4.0.0-is-released.md), [hx-sse 4.x source](https://github.com/bigskysoftware/htmx/blob/four/src/ext/hx-sse.js), [hx-csp](https://github.com/bigskysoftware/htmx/blob/four/www/src/content/extensions/14-hx-csp.md)
- Measurements: local builds in `/tmp/kman-research/dashboard-scratch/gobin/` and `/tmp/kman-research/dashboard-scratch/spa/` (Vite 8.3.5 `create-vite` templates).
