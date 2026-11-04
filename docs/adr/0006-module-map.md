# Module map: a snapshot-read hot path, pure policy, and an enforced dependency rule

kman is split into deep modules, one Go package per module under `internal/`. The package import direction is checked by a test that runs `go list` and fails on any violation:

- **Transports** (`adminapi`, `dashboard`, `cli`) import only `admin`. Nothing imports a transport.
- **`admin`** is the one mutation path (ADR 0004). It imports `store`, `registry`, `vault`, `events`, and `sessions`. Each command runs one store transaction, then publishes the change to the registry, then records an audit entry carrying the actor.
- **`gateway`** is the proxy listener. It imports `registry`, `policy`, `limiter`, `meter`, `vault`, `profiles`, `events`, and `sessions`, and never imports `admin`.
- **`token`** is a leaf package holding the token types (ADR 0001) that several modules share: `gateway` parses tokens, `admin` mints them, `sessions` mints child keys. It imports no internal package, and any module may import it, without counting against the lists above.

The structural choices that would surprise a reader:

- **The hot path takes no locks.** `registry` holds an immutable snapshot of keys, policies, and upstreams, swapped atomically. Every changed row bumps a `generation` number, so a later second instance can rebuild its snapshot from a change feed. This replaces keman's single global lock, which the request path took about eight times per request.
- **`policy` is a pure function.** Rate limits live in `limiter`, which has bounded memory, and spend lives in `meter`.
- **`meter` owns all spend.** Its interface is `Reserve`, `Settle`, and `Spend`. The counter store has two adapters from day one, SQLite increments and an in-memory one for tests. A shared counter for the team tier would be a third adapter.
- **`store` has no port yet.** There is one SQLite adapter, and tests use real SQLite. Its interface is still shaped around the domain: `Do(ctx, func(Tx) error)`, typed errors, and ordered migrations tracked by `user_version`. Rows carry `generation`, credentials carry `dek_id`, and audit entries carry `actor`. Postgres arrives later as a second adapter, not a rewrite.
- **`detect` returns decisions and never mutates.** `admin` executes an auto-disable using the same command an operator would, with the actor `system:detect`. Canary and disabled-key events take a priority lane in `events` and go to the persisted `alerts` outbox without waiting on the event-store write.

## Considered options

- **A port for the store now, so Postgres could be plugged in.** Rejected. With only one adapter, the port would be indirection with nothing to swap. The domain-shaped interface keeps that option open at no cost.
- **Locked maps with finer-grained locks (keman's model, refined).** Rejected. Lock contention is only part of the problem. A snapshot also gives the change-feed seam the team tier needs.
- **Letting `detect` disable keys directly (keman).** Rejected. It raced with operator commands and bypassed the mutation path.

## Consequences

- `events` and `alerts` ship in M1, with audit, soft-budget, and credential/expiry producers. `detect` is added as one more subscriber in M2.
- Admin tokens are named, and an install can have more than one, so every change has a meaningful actor. A solo install starts with `owner`.
- Child keys are the deliberate exception to the mutation path. They are minted by `sessions`, held only in memory, and never persisted (see ADR 0004).
