package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/kirank55/kman/internal/store"
	"github.com/kirank55/kman/internal/vault"
)

// rekeyActor is the audit actor of a rekey.
const rekeyActor = "system:rekey"

// pendingSuffix names a file staged beside the one it will replace once the
// database has committed.
const pendingSuffix = ".new"

const rekeyUsage = "usage: kman rekey [--config-dir dir] [--new-key-source file|systemd-creds|passphrase|env] [--new-key-file path] [--allow-debug]"

// cmdRekey replaces the master key and the data key, offline: a new data key,
// wrapped under a new master key, with every secret resealed under it in one
// transaction (ADR 0003). The order is what makes a crash safe. The new key
// is on disk before the database commits, and replaces the old one only after.
func cmdRekey(ctx context.Context, args []string, s *sys) int {
	fs := flag.NewFlagSet("rekey", flag.ContinueOnError)
	fs.SetOutput(s.stderr)
	configDir := fs.String("config-dir", "", "the config directory holding kman.yaml (default ~/.config/kman, or %APPDATA%\\kman on Windows)")
	newSource := fs.String("new-key-source", "", "where the new master key will come from: file, systemd-creds, passphrase or env (default the current source)")
	newFile := fs.String("new-key-file", "", "a new master key made by kman keygen. Without it, kman makes one for the file and passphrase sources; the others need it")
	allowDebug := fs.Bool("allow-debug", false, "skip the process hardening that keeps the master key out of core dumps and debuggers")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(s.stderr, rekeyUsage)
		return 1
	}
	r := rekeyRun{sys: s, configDir: *configDir, newSource: *newSource, newFile: *newFile, opts: vault.Options{AllowDebug: *allowDebug}}
	if err := r.run(ctx); err != nil {
		fmt.Fprintln(s.stderr, "kman rekey:", err)
		return 1
	}
	return 0
}

// rekeyRun is one run of rekey.
type rekeyRun struct {
	*sys
	configDir, newSource, newFile string
	opts                          vault.Options

	cfg  Config
	plan rekeyPlan
	// staged are the pending files this run wrote, for removing on failure.
	// A pending file it did not write may be all that is left of a rekey that
	// crashed after committing, so it is never touched unless the old key has
	// proved that rekey did not happen.
	staged []string
}

// rekeyPlan is where the new master key comes from and what changes on disk.
type rekeyPlan struct {
	to       vault.KeySource
	source   string // key_source after the rekey
	keyFile  string // master_key_file after the rekey, "" for none
	envFile  bool   // $KMAN_MASTER_KEY_FILE names the key, whatever kman.yaml says
	replaced string // the file a new key is staged to replace, "" for none
}

func (r *rekeyRun) run(ctx context.Context) error {
	if r.configDir == "" {
		dir, _, err := defaultDirs(r.goos, r.getenv)
		if err != nil {
			return err
		}
		r.configDir = dir
	}
	cfgPath := filepath.Join(r.configDir, configFile)
	cfg, err := loadConfig(cfgPath, r.getenv)
	if err != nil {
		return err
	}
	r.cfg = cfg
	if err := r.choose(); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(cfg.DataDir, store.FileName)); err != nil {
		return fmt.Errorf("%s holds no kman database (%v): there is nothing to rekey", cfg.DataDir, err)
	}
	// Before any prompt: a running server makes all of it pointless.
	st, err := store.OpenExclusive(ctx, store.Options{Dir: cfg.DataDir, Now: r.now()})
	if errors.Is(err, store.ErrHeld) {
		return fmt.Errorf("%w: stop kman serve, then run kman rekey again. Nothing has changed", err)
	}
	if err != nil {
		return err
	}
	defer st.Close()

	secrets, err := r.rekey(ctx, st)
	if err != nil {
		for _, p := range r.staged {
			os.Remove(p)
		}
		return fmt.Errorf("%w. Nothing has changed", err)
	}
	// Close checkpoints, which takes the old sealed bytes out of the
	// write-ahead log. The commit was already durable, so a failure here costs
	// nothing that the next opener does not put right.
	if err := st.Close(); err != nil {
		fmt.Fprintln(r.stderr, "kman rekey: warning: closing the database:", err)
	}
	if err := r.install(); err != nil {
		return err
	}
	r.report(secrets)
	return nil
}

