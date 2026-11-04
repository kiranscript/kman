package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/cli"
	"github.com/kirank55/kman/internal/store"
	"github.com/kirank55/kman/internal/vault"
)

// ownerToken is the admin token every install starts with.
const ownerToken = "owner"

// initActor is the audit actor of what init records.
const initActor = "system:init"

const (
	dirMode  os.FileMode = 0o700
	fileMode os.FileMode = 0o600
)

// errRefused is an init that refused and has already said why.
var errRefused = errors.New("refused")

// cmdInit sets up an install without asking anything: it writes kman.yaml
// and the master key to the config directory, creates the data directory
// and its data key, mints the owner admin token, and prints it once.
func cmdInit(args []string, s *sys) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(s.stderr)
	configDir := fs.String("config-dir", "", "the config directory (default ~/.config/kman, or %APPDATA%\\kman on Windows)")
	dataDir := fs.String("data-dir", "", "the data directory (default ~/.local/share/kman, or %LOCALAPPDATA%\\kman on Windows)")
	source := fs.String("key-source", "", "where the master key comes from: file, systemd-creds, passphrase or env (default file)")
	noSave := fs.Bool("no-save-token", false, "print the owner admin token without saving it to the credentials file")
	allowDebug := fs.Bool("allow-debug", false, "skip the process hardening that keeps the master key out of core dumps and debuggers")
	proxyListen := fs.String("proxy-listen", "", "the proxy listener, host:port (default $"+proxyListenEnv+", else "+defaultProxyListen+")")
	adminListen := fs.String("admin-listen", "", "the admin listener, host:port or unix:/path (default $"+adminListenEnv+", else "+defaultAdminListen+")")
	allowPlain := fs.Bool("allow-plain-http", false, "let the admin listener serve plain HTTP off loopback (default $"+allowPlainHTTPEnv+", else false)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(s.stderr, "usage: kman init [--config-dir dir] [--data-dir dir] [--key-source file|systemd-creds|passphrase|env] "+
			"[--proxy-listen addr] [--admin-listen addr] [--allow-plain-http] [--no-save-token]")
		return 1
	}
	in := initRun{sys: s, configDir: *configDir, dataDir: *dataDir, source: *source,
		proxyListen: *proxyListen, adminListen: *adminListen,
		save: !*noSave, opts: vault.Options{AllowDebug: *allowDebug}}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "allow-plain-http" {
			in.allowPlain = allowPlain
		}
	})
	if err := in.run(); err != nil {
		in.rollback()
		if !errors.Is(err, errRefused) {
			fmt.Fprintln(s.stderr, "kman init:", err)
		}
		return 1
	}
	return 0
}

// initRun is one run of init. It records each file and directory it
// creates, so a failed init leaves nothing behind and can be run again.
type initRun struct {
	*sys
	configDir, dataDir, source string
	proxyListen, adminListen   string // empty: from the environment, else the default
	allowPlain                 *bool  // nil: from the environment, else false
	save                       bool
	opts                       vault.Options

	created []string // in order of creation
}

func (in *initRun) run() error {
	if err := in.resolveDirs(); err != nil {
		return err
	}
	cfgPath := filepath.Join(in.configDir, configFile)
	if _, err := os.Stat(cfgPath); err == nil {
		return fmt.Errorf("%s already exists: this install is set up, so run kman serve", cfgPath)
	}
	if _, err := os.Stat(filepath.Join(in.dataDir, store.FileName)); err == nil {
		return fmt.Errorf("%s already holds a kman database: init never overwrites one", in.dataDir)
	}
	container := in.inContainer()
	cfg := Config{DataDir: in.dataDir}
	if err := in.chooseListeners(&cfg); err != nil {
		return err
	}
	if err := in.chooseKeySource(&cfg, container); err != nil {
		return err
	}
	// Refuse now what serve would refuse later, before writing anything.
	if err := cfg.check(); err != nil {
		return err
	}
	if cfg.MasterKeyFile != "" {
		if within, err := inside(in.dataDir, cfg.MasterKeyFile); err != nil {
			return err
		} else if within {
			return fmt.Errorf("%s: %w; pick a data directory apart from %s", cfg.MasterKeyFile, errKeyInDataDir, in.configDir)
		}
	}

	if err := in.mkdir(in.configDir); err != nil {
		return err
	}
	src, err := in.keyMaterial(cfg)
	if err != nil {
		return err
	}
	dekID, err := store.NewID(nil, "dek_")
	if err != nil {
		return err
	}
	v, wk, err := vault.Create(src, dekID, in.opts)
	if err != nil {
		return err
	}
	v.Close() // init seals nothing; serve unlocks the data key again
	owner, err := in.createStore(wk)
	if err != nil {
		return err
	}
	yaml, err := cfg.marshal()
	if err != nil {
		return err
	}
	if err := in.writeFile(cfgPath, yaml); err != nil {
		return err
	}
	in.created = nil // set up: nothing to roll back from here on
	in.report(cfg, owner, container)
	return nil
}

