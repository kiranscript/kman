# One mutation path, and nothing is dashboard-only

Every change to a running kman goes through the admin API's service layer, which commits to the database before it updates memory. The CLI is a typed client of the versioned `/admin/v1` API and covers all of it. The dashboard calls the same service layer in-process and covers only part of it. There is no action you can click that you can't also script. Install-level and offline operations (`init`, `keygen`, `rekey`, admin-token rotation, `geoip update`, `scan`) exist only in the CLI.

keman's beta also had a TUI, the default command, which ran the server inside the terminal. kman v1 drops the TUI. Watching kman over SSH is covered by `kman detections --follow`, `kman audit --follow`, and `kman dashboard` printing a one-time login link plus the matching `ssh -L` command when the host has no display.

## Considered options

- **Keep a slimmed-down TUI as well as the dashboard.** Rejected for v1. In keman, two rich UIs were the biggest maintenance cost (about 3.8K lines). Running the server inside the TUI also meant the proxy stopped whenever the terminal closed. A TUI can come back later as a thin client of the admin API.
- **Let the dashboard call the admin API over HTTP.** Rejected. It would add a loopback hop and a second auth path for no gain, because the service layer is the actual contract.

## Consequences

- The dashboard's session cookie is never accepted on `/admin/v1/*`, so the CSRF surface stays confined to `/ui/`.
- The OpenAPI document is generated from the Go request and response types, so it can't drift from the code.
- The audit log is an alert producer (SPEC §9): every entry a command appends is handed to the alerts in the same transaction, so an alert is never lost with a change that committed. Retention's prune (`system:retention`) goes the same way when it deletes something.
- Two things write the audit log without an alert, because the server is stopped when they run: `kman restore` (`data.restore`) and `kman rekey` (`data_key.rekey`) append their entry directly, so it shows in `kman audit` but nothing is queued or delivered. They are offline operations by design, run by someone at the machine.
- `SettleSecrets` is the one housekeeping pass that changes the store with no audit entry and no alert. It only moves what time has already decided (a rotated key's previous secret whose grace has ended, and a retired secret kept 30 days); the rotation that started the grace was audited and alerted when it happened.
- Child keys are the one exception to the mutation path. They are ephemeral: minted on the proxy listener, held only in memory, and recorded through the realtime session's close event rather than the audit log. Everything durable goes through the admin API.
