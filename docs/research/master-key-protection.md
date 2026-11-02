# Master-key and in-memory secret protection for a self-hosted Go binary

Ticket: kirank55/kman#4. Researched 2026-11-02.

**Question.** What options exist for protecting kman's master key at rest and its decrypted credentials in memory, for a self-hosted, single-binary, `CGO_ENABLED=0` Go program on Linux, macOS, and Windows, without a cloud KMS?

**Method.** I read primary sources: man pages, vendor docs, RFCs, and library source code at the exact released versions, fetched into the Go module cache. Release dates come from `proxy.golang.org/<module>/@latest`, and repo activity from the GitHub API. Latency numbers are my own benchmarks (Go 1.27.3, linux/amd64, i5-13420H under WSL2, `CGO_ENABLED=0`). The `/proc` and dumpable behaviour was tested empirically on the same machine. Anything not verified against a primary source is marked **(unverified)**.

---

## 1. Baseline: what keman does today

- `init` writes `master.key` (mode 0600) next to the config and DB. The key can also come from `KEMAN_MASTER_KEY` (env) or `KEMAN_MASTER_KEY_FILE` / `master_key_file`. Sources: `keman/SECURITY.md` ("What you must protect"), `internal/cli/cli.go:184-192`.
- Envelope: the master key wraps one DEK (AES-256-GCM, AAD `keman-dek-v1`), and the DEK encrypts each credential with the credential id as AAD (`internal/cryptoenc/box.go`).
- At startup `App.load` decrypts **every** credential once and stores it as a Go `string` (`c.Plaintext = string(plain)`, `internal/app/app.go:320-330`). The redactor `redact.Secrets` keeps another copy.
- The threat model already concedes two things: "Memory inspection, a debugger, or a core dump. Plaintext credentials are cached." and "Someone who can read the data directory, when `master.key` is left in it (the default)" (`docs/THREAT-MODEL.md`).
- The budget is proxy overhead p50 < 3 ms and p99 < 10 ms (`beta.md:217`). Measured overhead is 71 µs p50 and 555 µs p99 on loopback (`benchmarks/RESULTS.md`).

So the real gap is not the cryptography. The key **sits beside the ciphertext** by default, and the process does nothing to limit who can read its memory.

---

## 2. Threats used to score each option

| ID | Threat |
|---|---|
| T1 | Copy of the data dir, a backup, or a shared volume |
| T2 | Offline disk theft (stolen laptop, decommissioned disk, VM snapshot) |
| T3 | Another **non-root** local user |
| T4 | A **same-UID** process (malware in the user session, compromised sibling process) |
| T5 | root / host admin / container runtime on the running host |
| T6 | Core dump or crash reporter capturing memory |
| T7 | Swap / hibernation image written to disk |
| T8 | Live memory read (ptrace, gdb, `gcore`, `/proc/pid/mem`) |
| U | Must survive an **unattended restart** (headless server, Docker, systemd `Restart=`) |

---

## 3. At rest: options for the master key

### 3.1 Plain key file (status quo) and env/file injection (Docker/K8s secrets)

- **Pure Go:** yes, stdlib only.
- **Docker:** swarm secrets are "mounted into the container in an in-memory filesystem" at `/run/secrets/<name>`. They are "only available to swarm services, not to standalone containers". On Windows containers, "secrets are persisted in clear text to the container's root disk". https://docs.docker.com/engine/swarm/secrets/
- **Kubernetes:** "Secrets are, by default, stored unencrypted in the API server's underlying data store (etcd)". Also, "anyone who is authorized to create a Pod in a namespace can use that access to read any Secret in that namespace". https://kubernetes.io/docs/concepts/configuration/secret/
- **Env var caveat (measured):** a same-UID process can read `KMAN_MASTER_KEY` from `/proc/<pid>/environ`. **`os.Unsetenv` does not scrub it.** I tested this: after `os.Unsetenv("SECRETX")`, `/proc/<pid>/environ` still contained `SECRETX=hunter2`. Go copies the environment into its own slice and leaves the kernel's initial environ block untouched.
- **Removes:** T1, if the file lives outside the data dir (a secrets mount, a different directory, an orchestrator-managed tmpfs). Moving the key out of the data dir is the single highest-value change, and it costs nothing.
- **Does not remove:** T3 if file modes are wrong, T4 (same UID reads the file or the environ), T5. It removes T2 only if the secret lives on tmpfs or an encrypted disk.
- **UX:** no prompt. Works headless, in Docker, and in K8s. This is the right default for containers.

