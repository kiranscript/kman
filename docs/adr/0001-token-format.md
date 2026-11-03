# Token format: fixed prefixes, a checksum, and the prefix as the version

Virtual keys are `kmn_<id:8>_<secret:43><crc:6>`, child keys are `ek_kmn_<id:8>_<secret:43><crc:6>`, and admin tokens are `kma_<secret:43><crc:6>`, all base62. The secret is 256 random bits. Only its hash is stored. The trailing 6 characters are a base62 CRC32 of everything before them, prefix included. Canary keys use the virtual-key format unchanged: a key's kind lives on the server and never in the token, with one exception, child keys. Once tokens are issued, this format can't change without breaking every app that holds one, so it is fixed before v1.

The official OpenAI browser SDKs (`openai-node`'s realtime WebSocket, and `@openai/agents-realtime` over WebSocket and WebRTC) refuse any credential that doesn't start with `ek_` unless the app sets an opt-out flag. A child key is short-lived by design (60 seconds to connect, one session), so the `ek_` prefix tells the truth about it, and those SDKs accept it without changes.

## Considered options

- **No checksum (keman's format).** Rejected. A checksum lets `kman scan` and third-party secret scanners confirm a match offline. It also lets the proxy reject truncated or mistyped keys before any lookup. It can't be added after keys exist. It is not a security control: the secret alone carries the entropy.
- **Canaries disguised as provider keys (a fake `sk-proj-…`).** Rejected. A thief would use such a key against the provider directly, where kman never sees it. A kman canary can only fire through kman. A recognizable `kmn_` prefix tells a thief that a key belongs to kman, but nothing in the token says whether it is a canary.
- **A version byte inside the token.** Rejected. The prefix is the version. If the format ever changes, kman mints a new prefix and keeps parsing the old one until those keys expire.
- **Child keys in the plain `kmn_` format, with docs telling apps to set `dangerouslyAllowBrowser` or `useInsecureApiKey`.** Rejected. Those flags don't only let kman's key through: they turn the SDK's browser guard off for every key, including a real `sk-…` or a parent `kmn_` key shipped to a browser by mistake.
- **`ek_` as an optional wrapper, with the checksum over the inner `kmn_…` part.** Rejected. kman would either accept `ek_` on standard keys, which lets apps slip long-lived keys past the SDK guard, or need a kind check to refuse them. With the checksum over the whole token, `ek_` on a standard key or a canary, or a child key without it, simply fails validation.

## Consequences

- kman starts clean. It does not read keman's database, and keys issued by keman are not honored, even though they share the `kmn_` prefix. keman keys have no checksum, so they fail validation. keman had about 12 binary downloads, so no installed base was worth a migrator.
- A child key is recognizable as one. That's harmless: the kind rule exists to keep canaries indistinguishable, and a thief learns only that the token dies within a minute.
- Every child key carries `ek_`, however it was minted (the intercepted OpenAI or xAI client-secret endpoints, or kman's native mint route), so there is one form.
- The scanner pattern is `\b(?:ek_)?kmn_[0-9A-Za-z]{8}_[0-9A-Za-z]{49}\b`, both in `kman scan` and in the regex published for third-party scanners. `\bkmn_` alone would miss child keys, since `_` is a word character. `kman scan` reports a matched child key at lower severity, labelled "child key (short-lived)".
- If OpenAI tightens its check (length, a server round trip), `ek_` stops being enough. The pinned-SDK e2e suite runs both SDKs' browser flows with no opt-out flag on every PR and against the latest SDKs weekly, so a break shows up before users hit it. The docs mention the opt-out flags only under troubleshooting, as a stopgap, with a warning about what they turn off.
