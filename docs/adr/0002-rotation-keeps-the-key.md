# Rotation replaces the secret and keeps the key

When a virtual key is rotated, it stays the same key with the same key id. Only its secret changes. During the grace period the key accepts two secrets, the current one and the previous one. After the grace period it accepts only the current one. A grace of zero kills the previous secret at once.

keman created a new key with a new id and copied state across. Because of that, rotation reset spend budgets and the learning period, and a rotation that failed partway could leave an active key with no limits. In kman, spend counters, the baseline, open incidents, and audit history all belong to the one key, so they carry over without any copying.

## Considered options

- **A new key linked to the old one by lineage (keman's model, made correct).** Rejected. Every piece of state would have to be either copied or looked up through the lineage chain. That is the code path keman got wrong.

## Consequences

- The key id is not secret, so it surviving a leak costs nothing. It stays visible in logs and alerts after a rotation.
- A key stores at most two secret hashes at a time. A second rotation inside the grace period ends the earlier previous secret immediately.
- After the grace period, the previous secret becomes a **retired secret**. Its hash is kept for 30 days for detection only: it never authenticates, and any use of it raises a high-severity detection. Retired hashes are not counted in the two above. (Added by *Detection quality and the alert-response loop*, #15.)
