# The TUI is a separate, thin admin-API client

kman now has two terminal UIs, and this ADR is about the second. `kman tui` is a read-only view written in Go, inside the binary (browse keys, exposure, audit and upstreams). `tui/` is a React app on Ink that watches keys, recent requests, spend, upstreams and the audit log, and can also disable, enable, revoke and create virtual keys, and answer an incident: acknowledge it, say it was you, resolve it, disable, enable, rotate or revoke its key, and reset its canary. Both are the later that ADR 0004 left open ("a TUI can come back later as a thin client of the admin API"), and both keep that ADR's rules.

- **It is only an admin API client.** What it flags among the requests it works out itself, from the events, canaries and incident details the API already gives. Every change is one request to `/admin/v1`, under the admin token it is run with, so the audit log names the same actor as for the CLI. It imports nothing from the Go code and adds no endpoint. Nothing can be done in it that the CLI cannot do (ADR 0004).
- **It does not run the server.** keman's beta TUI ran the proxy inside the terminal, so the proxy stopped when the terminal closed. This one connects to a `kman serve` that is already running, and quitting it changes nothing.
- **It reads the CLI's credentials.** The credentials file in the config directory, overridden by `KMAN_ADMIN_URL` and `KMAN_ADMIN_TOKEN`, the same rules as `internal/cli`. Its only network traffic is to that admin address (ADR 0008). It has no telemetry and no update check.
- **It is not in the kman binary.** It needs Node 22 or later and is installed and run separately (`tui/README.md`). The Go binary stays one static file, and the TUI is not part of a release archive, so a security fix to kman never waits on it.
- **A secret is shown once, and held only on screen.** The secret of a created or rotated key lives only in the dialog that shows it, is never logged or written, and is cleared when the operator presses Enter. Esc does not close that dialog.

## Considered options

- **Adding the actions to `kman tui`.** Not done. `kman tui` is read-only by design (its docs say to use the CLI to change things), and the request for this one was a React interface. A separate package also keeps its cost away from the Go release path.
- **Leave it to `kman audit --follow` and the dashboard.** Still true for what they cover. The TUI is the quick look over SSH that needs no tunnel and no second tool.

## Consequences

- **Two TUIs overlap.** Browsing keys, audit and upstreams exists in both. ADR 0004 rejected two rich UIs for their maintenance cost, and this is two. Whether to keep both, or to fold one into the other, is open.

- CI builds, type-checks and tests `tui/` as its own job, which `required` waits on. The Go gates do not look at it.
- Two lists of the admin API's shapes now exist: the OpenAPI document and the TypeScript types in `tui/src/api.ts`. They are written by hand, and only the fields the TUI reads are in them. If the API changes a field the TUI uses, the TUI's tests against its own fake will not notice. The run against a real kman, described in `tui/README.md`, will.
- The TUI cannot do what only the CLI may: `init`, `keygen`, `rekey`, admin-token management.
