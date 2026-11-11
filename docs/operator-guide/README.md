# Operator guide

For the person who runs kman. New here? Start with the [quickstart](../quickstart.md).

- [Install](install.md): every channel and platform, macOS quarantine, the Windows SmartScreen warning, where kman keeps its files.
- [Key sources](key-sources.md): where the master key lives, and how to choose.
- [systemd](systemd.md): the `.deb` and `.rpm` packages and the service.
- [Docker and Compose](docker.md): the image and the Compose example.
- [TLS and reverse proxies](tls.md): getting an app on another machine to kman safely.
- [Address locks and GeoIP](geoip.md): lock a key to networks or countries, and `kman geoip update`.
- [Upgrade, rekey and old copies](upgrade.md): upgrades, the `kman.db.pre-<NNNN>` copies, backing up, replacing the master key.

And elsewhere:

- [Keys, policy and spend](../policy.md): caps, allowlists, rate limits, what is charged, the errors an app sees.
- [Using kman from your code](../sdks.md).
- [What v0.1.0 leaves out](../v0.1.0-scope.md), and what to do meanwhile: a leaked key, a leaked credential, no alerts.
- [Price data](../price-data.md), [CLI reference](../cli.md), [admin API reference](../admin-api/README.md).
- [Threat model](../../THREAT-MODEL.md), [security policy](../../SECURITY.md), [verify a release](../verify.md).