### 3.2 systemd-creds (`LoadCredentialEncrypted=`)

- **How it works:** `systemd-creds encrypt` produces a `.cred` file encrypted with AES-256-GCM. The key comes from TPM2, `/var/lib/systemd/credential.secret` (root-only), or both. "If a TPM2 is present and /var/ is persistent, the default is to use both", which means the file decrypts only on the same hardware and OS install. On activation the service gets plaintext in `$CREDENTIALS_DIRECTORY`, placed "in non-swappable memory, using ramfs where permissions allow". Access is limited to the service's user, and credentials are not inherited the way env vars are. https://systemd.io/CREDENTIALS/
- **Versions:** `encrypt` / `--with-key=` / `--tpm2-pcrs=` were added in systemd 250. `--user` was added in 256. "systemd v262 and later also pin encrypted credentials to the TPM2's SRK", which makes them incompatible with earlier versions. https://www.freedesktop.org/software/systemd/man/latest/systemd-creds.html
- **Pure Go:** yes, and **no library is needed**. kman only reads `$CREDENTIALS_DIRECTORY/<name>`. (`github.com/coreos/go-systemd/v22` v22.7.0, 2026-01-27, exists but is not required.)
- **Removes:** T1 and T2 (the ciphertext `.cred` file is useless off the host, and with TPM2 it is useless off the machine). T3 (ramfs is readable only by the service user). Swap of the key file (T7, partially).
- **Does not remove:** T5. Root can decrypt with the host key and the TPM. T4 against the running service user. T6, T8.
- **UX:** one-time `systemd-creds encrypt` at install. No prompt. Survives unattended restart (U). **Linux + systemd only.** Not available inside a typical Docker container, and not on macOS or Windows. Best production story on Linux hosts, at zero code cost.

### 3.3 OS keychains