// resolveDirs fills in the default directories and makes both absolute.
func (in *initRun) resolveDirs() error {
	if in.configDir == "" || in.dataDir == "" {
		config, data, err := defaultDirs(in.goos, in.getenv)
		if err != nil {
			return err
		}
		if in.configDir == "" {
			in.configDir = config
		}
		if in.dataDir == "" {
			in.dataDir = data
		}
	}
	var err error
	if in.configDir, err = filepath.Abs(in.configDir); err != nil {
		return err
	}
	in.dataDir, err = filepath.Abs(in.dataDir)
	return err
}

// chooseListeners settles the listeners: a flag, else its environment
// variable, else loopback (invariant 8). The variables are for
// docker compose run --rm kman init, which passes no flags.
func (in *initRun) chooseListeners(cfg *Config) error {
	pick := func(flag, env, def string) string {
		if flag != "" {
			return flag
		}
		if v := in.getenv(env); v != "" {
			return v
		}
		return def
	}
	cfg.ProxyListen = pick(in.proxyListen, proxyListenEnv, defaultProxyListen)
	cfg.AdminListen = pick(in.adminListen, adminListenEnv, defaultAdminListen)
	switch v := in.getenv(allowPlainHTTPEnv); {
	case in.allowPlain != nil:
		cfg.AllowPlainHTTP = *in.allowPlain
	case v != "":
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("$%s=%q is not true or false", allowPlainHTTPEnv, v)
		}
		cfg.AllowPlainHTTP = b
	}
	return nil
}

// chooseKeySource settles the key source and the master key file. With no
// --key-source, a key from outside (KMAN_MASTER_KEY_FILE, and in a
// container KMAN_MASTER_KEY or a systemd credential) wins over making a
// new key file. In a container, init refuses to make a key, since the key
// would land on the container's filesystem or volume.
func (in *initRun) chooseKeySource(cfg *Config, container bool) error {
	keyFile := in.getenv(keyFileEnv)
	src := in.source
	if src == "" {
		switch {
		case keyFile != "":
			src = keySourceFile
		case container && in.getenv(vault.EnvVar) != "":
			src = keySourceEnv
		case container && in.getenv("CREDENTIALS_DIRECTORY") != "":
			src = keySourceSystemd
		default:
			src = keySourceFile
		}
	}
	cfg.KeySource = src
	fromOutside := false
	switch src {
	case keySourceFile:
		if keyFile != "" {
			abs, err := filepath.Abs(keyFile)
			if err != nil {
				return err
			}
			cfg.MasterKeyFile, fromOutside = abs, true
		} else {
			cfg.MasterKeyFile = filepath.Join(in.configDir, masterKeyFile)
		}
	case keySourcePassphrase:
		cfg.MasterKeyFile = filepath.Join(in.configDir, sealedKeyFile)
	case keySourceEnv:
		fromOutside = in.getenv(vault.EnvVar) != ""
	case keySourceSystemd:
		fromOutside = in.getenv("CREDENTIALS_DIRECTORY") != ""
	default:
		return fmt.Errorf("--key-source %q is not one of file, systemd-creds, passphrase, env", src)
	}
	if container && !fromOutside {
		fmt.Fprintf(in.stderr, "kman init: this looks like a container, and no master key was given (key source %s).\n\n%s", src, dockerSteps)
		return errRefused
	}
	return nil
}

// keyMaterial makes the master key when init owns it, and returns the
// source to read it back from.
func (in *initRun) keyMaterial(cfg Config) (vault.KeySource, error) {
	switch cfg.KeySource {
	case keySourceFile:
		if in.getenv(keyFileEnv) == "" {
			mk := vault.NewMasterKey()
			defer clear(mk)
			text := vault.EncodeMasterKey(mk)
			defer clear(text)
			if err := in.writeFile(cfg.MasterKeyFile, text); err != nil {
				return nil, err
			}
		}
		return vault.FileSource(cfg.MasterKeyFile), nil
	case keySourcePassphrase:
		pass, err := in.newPassphrase()
		if err != nil {
			return nil, err
		}
		defer clear(pass)
		mk := vault.NewMasterKey()
		defer clear(mk)
		sealed, err := vault.SealMasterKey(mk, pass)
		if err != nil {
			return nil, err
		}
		if err := in.writeFile(cfg.MasterKeyFile, sealed); err != nil {
			return nil, err
		}
		// Create unseals the file again with this copy, so init asks only
		// twice; the passphrase source wipes it after use.
		again := bytes.Clone(pass)
		return vault.PassphraseSource(cfg.MasterKeyFile, func() ([]byte, error) { return again, nil }), nil
	}
	return keySource(cfg, in.stderr, nil)
}

