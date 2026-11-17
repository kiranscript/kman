package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"path/filepath"

	"github.com/kirank55/kman/internal/store"
	"github.com/kirank55/kman/internal/vault"
)

// restoreActor is the audit actor of a restore.
const restoreActor = "system:restore"

const restoreUsage = "usage: kman restore <file> [--config-dir dir] [--allow-debug]"

// cmdRestore replaces the database with a backup, offline: the server must be
// stopped, and the master key must be the one the backup was made under. Both are
// checked before anything changes, and the database it replaces is kept.
func cmdRestore(ctx context.Context, args []string, s *sys) int {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(s.stderr)
	fs.Usage = func() {
		fmt.Fprintf(s.stderr, "%s\n\nReplaces the database with a backup made by kman backup. The server must be stopped, and the master key must be the one "+
			"the backup was made under: it holds ciphertext only. The database it replaces is kept as kman.db.before-restore. "+
			"What was written after the backup is gone.\n", restoreUsage)
		fs.PrintDefaults()
	}
	configDir := fs.String("config-dir", "", "the config directory holding kman.yaml (default ~/.config/kman, or %APPDATA%\\kman on Windows)")
	allowDebug := fs.Bool("allow-debug", false, "skip the process hardening that keeps the master key out of core dumps and debuggers")
	// Flags may follow the file, as with the online commands.
	var pos []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 1
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		pos = append(pos, rest[0])
		rest = rest[1:]
	}
	if len(pos) != 1 {
		fmt.Fprintln(s.stderr, restoreUsage)
		return 1
	}
	r := restoreRun{sys: s, configDir: *configDir, file: pos[0], opts: vault.Options{AllowDebug: *allowDebug}}
	if err := r.run(ctx); err != nil {
		fmt.Fprintln(s.stderr, "kman restore:", err)
		return 1
	}
	return 0
}

type restoreRun struct {
	*sys
	configDir, file string
	opts            vault.Options
}

func (r *restoreRun) run(ctx context.Context) error {
	if r.configDir == "" {
		dir, _, err := defaultDirs(r.goos, r.getenv)
		if err != nil {
			return err
		}
		r.configDir = dir
	}
	cfg, err := loadConfig(filepath.Join(r.configDir, configFile), r.getenv)
	if err != nil {
		return err
	}
	info, err := store.InspectBackup(ctx, r.file)
	if err != nil {
		return fmt.Errorf("%w. Nothing has changed", err)
	}
	// The master key has to open the backup, or restoring it would leave an
	// install that cannot start. Proving it takes no change.
	src, err := keySource(cfg, r.stderr, func() ([]byte, error) {
		return r.passphrase("kman: passphrase for " + cfg.MasterKeyFile + ": ")
	})
	if err != nil {
		return err
	}
	v, err := vault.Unlock(src, vault.WrappedKey{ID: info.DataKey.ID, Nonce: info.DataKey.Nonce, Wrapped: info.DataKey.Wrapped,
		Fingerprint: info.DataKey.KEKFingerprint}, r.opts)
	if err != nil {
		return fmt.Errorf("the master key from %s does not open this backup (%v). A backup opens only with the master key it was made under: "+
			"if the key was replaced with kman rekey since, find the key from before it. Nothing has changed", src, err)
	}
	v.Close()

	if err := store.Restore(ctx, cfg.DataDir, r.file); err != nil {
		if errors.Is(err, store.ErrHeld) {
			return fmt.Errorf("%w: stop kman serve, then run kman restore again. Nothing has changed", err)
		}
		return fmt.Errorf("%w. Nothing has changed", err)
	}
	if err := r.audit(ctx, cfg.DataDir, info); err != nil {
		// The restore is done and the database is right; only its record is missing.
		fmt.Fprintln(r.stderr, "kman restore: warning: the restore is not recorded in the audit log:", err)
	}
	fmt.Fprintf(r.stdout, "Restored the database from %s (schema %d).\n"+
		"The database it replaced is kept as %s: delete it when you no longer need it. What was written after the backup is gone.\n"+
		"Start kman with kman serve.\n",
		r.file, info.SchemaVersion, filepath.Join(cfg.DataDir, store.FileName+".before-restore"))
	return nil
}

// audit records the restore in the restored database, which migrates it
// forward first if the backup is from an older release.
func (r *restoreRun) audit(ctx context.Context, dataDir string, info store.BackupInfo) error {
	st, err := store.OpenExclusive(ctx, store.Options{Dir: dataDir, Now: r.now()})
	if err != nil {
		return err
	}
	defer st.Close()
	after, err := json.Marshal(map[string]any{"from": filepath.Base(r.file), "schema_version": info.SchemaVersion, "data_key": info.DataKey.ID})
	if err != nil {
		return err
	}
	return st.Do(ctx, func(tx *store.Tx) error {
		_, err := tx.AppendAudit(store.AuditEntry{TS: r.now(), Actor: restoreActor, Action: "data.restore",
			TargetKind: "store", TargetID: "install", AfterJSON: string(after)})
		return err
	})
}