// rekey reads what is sealed, reseals it under a new key, staging the files
// the new key needs, and commits, returning how many secrets it carried over.
// It returns an error only if the database did not change.
func (r *rekeyRun) rekey(ctx context.Context, st *store.Store) (int, error) {
	var old store.DataKey
	var secrets []store.SealedSecret
	if err := st.Do(ctx, func(tx *store.Tx) error {
		var err error
		if old, err = tx.CurrentDataKey(); err != nil {
			return err
		}
		secrets, err = tx.SealedSecrets()
		return err
	}); err != nil {
		return 0, err
	}
	from, err := keySource(r.cfg, r.stderr, func() ([]byte, error) {
		return r.passphrase("kman: passphrase for " + r.cfg.MasterKeyFile + ": ")
	})
	if err != nil {
		return 0, err
	}
	newID, err := store.NewID(nil, "dek_")
	if err != nil {
		return 0, err
	}
	stored := make([]vault.Stored, len(secrets))
	for i, s := range secrets {
		stored[i] = vault.Stored{ID: s.ID, Sealed: vault.Sealed{DataKeyID: s.DataKeyID, Nonce: s.Nonce, Ciphertext: s.Ciphertext}}
	}
	wk, resealed, err := vault.Rekey(from, r.plan.to, vault.WrappedKey{ID: old.ID, Nonce: old.Nonce, Wrapped: old.Wrapped, Fingerprint: old.KEKFingerprint}, newID, stored, r.opts)
	if err != nil {
		return 0, r.explain(err)
	}
	if err := r.stageConfig(); err != nil {
		return 0, err
	}
	for i := range secrets {
		secrets[i].Ciphertext, secrets[i].Nonce, secrets[i].DataKeyID = resealed[i].Ciphertext, resealed[i].Nonce, wk.ID
	}
	now := r.now()
	err = st.Do(ctx, func(tx *store.Tx) error {
		next := store.DataKey{ID: wk.ID, Wrapped: wk.Wrapped, Nonce: wk.Nonce, KEKFingerprint: wk.Fingerprint, CreatedAt: now}
		if err := tx.ReplaceDataKey(old.ID, next, secrets, now); err != nil {
			return err
		}
		return r.audit(tx, old, next, len(secrets))
	})
	if err != nil {
		return 0, err
	}
	return len(secrets), nil
}

// audit records the rekey in its transaction. Fingerprints name master keys
// without revealing them.
func (r *rekeyRun) audit(tx *store.Tx, old, next store.DataKey, secrets int) error {
	before, err := json.Marshal(map[string]any{"data_key": old.ID, "kek_fingerprint": old.KEKFingerprint, "key_source": r.cfg.KeySource})
	if err != nil {
		return err
	}
	after, err := json.Marshal(map[string]any{"data_key": next.ID, "kek_fingerprint": next.KEKFingerprint, "key_source": r.plan.source, "secrets": secrets})
	if err != nil {
		return err
	}
	_, err = tx.AppendAudit(store.AuditEntry{TS: next.CreatedAt, Actor: rekeyActor, Action: "data_key.rekey",
		TargetKind: "data_key", TargetID: next.ID, BeforeJSON: string(before), AfterJSON: string(after)})
	return err
}