// newPassphrase asks for a passphrase twice.
func (in *initRun) newPassphrase() ([]byte, error) {
	pass, err := in.passphrase("Passphrase for the master key: ")
	if err != nil {
		return nil, fmt.Errorf("read the passphrase: %w", err)
	}
	if len(pass) == 0 {
		return nil, errors.New("the passphrase is empty")
	}
	again, err := in.passphrase("The same passphrase again: ")
	defer clear(again)
	if err != nil {
		clear(pass)
		return nil, fmt.Errorf("read the passphrase: %w", err)
	}
	if !bytes.Equal(pass, again) {
		clear(pass)
		return nil, errors.New("the two passphrases differ")
	}
	return pass, nil
}

// createStore creates the database with the data key, then mints the
// owner admin token through the admin service, as actor system:init.
func (in *initRun) createStore(wk vault.WrappedKey) (admin.CreatedAdminToken, error) {
	if _, err := os.Stat(in.dataDir); errors.Is(err, os.ErrNotExist) {
		in.created = append(in.created, in.dataDir)
	}
	db := filepath.Join(in.dataDir, store.FileName)
	in.created = append(in.created, db, db+"-wal", db+"-shm")
	now := in.now()
	ctx := context.Background()
	st, err := store.Open(ctx, store.Options{Dir: in.dataDir, Now: now})
	if err != nil {
		return admin.CreatedAdminToken{}, err
	}
	err = st.Do(ctx, func(tx *store.Tx) error {
		return tx.CreateDataKey(store.DataKey{ID: wk.ID, Wrapped: wk.Wrapped, Nonce: wk.Nonce,
			KEKFingerprint: wk.Fingerprint, CreatedAt: now})
	})
	var owner admin.CreatedAdminToken
	if err == nil {
		svc := admin.New(admin.Options{Store: st, Now: in.now})
		owner, err = svc.CreateAdminToken(admin.WithCaller(ctx, admin.Caller{Actor: initActor}),
			admin.CreateAdminTokenRequest{Name: ownerToken})
	}
	if cerr := st.Close(); err == nil {
		err = cerr
	}
	return owner, err
}

// report prints the owner token once, saves it unless told not to or in a
// container, and names the next three commands.
func (in *initRun) report(cfg Config, owner admin.CreatedAdminToken, container bool) {
	w := in.stdout
	fmt.Fprintf(w, "kman is set up.\n\n  config:      %s\n  data:        %s\n", filepath.Join(in.configDir, configFile), cfg.DataDir)
	switch cfg.KeySource {
	case keySourceFile, keySourcePassphrase:
		fmt.Fprintf(w, "  master key:  %s\n               Back it up apart from the data directory: without it the data cannot be decrypted.\n", cfg.MasterKeyFile)
	default:
		fmt.Fprintf(w, "  master key:  from the %s key source\n", cfg.KeySource)
	}
	fmt.Fprintf(w, "\nThe %s admin token, shown only this once:\n\n  %s\n\n", ownerToken, owner.Secret)
	switch {
	case container:
		fmt.Fprintln(w, "In a container kman never saves the token. Keep it somewhere safe and pass it as KMAN_ADMIN_TOKEN.")
	case !in.save:
		fmt.Fprintln(w, "Not saved (--no-save-token). Pass it as KMAN_ADMIN_TOKEN, or save it with kman login.")
	default:
		path := filepath.Join(in.configDir, cli.CredentialsFile)
		if err := cli.SaveCredentials(path, adminURL(cfg), owner.Secret); err != nil {
			fmt.Fprintf(in.stderr, "kman init: could not save the token (%v); pass it as KMAN_ADMIN_TOKEN instead.\n", err)
		} else {
			fmt.Fprintf(w, "Saved to %s, so the kman commands on this machine use it.\n", path)
		}
	}
	fmt.Fprintf(w, "\nNext:\n\n  kman serve\n  kman upstream add openai --preset openai\n  kman key create my-app --upstream openai\n")
}

// adminURL is how a client on this machine reaches the admin listener.
func adminURL(c Config) string {
	if strings.HasPrefix(c.AdminListen, unixPrefix) {
		return c.AdminListen
	}
	if c.TLS.Admin.enabled() {
		return "https://" + c.AdminListen
	}
	return "http://" + c.AdminListen
}

// mkdir creates dir 0700 if it is missing.
func (in *initRun) mkdir(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return nil
	}
	if parent := filepath.Dir(dir); parent != dir {
		if _, err := os.Stat(parent); errors.Is(err, os.ErrNotExist) {
			if err := in.mkdir(parent); err != nil {
				return err
			}
		}
	}
	if err := os.Mkdir(dir, dirMode); err != nil {
		return err
	}
	in.created = append(in.created, dir)
	return nil
}

func (in *initRun) writeFile(path string, data []byte) error {
	if err := writeNew(path, data); err != nil {
		return err
	}
	in.created = append(in.created, path)
	return nil
}

// writeNew writes data to a new 0600 file, and fails if path exists.
func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
	}
	return err
}

// rollback removes what this run created, newest first.
func (in *initRun) rollback() {
	for i := len(in.created) - 1; i >= 0; i-- {
		os.Remove(in.created[i])
	}
}
