# kman makes no network call the operator didn't configure or ask for

kman never contacts the network on its own. Its outbound traffic is limited to the upstreams and alert channels the operator configured, plus commands the operator runs on purpose (`kman version --check`, `kman geoip update`). There is no telemetry, no crash reporting, and no update check by default. An operator who wants update checks turns on `update_check: true`, which asks the GitHub releases API once a day and reports the answer in the status summary, the dashboard, and a metric.

The cost is that an install can fall behind silently, and since price data ships only in releases (ADR 0007), a stale install also enforces caps with stale prices. kman covers that without the network by showing how old the binary and its price table are ("built 74 days ago"), in the status summary and on the dashboard, which works air-gapped.

## Considered options

- **A daily release check, on by default, with an opt-out.** Rejected. It only sends an IP address and a User-Agent, but a proxy that holds every upstream credential should be quiet by default, and keman already promised it "does not phone home". Reversing that later would cost more trust than the check is worth.
- **Telemetry or crash reporting, opt-in.** Rejected for v1. Nothing in the solo tier needs it, and it would add a data path to review in the threat model.

## Consequences

- Operators learn about releases from the status summary's age line, `kman version --check`, or GitHub's release notifications. Security advisories are published as GitHub Security Advisories.
- Any future feature that would make kman reach out by itself (price feeds, update checks on by default, a license server for the team tier) has to amend this ADR.
