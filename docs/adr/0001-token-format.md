# Token format: fixed prefixes, a checksum, and the prefix as the version

Virtual keys are `kmn_<id:8>_<secret:43><crc:6>` and admin tokens are `kma_<secret:43><crc:6>`, all base62. The secret is 256 random bits. Only its hash is stored. The trailing 6 characters are a base62 CRC32 of everything before them. Canary keys and child keys use the virtual-key format unchanged. A key's kind lives on the server and never in the token. Once tokens are issued, this format can't change without breaking every app that holds one, so it is fixed before v1.

## Considered options

- **No checksum (keman's format).** Rejected. A checksum lets `kman scan` and third-party secret scanners confirm a match offline. It also lets the proxy reject truncated or mistyped keys before any lookup. It can't be added after keys exist. It is not a security control: the secret alone carries the entropy.
- **Canaries disguised as provider keys (a fake `sk-proj-…`).** Rejected. A thief would use such a key against the provider directly, where kman never sees it. A kman canary can only fire through kman. A recognizable `kmn_` prefix tells a thief that a key belongs to kman, but nothing in the token says whether it is a canary.
- **A version byte inside the token.** Rejected. The prefix is the version. If the format ever changes, kman mints a new prefix and keeps parsing the old one until those keys expire.

## Consequences

- kman starts clean. It does not read keman's database, and keys issued by keman are not honored, even though they share the `kmn_` prefix. keman keys have no checksum, so they fail validation. keman had about 12 binary downloads, so no installed base was worth a migrator.
