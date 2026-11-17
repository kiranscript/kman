package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/store"
	"github.com/kirank55/kman/internal/vault"
)

// backUp copies the install's database to a new file, as kman backup does.
func (h rekeyHome) backUp(t *testing.T) string {
	t.Helper()
	s, _, _ := testSys(t, nil)
	st, err := store.Open(t.Context(), store.Options{Dir: h.data, Now: s.now()})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	dst := filepath.Join(t.TempDir(), "kman.backup")
	if err := st.Backup(t.Context(), dst); err != nil {
		t.Fatal(err)
	}
	return dst
}

// upstreams lists the names of the install's upstreams.
func (h rekeyHome) upstreams(t *testing.T) []string {
	t.Helper()
	s, _, _ := testSys(t, nil)
	st, err := store.Open(t.Context(), store.Options{Dir: h.data, Now: s.now()})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var names []string
	if err := st.Do(t.Context(), func(tx *store.Tx) error {
		us, err := tx.Upstreams()
		for _, u := range us {
			names = append(names, u.Name)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return names
}

func (h rekeyHome) addUpstream(t *testing.T, name string) {
	t.Helper()
	h.withService(t, vault.FileSource(h.keyFile()), func(ctx context.Context, svc *admin.Service) {
		if _, err := svc.AddUpstream(ctx, admin.AddUpstreamRequest{Preset: "anthropic", Name: name,
			Credential: &admin.NewCredential{Name: name + "-cred", Secret: "sk-another-0123456789"}}); err != nil {
			t.Fatal(err)
		}
	})
}

func restore(t *testing.T, h rekeyHome, args ...string) (int, string, string) {
	t.Helper()
	s, stdout, stderr := testSys(t, nil)
	code := run(t.Context(), append([]string{"restore", "--config-dir", h.config, "--allow-debug"}, args...), s)
	return code, stdout.String(), stderr.String()
}

func TestRestoreBringsBackTheDatabaseAsItWasAndTheSameMasterKeyOpensIt(t *testing.T) {
	h := newRekeyHome(t, nil, nil)
	backup := h.backUp(t)
	h.addUpstream(t, "later") // made after the backup

	code, stdout, stderr := restore(t, h, backup)
	if code != 0 {
		t.Fatalf("restore exited %d:\n%s%s", code, stdout, stderr)
	}
	if got := h.upstreams(t); len(got) != 1 || got[0] != "openai" {
		t.Errorf("after the restore the upstreams are %v, want [openai]: what came after the backup is gone", got)
	}
	h.checkOpensWith(t, vault.FileSource(h.keyFile())) // the credential still decrypts
	for _, want := range []string{store.FileName + ".before-restore", "kman serve"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("restore printed %q, which lacks %q", stdout, want)
		}
	}
	// What it replaced is kept, with the upstream made after the backup.
	aside := t.TempDir()
	b, err := os.ReadFile(filepath.Join(h.data, store.FileName+".before-restore"))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(aside, store.FileName), b, 0o600)
	if got := (rekeyHome{config: h.config, data: aside}).upstreams(t); len(got) != 2 {
		t.Errorf("the database set aside holds %v, want both upstreams", got)
	}
}

func TestRestoreIsAuditedInTheRestoredDatabase(t *testing.T) {
	h := newRekeyHome(t, nil, nil)
	backup := h.backUp(t)
	if code, so, se := restore(t, h, backup); code != 0 {
		t.Fatalf("restore exited %d:\n%s%s", code, so, se)
	}
	s, _, _ := testSys(t, nil)
	st, err := store.Open(t.Context(), store.Options{Dir: h.data, Now: s.now()})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var last store.AuditEntry
	if err := st.Do(t.Context(), func(tx *store.Tx) error {
		es, err := tx.LatestAuditEntries(1)
		if len(es) == 1 {
			last = es[0]
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if last.Actor != "system:restore" || last.Action != "data.restore" || last.TargetKind != "store" {
		t.Errorf("the newest audit entry is %+v, want system:restore's data.restore", last)
	}
	if strings.Contains(last.AfterJSON, rekeySecret) {
		t.Error("the audit entry holds a secret")
	}
}

func TestRestoreWithAnotherMasterKeyIsRefusedAndChangesNothing(t *testing.T) {
	h := newRekeyHome(t, nil, nil)
	other := newRekeyHome(t, nil, nil) // another install, with its own master key
	foreign := other.backUp(t)

	code, _, stderr := restore(t, h, foreign)
	if code != 1 || !strings.Contains(stderr, "master key") || !strings.Contains(stderr, "does not open") {
		t.Errorf("exit %d, stderr %q, want a refusal that says the master key does not open it", code, stderr)
	}
	if got := h.upstreams(t); len(got) != 1 {
		t.Errorf("a refused restore changed the database: %v", got)
	}
	if _, err := os.Stat(filepath.Join(h.data, store.FileName+".before-restore")); err == nil {
		t.Error("a refused restore set the database aside")
	}
	h.checkOpensWith(t, vault.FileSource(h.keyFile()))
}

func TestRestoreAfterARekeyNeedsTheOldMasterKeyAndSaysSo(t *testing.T) {
	h := newRekeyHome(t, nil, nil)
	old := h.backUp(t)
	s, stdout, stderr := testSys(t, nil)
	if code := run(t.Context(), []string{"rekey", "--config-dir", h.config, "--allow-debug"}, s); code != 0 {
		t.Fatalf("rekey exited %d:\n%s%s", code, stdout, stderr)
	}
	code, _, errText := restore(t, h, old)
	if code != 1 || !strings.Contains(errText, "rekey") {
		t.Errorf("exit %d, stderr %q, want a refusal that names a rekey as the likely cause", code, errText)
	}
}

func TestRestoreIsRefusedWhileTheServerRuns(t *testing.T) {
	h := newRekeyHome(t, nil, nil)
	backup := h.backUp(t)
	s, _, _ := testSys(t, nil)
	held, err := store.Open(t.Context(), store.Options{Dir: h.data, Now: s.now()})
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := held.Do(t.Context(), func(tx *store.Tx) error { _, err := tx.Meta(); return err }); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := restore(t, h, backup)
	if code != 1 || !strings.Contains(stderr, "stop kman serve") {
		t.Errorf("exit %d, stderr %q, want a refusal that says to stop kman serve", code, stderr)
	}
}

func TestRestoreRefusesAFileThatIsNotABackupAndOneThatIsMissing(t *testing.T) {
	h := newRekeyHome(t, nil, nil)
	junk := filepath.Join(t.TempDir(), "junk")
	os.WriteFile(junk, []byte(strings.Repeat("not a database ", 500)), 0o600)
	for name, file := range map[string]string{"junk": junk, "missing": filepath.Join(t.TempDir(), "nope")} {
		if code, _, stderr := restore(t, h, file); code != 1 || stderr == "" {
			t.Errorf("%s: exit %d, stderr %q, want a refusal", name, code, stderr)
		}
	}
	if got := h.upstreams(t); len(got) != 1 {
		t.Errorf("a refused restore changed the database: %v", got)
	}
}

func TestRestoreOntoAMachineThatHasTheConfigAndKeyButNoDataDirectory(t *testing.T) {
	h := newRekeyHome(t, nil, nil)
	backup := h.backUp(t)
	if err := os.RemoveAll(h.data); err != nil {
		t.Fatal(err)
	}
	if code, so, se := restore(t, h, backup); code != 0 {
		t.Fatalf("restore exited %d:\n%s%s", code, so, se)
	}
	h.checkOpensWith(t, vault.FileSource(h.keyFile()))
}

func TestRestoreTakesExactlyOneFile(t *testing.T) {
	h := newRekeyHome(t, nil, nil)
	for _, args := range [][]string{nil, {"a", "b"}} {
		if code, _, stderr := restore(t, h, args...); code != 1 || !strings.Contains(stderr, "usage: kman restore") {
			t.Errorf("restore %v: exit %d, stderr %q", args, code, stderr)
		}
	}
}