// choose settles where the new master key comes from. Like init, it makes a key
// only where one can be kept: never in a container, where it would land on the
// container's filesystem or volume, and never over a key from outside.
func (r *rekeyRun) choose() error {
	cfg := r.cfg
	source := r.newSource
	if source == "" {
		source = cfg.KeySource
	}
	var given string
	if r.newFile != "" {
		abs, err := filepath.Abs(r.newFile)
		if err != nil {
			return err
		}
		given = abs
	}
	p := rekeyPlan{source: source, envFile: source == keySourceFile && r.getenv(keyFileEnv) != ""}
	makeOne := func(defaultName string) error {
		if r.inContainer() {
			return errors.New("this looks like a container, where kman will not make a master key: run kman keygen on the host, mount the key, and pass --new-key-file")
		}
		if p.envFile {
			return fmt.Errorf("$%s names a key from outside, which kman will not replace: run kman keygen and pass --new-key-file", keyFileEnv)
		}
		p.keyFile = filepath.Join(r.configDir, defaultName)
		if cfg.KeySource == source {
			p.keyFile = cfg.MasterKeyFile
		}
		p.replaced = p.keyFile
		return nil
	}
	switch source {
	case keySourceFile:
		if given != "" {
			p.to, p.keyFile = vault.FileSource(given), given
			break
		}
		if err := makeOne(masterKeyFile); err != nil {
			return err
		}
		p.to = &madeKey{run: r, path: p.keyFile + pendingSuffix, render: func(mk []byte) ([]byte, error) { return vault.EncodeMasterKey(mk), nil }}
	case keySourcePassphrase:
		if given != "" {
			return errors.New("--new-key-file holds a plain key, and the passphrase source seals its own: leave it out")
		}
		if err := makeOne(sealedKeyFile); err != nil {
			return err
		}
		p.to = &madeKey{run: r, path: p.keyFile + pendingSuffix, render: func(mk []byte) ([]byte, error) {
			pass, err := r.newPassphrase()
			if err != nil {
				return nil, err
			}
			defer clear(pass)
			return vault.SealMasterKey(mk, pass)
		}}
	case keySourceEnv, keySourceSystemd:
		if given == "" {
			return fmt.Errorf("the %s key source holds no file kman can write: run kman keygen and pass --new-key-file", source)
		}
		p.to = vault.FileSource(given)
	default:
		return fmt.Errorf("--new-key-source %q is not one of file, systemd-creds, passphrase, env", source)
	}
	for _, path := range []string{given, p.keyFile} {
		if path == "" {
			continue
		}
		within, err := inside(cfg.DataDir, path)
		if err != nil {
			return err
		}
		if within && !cfg.AllowKeyInDataDir {
			return fmt.Errorf("%s: %w. Pick a path outside it with --new-key-file", path, errKeyInDataDir)
		}
	}
	r.plan = p
	return nil
}

// madeKey is the source of a master key kman makes itself. It makes the key
// when it is read, which vault.Rekey does only once the old key has proved
// itself, and stages the file that will hold it before returning it.
type madeKey struct {
	run    *rekeyRun
	path   string
	render func(mk []byte) ([]byte, error)
}

func (k *madeKey) String() string { return "new key file " + k.path }

func (k *madeKey) MasterKey() ([]byte, error) {
	mk := vault.NewMasterKey()
	content, err := k.render(mk)
	if err == nil {
		defer clear(content)
		err = k.run.stage(k.path, content)
	}
	if err != nil {
		clear(mk)
		return nil, err
	}
	return mk, nil
}

// stage writes a pending file durably, replacing a stale one. By now the old
// key has unwrapped the data key, so a pending file already there is from a
// rekey that did not commit.
func (r *rekeyRun) stage(path string, content []byte) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := writeNew(path, content); err != nil {
		return err
	}
	syncDir(filepath.Dir(path))
	r.staged = append(r.staged, path)
	return nil
}

