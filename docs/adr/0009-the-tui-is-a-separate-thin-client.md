# The TUI is a separate, thin admin-API client

kman has a terminal UI again, in `tui/`: a React app on Ink that watches keys, spend, upstreams and the audit log, and can disable, enable, revoke and create virtual keys. It is the later that ADR 0004 left open ("a TUI can come back later as a thin client of the admin API"), and it keeps that ADR's rules.

- **It is only an admin API client.** Every change is one request to `/admin/v1`, under the admin token it is run with, so the audit log names the same actor as for the CLI. It imports nothing from the Go code and adds no endpoint. Nothing can be done in it that the CLI cannot do (ADR 0004).
- **It does not run the server.** keman's beta TUI ran the proxy inside the terminal, so the proxy stopped when the terminal closed. This one connects to a `kman serve` that is already running, and quitting it changes nothing.
- **It reads the CLI's credentials.** The credentials file in the config directory, overridden by `KMAN_ADMIN_URL` and `KMAN_ADMIN_TOKEN`, the same rules as `internal/cli`. Its only network traffic is to that admin address (ADR 0008). It has no telemetry and no update check.
- **It is not in the kman binary.** It needs Node 22 or later and is installed and run separately (`tui/README.md`). The Go binary stays one static file, and the TUI is not part of a release archive, so a security fix to kman never waits on it.
- **A secret is shown once, and held only on screen.** The secret of a created key lives only in the dialog that shows it, is never logged or written, and is cleared when the operator presses Enter. Esc does not close that dialog.

## Considered options

- **A Go TUI inside the binary.** Rejected here. It would keep the binary single, but the request was for a React interface, and a separate package keeps the cost of a second UI (ADR 0004's objection) away from the Go release path.
- **Leave it to `kman audit --follow` and the dashboard.** Still true for what they cover. The TUI is the quick look over SSH that needs no tunnel and no second tool.

## Consequences

- CI builds, type-checks and tests `tui/` as its own job, which `required` waits on. The Go gates do not look at it.
- Two lists of the admin API's shapes now exist: the OpenAPI document and the TypeScript types in `tui/src/api.ts`. They are written by hand, and only the fields the TUI reads are in them. If the API changes a field the TUI uses, the TUI's tests against its own fake will not notice. The run against a real kman, described in `tui/README.md`, will.
- The TUI cannot do what only the CLI may: `init`, `keygen`, `rekey`, admin-token management.
