# The live event feed drops for a slow client and never waits

`kman events --follow` shows requests as they happen (SPEC §9). It is one more subscriber on the event bus, so it must not be able to slow a request down, and a person watching a terminal is the least reliable consumer kman has.

- **One subscription per open stream, which drops when full.** `GET /admin/v1/events/stream` subscribes to the bus (as `event-stream`) when a client connects and unsubscribes when it leaves. The bus already drops for a subscription whose buffer is full (256 events) instead of waiting, so a client that cannot keep up costs the request path nothing. `Bus.Unsubscribe` is new: before it, a subscription lived as long as the bus.
- **The slow client loses events and is told; its connection stays open.** The stream sends `event: dropped` with the number it has missed, and `kman events --follow` prints that on stderr. The loss is counted in `kman_event_subscriber_dropped_total{subscriber="event-stream"}`, summed over every stream, including those that have closed, so the counter never goes down when a client leaves.
- **Streams are bounded and end with kman.** At most 16 are open at once (a 17th is refused with 409). `kman serve` ends every stream before it shuts its listeners, so a follower never keeps kman waiting for its grace period. A stream also ends when its client leaves, which frees its subscription.
- **A streamed event is the stored event minus what only the store knows.** It has no id (it is not stored yet), and its AS organisation comes from the bus event, where a stored event's is the label of the key's place for that ASN. Either way the client address follows [ADR 0010](0010-client-addresses-are-opt-in-on-the-admin-api.md): left out unless `expose_client_addresses` is on, read again for each batch of events.
- **The list and the stream are different moments.** The store writer flushes every 5 seconds, so `GET /admin/v1/events` can trail the stream by that long. `kman events --follow` prints the stored events first and then the stream, which shows what happens after it connects.

## Considered options

- **Disconnect a client that falls behind.** Rejected. A follower that is behind for a moment during a burst is exactly the operator watching a leak, and a feed that drops them, or makes them reconnect and lose the gap anyway, is worse than one that says how many it skipped. A connection that stops reading altogether holds one goroutine and a 256-event buffer, bounded by the cap on streams and by the TCP keep-alive that notices a client that is gone.
- **Replay from the store when a client connects or falls behind.** Rejected for now. The store lags the bus by up to 5 seconds, so a replay would have a seam that is hard to explain.
- **Let a slow client block, with a write deadline.** Rejected. A deadline needs the wall clock, which only `main` reads (SPEC §17), and it would make the feed's behaviour depend on how long a terminal takes to scroll.

## Consequences

- An event emitted a moment before a follower connects, and not yet written to the store, is in neither its backfill nor its stream.
- A client that does not read at all is not disconnected; the cap on streams and the TCP keep-alive bound it.
- `admin` imports `events` (SPEC §2) and takes the bus as an option, so `kman serve` makes the bus before the admin service. A process with no bus, such as `kman init`, answers a stream with a 409.