// stageConfig stages kman.yaml if the rekey changes what it says. While
// $KMAN_MASTER_KEY_FILE names the key file, the file kman.yaml names is not the
// one in use, so a change of file alone is left to the operator's environment.
func (r *rekeyRun) stageConfig() error {
	p := r.plan
	if p.source == r.cfg.KeySource && (p.keyFile == r.cfg.MasterKeyFile || p.envFile) {
		return nil
	}
	path := filepath.Join(r.configDir, configFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	edited, err := editConfig(raw, p.source, p.keyFile)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return r.stage(path+pendingSuffix, edited)
}

// editConfig sets key_source and master_key_file in the text of kman.yaml,
// keeping the rest of it, comments included. An empty keyFile removes
// master_key_file.
func editConfig(raw []byte, source, keyFile string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("it is not a mapping")
	}
	root := doc.Content[0]
	setKey(root, "key_source", source)
	setKey(root, "master_key_file", keyFile)
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// setKey sets a string key of a mapping, or removes it when value is empty.
func setKey(m *yaml.Node, key, value string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value != key {
			continue
		}
		if value == "" {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
		} else {
			m.Content[i+1].Value, m.Content[i+1].Tag, m.Content[i+1].Style = value, "!!str", 0
		}
		return
	}
	if value != "" {
		m.Content = append(m.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
	}
}

// install moves the staged files over the ones they replace, after the commit.
func (r *rekeyRun) install() error {
	for i, p := range r.staged {
		final := strings.TrimSuffix(p, pendingSuffix)
		if err := os.Rename(p, final); err != nil {
			return fmt.Errorf("the database is rekeyed, but installing the new files failed (%w). Before starting kman, %s", err, moves(r.staged[i:]))
		}
		syncDir(filepath.Dir(final))
	}
	return nil
}

// moves says how to finish installing pending files by hand.
func moves(pending []string) string {
	var says []string
	for _, p := range pending {
		says = append(says, "move "+p+" over "+strings.TrimSuffix(p, pendingSuffix))
	}
	return strings.Join(says, ", then ")
}

// explain adds what the operator can do about an error from the vault.
func (r *rekeyRun) explain(err error) error {
	switch {
	case errors.Is(err, vault.ErrSameMasterKey):
		return fmt.Errorf("%w: make a new one with kman keygen", err)
	case errors.Is(err, vault.ErrWrongMasterKey):
		// A crash between the commit and the install leaves exactly this: the
		// database under the new key, the old key still in place.
		var pending []string
		candidates := []string{filepath.Join(r.configDir, configFile) + pendingSuffix}
		if r.plan.replaced != "" {
			candidates = append([]string{r.plan.replaced + pendingSuffix}, candidates...)
		}
		for _, p := range candidates {
			if _, serr := os.Stat(p); serr == nil {
				pending = append(pending, p)
			}
		}
		if len(pending) > 0 {
			return fmt.Errorf("%w. An earlier kman rekey left new files behind: if it stopped after committing, %s", err, moves(pending))
		}
	}
	return err
}

// report says what happened and what is left for the operator.
func (r *rekeyRun) report(secrets int) {
	w := r.stdout
	p, old := r.plan, r.cfg
	fmt.Fprintf(w, "kman is rekeyed: a new data key under a new master key, with %d sealed secrets carried over.\n\n", secrets)
	switch {
	case p.source == keySourceEnv:
		fmt.Fprintf(w, "Before starting kman, set $%s to the contents of %s, then delete that file.\n", vault.EnvVar, r.newFile)
	case p.source == keySourceSystemd:
		fmt.Fprintf(w, "Before starting kman, run systemd-creds encrypt --name=%s %s and load the result with LoadCredentialEncrypted=, then delete that file.\n", vault.SystemdCredName, r.newFile)
	case p.envFile:
		fmt.Fprintf(w, "Before starting kman, point $%s at %s.\n", keyFileEnv, p.keyFile)
	default:
		fmt.Fprintf(w, "  master key:  %s\n               Back it up apart from the data directory: without it the data cannot be decrypted.\n", p.keyFile)
	}
	if old.MasterKeyFile != "" && old.MasterKeyFile != p.keyFile {
		fmt.Fprintf(w, "\n%s still holds the old master key, which opens nothing in this database any more. Delete it, unless the copies below need it.\n", old.MasterKeyFile)
	}
	fmt.Fprintln(w, "\nAnything copied before now still opens with the old master key, and only with it: every copy of the database you made yourself, and:")
	copies, _ := filepath.Glob(filepath.Join(old.DataDir, store.FileName+".pre-*"))
	for _, c := range copies {
		fmt.Fprintln(w, "  "+c)
	}
	if len(copies) == 0 {
		fmt.Fprintln(w, "  (no pre-migration copies in the data directory)")
	}
	fmt.Fprintln(w, "Keep the old key as long as you keep those. If it leaked, destroy them both.")
}

// syncDir makes a rename or a new file in dir durable, where the OS can.
func syncDir(dir string) {
	f, err := os.Open(dir)
	if err != nil {
		return
	}
	defer f.Close()
	f.Sync() // Windows refuses this; NTFS journals the directory anyway
}