| Platform | Pure-Go library (version, date) | Mechanism | Notes |
|---|---|---|---|
| macOS | `github.com/zalando/go-keyring` v0.2.8 (2026-03-23; repo pushed 2026-10-16; 39 open issues) | Runs `/usr/bin/security`. `Set` writes `add-generic-password … -w` to `security -i` **over stdin**, so the secret never appears in argv. `Get` uses `find-generic-password -wa` (`keyring_darwin.go`, v0.2.8) | Native Security.framework bindings need CGO: `keybase/go-keychain` v0.0.1 (2025-02-27) and `byteness/go-keychain` v0.1.0 both `import "C"`. `99designs/keyring` gates its keychain backend on `darwin && cgo` (`keychain.go` v1.2.2, last release 2022-12-19). Its maintained fork `byteness/keyring` v1.15.0 (2026-11-01) also uses the CGO keychain. |
| Windows | `github.com/danieljoos/wincred` v1.2.3 (2025-10-02), used by go-keyring | Credential Manager `CredWrite`/`CredRead` via syscall, no CGO | go-keyring caps the password at 2560 bytes (`keyring.go`). `CredWrite` fails with `ERROR_NO_SUCH_LOGON_SESSION`: "Network logon sessions do not have an associated credential set". https://learn.microsoft.com/en-us/windows/win32/api/wincred/nf-wincred-credwritew |
| Windows | `golang.org/x/sys/windows.CryptProtectData` / `CryptUnprotectData` (x/sys v0.49.0, 2026-10-06) | DPAPI. Encrypt the 32-byte key and store the blob as a file | "only a user with the same logon credential as the user who encrypted the data can decrypt the data… usually must be done on the same computer". `CRYPTPROTECT_LOCAL_MACHINE` means "any user on the computer" can decrypt. The prompt struct flow "will be removed in February 2027", so pass NULL. https://learn.microsoft.com/en-us/windows/win32/api/dpapi/nf-dpapi-cryptprotectdata. The `billgraziano/dpapi` wrapper (v0.5.0, 2023-10-27) is unnecessary because x/sys already exposes the calls. |
| Linux desktop | go-keyring → Secret Service over D-Bus (`github.com/godbus/dbus/v5` v5.2.4, 2026-10-13), pure Go | Needs gnome-keyring or KWallet running on a session bus | Fails headless. Example errors: "The name org.freedesktop.secrets was not provided by any .service files" (zalando/go-keyring#8), and "failed to unlock correct collection" in Docker (#45, #123). It can block on an unlock **prompt** (#118, still open). |
| Linux | Kernel keyring: `golang.org/x/sys/unix.AddKey`, `KeyctlString`, `KeyctlSearch` (pure Go). `jsipprell/keyctl` v1.0.3 is stale (2021) | Keys live in unswappable kernel memory, with possessor/user/group/other permissions | **Not a store for data at rest.** keyrings(7) describes session, process, user and persistent keyrings. The persistent keyring has an expiry and lasts beyond the UID record, but nothing in the page says keys survive reboot. User keys are "kept entirely within kernel memory". https://man7.org/linux/man-pages/man7/keyrings.7.html. It suits a short-lived CLI unlock cache, not boot-time unlock. |

- **Removes:** T1 and T2 (the secret lives in the OS store, which is encrypted at rest under the login credential). T3.
- **Does not remove:** T4. On macOS via `/usr/bin/security`, the item's ACL trusts the `security` tool, so any same-UID process that runs `security find-generic-password -w` can likely read it without a prompt **(unverified on a Mac; inferred from the security(1) `-T`/`-A` ACL semantics)**. DPAPI user-scope is decryptable by any code running as that user. T5, T6, T8.
- **UX:** good on a **laptop**. The keychain is unlocked at login, so there is no prompt. Poor or unavailable on **headless servers**: the macOS login keychain is locked before user login for launchd daemons **(unverified)**, there is no Secret Service on headless Linux, and Credential Manager is unavailable in network or service logon sessions. Unusable in Docker.

### 3.4 TPM-sealed key (direct)

- **Pure Go:** `github.com/google/go-tpm` v0.9.8 (2025-12-29; repo pushed 2026-10-30) has no `import "C"`. Transports: `tpm2/transport/linuxtpm` (`/dev/tpmrm0`), `windowstpm` (TBS), plus a simulator. It cross-compiles for windows with `CGO_ENABLED=0` (checked with `go list`). `github.com/google/go-tpm-tools` v0.4.11 (2026-10-09) provides `client.(*Key).Seal` and `Unseal` with PCR policy (`client/keys.go:279,353`). Only its `simulator/` subpackage uses CGO.
- **macOS:** there is no TPM. The Secure Enclave "works only with NIST P-256 keys" and keys "can only be used for creating and verifying cryptographic signatures, or for elliptic curve Diffie-Hellman key exchange". https://developer.apple.com/documentation/security/protecting-keys-with-the-secure-enclave. Using it means Security.framework, which means CGO and a signed binary, so it is not viable for kman.
- **Removes:** T1 and T2 (the key is bound to the chip). With a PCR policy it also blocks booting another OS to unseal.
- **Does not remove:** T5. Root on the running host can talk to the TPM unless an auth value or passphrase is added. Also T4 if the user can open `/dev/tpmrm0` (usually group `tss`), and T6 and T8.
- **UX:** PCR-bound seals break on firmware or kernel updates, which means lockout risk and the need for a recovery key. Usually absent in containers and many VMs. **On Linux, systemd-creds already wraps exactly this, so bespoke TPM code adds cost without adding coverage.**

### 3.5 Passphrase + Argon2id unlock

- **Pure Go:** `golang.org/x/crypto/argon2.IDKey` (x/crypto v0.58.0, 2026-10-09). `golang.org/x/term.ReadPassword` reads the passphrase without echo, and keman already depends on x/term.
- **Parameters:** RFC 9106 §4 gives two options. The first is "t=1 iteration, p=4 lanes, m=2^(21) (2 GiB of RAM)". The second is "t=3 iterations, p=4 lanes, m=2^(16) (64 MiB of RAM)", both with a 128-bit salt. https://www.rfc-editor.org/rfc/rfc9106.html. **Measured:** the second option costs **~56 ms** per derivation. That is a one-time startup cost with no effect on per-request latency.
- **Removes:** T1, T2, T3, and T5 *for an offline or stopped host*: nothing on disk decrypts without the passphrase.
- **Does not remove:** anything once the process is running (T4/T5 live memory, T6, T8). It also gives no protection from a keylogger.
- **UX:** **a prompt on every start**. This breaks U (unattended restart, `Restart=always`, Docker restarts, reboots). It suits a laptop or a high-assurance opt-in "sealed" mode. On servers, a passphrase delivered from a file is just a key file. Losing the passphrase is unrecoverable, which matches keman's existing "A lost master key. There is no recovery path."

### 3.6 At-rest summary

| Option | Pure Go | Linux | macOS | Windows | Headless/U | Docker | T1 | T2 | T3 | T4 | T5 |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Key file beside DB (keman default) | yes | yes | yes | yes | yes | yes | no | no | mode | no | no |
| Key file / secret mount outside data dir | yes | yes | yes | yes | yes | yes | **yes** | tmpfs only | mode | no | no |
| Env var | yes | yes | yes | yes | yes | yes | yes | yes | yes | no (environ) | no |
| systemd-creds | yes (read a file) | systemd ≥250 | - | - | yes | no | yes | yes | yes | no | no |
| macOS Keychain (go-keyring → `security`) | yes | - | yes | - | weak | no | yes | yes | yes | no (likely) | no |
| Windows DPAPI (x/sys) | yes | - | - | yes | yes (service acct w/ profile) | no | yes | yes | yes | no | no |
| Windows Cred Manager (wincred) | yes | - | - | yes | no (network logon) | no | yes | yes | yes | no | no |
| Secret Service (go-keyring/godbus) | yes | desktop only | - | - | no | no | yes | yes | yes | no | no |
| Kernel keyring | yes (x/sys) | yes | - | - | not across reboot | partial | n/a | n/a | yes | possessor | no |
| TPM seal (go-tpm/go-tpm-tools) | yes | yes | no | yes | yes | rarely | yes | yes | group | group | no |
| Passphrase + Argon2id | yes | yes | yes | yes | **no** | no | yes | yes | yes | no (running) | offline only |

**No at-rest option protects against T5 (root) or T4 (same UID) on a running host.** They differ only in what a copy or theft of the disk yields.

---

## 4. In memory

### 4.1 What the Go runtime makes impossible

- **Strings cannot be zeroed.** `net/http.Header` is `map[string][]string` (`src/net/http/header.go:24`), so every request materializes the credential as an immutable heap string. Further copies end up in bufio and TLS record buffers. keman also stores `Plaintext string` and a redactor copy.
- **The GC does not move objects, but it does not wipe freed memory either.** "Go has a non-moving GC" (https://go.dev/doc/gc-guide). Freed slots are reclaimed at sweep and zeroed only on reuse when `needzero` is set (`runtime/malloc.go:65`). Goroutine stacks are *copied* on growth (`runtime/stack.go:927 copystack`), which leaves stale copies behind.
- **The AES key schedule lives in the GC heap.** `crypto/internal/fips140/aes.blockExpanded` holds `enc [60]uint32; dec [60]uint32` (`aes.go:24-28`), and those round keys are equivalent to the DEK. So even if the DEK `[]byte` lives in a guarded buffer, the `cipher.AEAD` built from it does not.
- **`runtime/secret` (experimental).** `secret.Do(f)` erases the registers and stack used by `f`, and erases heap allocations made by `f` "as soon as the garbage collector realizes" they are unreachable. Only with `GOEXPERIMENT=runtimesecret`, "not subject to the Go 1 compatibility promise". "Currently only supported on linux/amd64 and linux/arm64. On unsupported platforms, Do will invoke f directly." It also "does not extend to any global variables written by f". (Go 1.27.3 `src/runtime/secret/secret.go`, `doc.go`.) The proposal golang/go#21865 was accepted and closed on 2025-11-26 with milestone Go1.26. Later CLs add riscv64 and loong64. https://github.com/golang/go/issues/21865. **Usefulness for kman:** it suits the one-shot unwrap at startup. It does nothing for a long-lived credential cache, and it would tie release builds to an experiment flag.

### 4.2 Guarded buffers: memguard

- `github.com/awnumar/memguard` v0.23.0 (2025-08-27; repo pushed 2026-05-08; 2.8k stars; 12 open issues). It depends on `github.com/awnumar/memcall` v0.4.0 (v0.5.0 released 2025-09-05). "written in pure Go". The README says "API is experimental and may have unstable changes." https://github.com/awnumar/memguard
- **What it does (from memcall v0.4.0 source):** on unix it calls `Madvise(MADV_DONTDUMP)`, `Mlock`, and `Setrlimit(RLIMIT_CORE, 0)` (`memcall_unix.go:15,18,91`). On Windows it uses `VirtualLock`. It adds guard pages and canaries, and keeps Enclaves encrypted with XSalsa20-Poly1305. "Core dumps are disabled by default" (`docs.go`). It does **not** set `PR_SET_DUMPABLE`.
- **Builds:** `CGO_ENABLED=0` builds succeed for linux, darwin, and windows (checked).
- **Measured cost:**

| Operation (164-byte credential) | Serial | 12 goroutines (per op) |
|---|---|---|
| Cached `cipher.AEAD.Open` (AES-256-GCM) | **180-200 ns**, 0 allocs | **12.7 ns** (scales) |
| New `aes.NewCipher` + `NewGCM` + Open per request | 2.3-3.4 µs, 3 allocs | - |
| `memguard.Enclave.Open` + `Destroy` | **75-86 µs**, 4 allocs | **75-77 µs** (does **not** scale) |
| Read from a held `LockedBuffer` | 3.9 ns | - |

  `Enclave.Open` is serialized by a package-global `keyMtx` (`core/enclave.go:10`). That gives a process-wide ceiling of about 13k opens/s, after which queueing pushes p50 up. A single open is about 3% of the 3 ms budget, but under concurrency the mutex, not the crypto, dominates.
- **Removes:** T7 (swap) and T6 for **its own pages only**.
- **Does not remove:** T8 (ptrace or root can still read the pages), T5, or hibernation. mlock(2): suspend "will save a copy of the system's RAM to disk, regardless of memory locks". https://man7.org/linux/man-pages/man2/mlock.2.html. The limit is `RLIMIT_MEMLOCK` unless the process has CAP_IPC_LOCK (64 MiB on this host). And per 4.1, the header strings and the AES schedule escape memguard anyway, so on its own it is partial coverage.

### 4.3 Decrypt-per-request vs. caching

- **Latency:** both are trivially inside the budget. Decrypting per request with a cached AEAD adds about 0.2 µs, against a 3 ms budget and keman's 71 µs measured overhead.
- **Security:** decrypting per request removes **idle** credentials from plaintext memory. But with stdlib `net/http` each request then creates a **new** unzeroable heap string of the credential. So under load there are *more* plaintext copies in memory, not fewer, and they live until the GC sweeps and the slot is reused. The real decision is whether a live memory read is in scope. For T6, T7, and T8 against a non-root attacker, process hardening (4.4) gets more out of the effort than changing when decryption happens.
- **Recommendation:** keep a single cached plaintext per credential. Store it as `[]byte` that is cleared on credential delete or rotation, not as `string`, to avoid extra copies in the model and the redactor. Keep the cached `cipher.AEAD` for any re-decrypt. Do not use memguard `Enclave.Open` on the hot path.

### 4.4 Process hardening (cheap, pure Go)

- **`prctl(PR_SET_DUMPABLE, 0)`** via `unix.Prctl` (x/sys). "Processes that are not dumpable can not be attached via ptrace(2) PTRACE_ATTACH", and the ownership of `/proc/<pid>` files changes. The flag is reset when the effective or filesystem UID or GID changes, or on execve of a setuid or file-capability binary. https://man7.org/linux/man-pages/man2/PR_SET_DUMPABLE.2const.html. Yama mode 0 lets the same UID ptrace "provided the target is dumpable", and setting non-dumpable removes that. https://www.kernel.org/doc/html/latest/admin-guide/LSM/Yama.html
  - **Measured:** with dumpable=0, `/proc/<pid>/environ` became `root root` and a same-UID read returned "Permission denied". With dumpable=1 the same read returned `SECRETX=hunter2`. So dumpable=0 also closes the env-var leak from 3.1.
- **`setrlimit(RLIMIT_CORE, 0)`** via `unix.Setrlimit` (memguard does this too). Go only produces a core when asked: "GOTRACEBACK=crash … on Unix systems, the crash raises SIGABRT to trigger a core dump" (Go 1.27.3 `runtime/extern.go:257`). External `kill -ABRT` combined with systemd-coredump or a crash pipe (this host uses `|/wsl-capture-crash`) is the realistic path, and dumpable=0 stops it.
- **systemd unit:** `LimitCORE=0` and `MemorySwapMax=0` (systemd ≥232: "absolute limit on swap usage"). Since systemd 253 `MemoryZSwapMax=` is also available. https://www.freedesktop.org/software/systemd/man/latest/systemd.resource-control.html
- **macOS / Windows:** `RLIMIT_CORE` works on darwin through x/sys. I found no pure-Go x/sys wrapper for an anti-debug equivalent of PR_SET_DUMPABLE on darwin (`PT_DENY_ATTACH`) **(unverified)**. On Windows, `GOTRACEBACK=wer` is the opt-in that "doesn't disable Windows Error Reporting", which implies the default crash path disables WER. `VirtualLock` is in x/sys.
- **Removes:** T6, and T8 by a **same-UID** non-root attacker, including the `environ` leak.
- **Does not remove:** root or CAP_SYS_PTRACE (T5), the kernel or hypervisor, or hibernation. **Cost:** blocks legitimate `gdb` and `dlv attach`, so provide an escape hatch such as `--allow-debug` or a config flag.

---

## 5. Recommendation for kman

1. **Add a `KeySource` seam** with explicit providers in this order: `env` (warn) → `file` → `systemd-creds` (`$CREDENTIALS_DIRECTORY`) → `keychain` (opt-in) → `passphrase` (opt-in).
2. **Never default the key file into the data dir.** Put it in a config dir that is separate from the DB dir, or refuse to start with a clear warning when the key sits next to the DB. This fixes T1, the threat keman's own docs call out, at zero cost.
3. **Linux server:** document `systemd-creds encrypt` + `LoadCredentialEncrypted=` as the recommended production path. It needs no code beyond reading a file, and gets TPM binding for free.
4. **Docker/K8s:** `KMAN_MASTER_KEY_FILE=/run/secrets/...`. Document that K8s etcd needs encryption at rest, and that Windows containers write secrets to disk.
5. **Laptop / desktop:** an optional keychain provider. Use `zalando/go-keyring` on macOS and Linux desktops, and **DPAPI through `golang.org/x/sys/windows` directly** on Windows. Prefer DPAPI to Credential Manager because Credential Manager fails in service and network logon sessions. Never use `CRYPTPROTECT_LOCAL_MACHINE`.
6. **Passphrase + Argon2id** (RFC 9106 second option, about 56 ms) as an opt-in sealed mode. Document that it breaks unattended restart.
7. **Skip for v1:** direct TPM code (systemd-creds covers Linux), the Secure Enclave (needs CGO), the kernel keyring as a store, and memguard on the hot path.
8. **In memory:** at startup, before reading secrets, call `PR_SET_DUMPABLE=0` and `RLIMIT_CORE=0`, with an opt-out. Clear the master key after unwrapping the DEK. Store credential plaintext as clearable `[]byte` held once. Keep the cached AEAD. Keep "memory read by root or the same UID before hardening" explicitly **out of scope** in THREAT-MODEL.md. Optionally wrap the unwrap step in `runtime/secret.Do` behind a build tag once the experiment graduates.

## 6. Open questions

- Whether the macOS `security`-tool ACL lets same-UID processes read silently needs testing on a real Mac.
- Whether macOS launchd daemons can use the login keychain needs the same.
- Whether `mlockall(MCL_CURRENT|MCL_FUTURE)` is workable with the Go runtime's virtual-memory reservations under the default `RLIMIT_MEMLOCK` was not tested.
- Whether to clear the redactor's secret copies, which would need a non-string matcher design.

## Sources

- keman (read-only clone): `SECURITY.md`, `docs/THREAT-MODEL.md`, `internal/cryptoenc/box.go`, `internal/app/app.go`, `internal/cli/cli.go`, `beta.md`, `benchmarks/RESULTS.md`
- systemd credentials: https://systemd.io/CREDENTIALS/ ; https://www.freedesktop.org/software/systemd/man/latest/systemd-creds.html ; https://www.freedesktop.org/software/systemd/man/latest/systemd.resource-control.html
- Linux: https://man7.org/linux/man-pages/man7/keyrings.7.html ; https://man7.org/linux/man-pages/man2/PR_SET_DUMPABLE.2const.html ; https://man7.org/linux/man-pages/man2/mlock.2.html ; https://www.kernel.org/doc/html/latest/admin-guide/LSM/Yama.html
- Windows: https://learn.microsoft.com/en-us/windows/win32/api/dpapi/nf-dpapi-cryptprotectdata ; https://learn.microsoft.com/en-us/windows/win32/api/wincred/nf-wincred-credwritew
- Apple: https://developer.apple.com/documentation/security/protecting-keys-with-the-secure-enclave
- Containers: https://docs.docker.com/engine/swarm/secrets/ ; https://kubernetes.io/docs/concepts/configuration/secret/
- KDF: https://www.rfc-editor.org/rfc/rfc9106.html
- Go: https://github.com/golang/go/issues/21865 ; https://github.com/golang/go/tree/go1.27.3/src/runtime/secret ; https://go.dev/doc/gc-guide ; https://github.com/golang/go/blob/go1.27.3/src/runtime/extern.go ; https://github.com/golang/go/blob/go1.27.3/src/net/http/header.go ; https://github.com/golang/go/blob/go1.27.3/src/crypto/internal/fips140/aes/aes.go
- Libraries (source read at these versions): https://github.com/awnumar/memguard (v0.23.0) ; https://github.com/awnumar/memcall (v0.4.0) ; https://github.com/zalando/go-keyring (v0.2.8; issues #8, #45, #118, #123) ; https://github.com/danieljoos/wincred (v1.2.3) ; https://github.com/99designs/keyring (v1.2.2) ; https://github.com/byteness/keyring (v1.15.0) ; https://github.com/keybase/go-keychain (v0.0.1) ; https://github.com/google/go-tpm (v0.9.8) ; https://github.com/google/go-tpm-tools (v0.4.11) ; https://pkg.go.dev/golang.org/x/sys ; https://pkg.go.dev/golang.org/x/crypto/argon2
- Release dates: `https://proxy.golang.org/<module>/@latest`, queried 2026-11-02
