# Rotating keys and replacing a leaked credential

Two commands answer a leak. Both are one transaction, take effect on the next request with no restart, and are audited (`kman audit`).

## Rotate a key

```sh
kman key rotate my-app [--grace 24h]
```

- The key stays: its id, spend, policy, expiry and audit history are untouched, so nothing resets. Only its secret changes, and the new one is printed once.
- With no `--grace`, the old secret stops at the next request. With `--grace <duration>` (at most 30 days), the old secret works beside the new one until the grace ends, so an app can be moved over without a gap. `--grace 0` is the same as none.
- A key holds at most two working secrets. Rotating again inside a grace ends the older secret at once.
- When a grace ends, the old secret becomes a **retired secret**: kman keeps its hash for 30 days, for detection, and it never authenticates again. `kman serve` does this as time passes, with no one asking; the request path stops the secret at the end of its grace either way.
- A revoked key cannot be rotated. A disabled key can, and stays disabled. A rotation does not extend the key's expiry.
- If the rotation fails, the key is exactly as it was.

## Replace a leaked credential

```sh
kman credential compromise openai
```

First revoke the leaked real key at the provider and make a new one: nothing kman does reaches the provider. Then this reads the new secret (asked for without echo, piped, or from `--credential-env VAR`), replaces the credential's secret, and rotates every virtual key on the credential that is not revoked, with no grace, in one transaction. It lists those keys, which are the apps that need new keys, each with its new secret, shown once. If anything fails, nothing changes.

To replace the real secret without touching the virtual keys, use `kman credential update`.

See also [A key leaked and A real credential leaked](../v0.1.0-scope.md#a-key-leaked), and the [CLI reference](../cli.md).
