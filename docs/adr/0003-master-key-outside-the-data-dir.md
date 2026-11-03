# The master key never lives in the data directory

kman refuses to start when its master key sits inside the data directory, unless config sets `allow_key_in_data_dir: true`. keman's spec told it to write `master.key` next to the config and the database. So any backup, `rsync`, or shared volume of the data directory held both the ciphertext and the key that decrypts it, and keman's own threat model listed that as an unresisted threat.

`kman init` writes the key to a config directory kept apart from the data directory. The master key can also come from systemd-creds, an environment variable (with a warning at startup), or a passphrase sealed with Argon2id. All of them sit behind one key-source seam. In Docker the volume *is* the data directory, so `kman keygen` prints a key to mount as a secret, and `kman init` refuses to run in a container that has no key source.

## Considered options

- **Warn instead of refusing.** Rejected. A warning on every start gets ignored, and that would leave keman's weakness as kman's default.
- **The macOS keychain as a key source.** Rejected for v1 after testing on macOS 15.8.2 and 26.6.2 (*Verify macOS keychain protection for the master key*, #14). Pure-Go access goes through `/usr/bin/security`, so the item has to trust that tool, and then any same-user process that runs `security find-generic-password` reads the key with no prompt. An item that trusts nothing blocks kman itself behind a GUI prompt, which is useless for a service. The keychain is therefore no better than a 0600 key file. A future adapter could return behind the same seam: a signed helper, or a CGO build using the Security framework directly.

## Consequences

- No source protects the key from root, or from a same-user process that reads the key file. THREAT-MODEL.md says so.
- `rekey` runs offline. It replaces the data key as well as the master key and re-encrypts every credential in one transaction, then config is pointed at the new key source.
