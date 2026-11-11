# Security policy

## Reporting a vulnerability

**Report it privately**, through GitHub's private vulnerability reporting: open the [Security tab](https://github.com/kirank55/kman/security) of this repository and choose **Report a vulnerability**, or go straight to <https://github.com/kirank55/kman/security/advisories/new>. Please do not open a public issue or pull request for a vulnerability.

Say what you found, the version (`kman version`), and how to reproduce it. A proof of concept helps; a real credential or a real key never does, so use made-up ones.

**What to expect:**

- an acknowledgement within **7 days**;
- a fix, and a public disclosure, within **90 days** of your report.

## What counts

Anything that breaks what kman promises: a credential, a virtual key's secret, an admin token or the master key reaching somewhere it should not (the [invariants](SPEC.md#1-invariants) say what that is); a capped or allowlisted key getting a request through unmetered; a disabled, revoked or expired key still working; a way to hold the proxy's memory or connections without a valid key; a flaw in how a release is built, signed or installed. [The threat model](THREAT-MODEL.md) says what kman does and does not defend against, and lists the limits we already know, so you can tell a gap from a design choice. If in doubt, report it.

## Supported versions

The **latest release** gets fixes. Before v1.0.0 there is no support for older ones, so upgrade ([how](docs/operator-guide/upgrade.md)). After v1.0.0 the latest minor release is supported.

## Verify what you install

Every release is signed and attested, and `install.sh` checks it for you. To check one yourself, with the exact commands and the signing identity to check against, see **[docs/verify.md](docs/verify.md)**.
