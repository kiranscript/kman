# The master key never lives in the data directory

kman refuses to start when its master key sits inside the data directory, unless config sets `allow_key_in_data_dir: true`. keman's spec told it to write `master.key` next to the config and the database. So any backup, `rsync`, or shared volume of the data directory held both the ciphertext and the key that decrypts it, and keman's own threat model listed that as an unresisted threat.

`kman init` writes the key to a config directory kept apart from the data directory. The master key can also come from systemd-creds, an environment variable (with a warning at startup), a passphrase sealed with Argon2id, or the OS keychain. All of them sit behind one key-source seam. In Docker the volume *is* the data directory, so `kman keygen` prints a key to mount as a secret, and `kman init` refuses to run in a container that has no key source.

## Considered options

- **Warn instead of refusing.** Rejected. A warning on every start gets ignored, and that would leave keman's weakness as kman's default.
- **Make the OS keychain the default.** Rejected for now. Pure-Go macOS access goes through `/usr/bin/security`. Whether other processes running as the same user can read those items silently is still unverified, and a headless server or a container has no keychain.

## Consequences

- No source protects the key from root, or from a same-user process that reads the key file. THREAT-MODEL.md says so.
- `rekey` runs offline. It replaces the data key as well as the master key and re-encrypts every credential in one transaction, then config is pointed at the new key source.
