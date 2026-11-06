package e2e_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRekeyKeepsCredentialsAndRefusesARunningServer(t *testing.T) {
	h := installed(t)
	s := h.serve(t)
	h.login(t, s)
	h.addOpenAI(t)
	before := h.showUpstream(t, "openai")

	stdout, stderr, code := h.run(t, "rekey")
	if code != 1 || !strings.Contains(stderr, "stop kman serve") {
		t.Fatalf("kman rekey beside a running server exited %d:\n%s%s", code, stdout, stderr)
	}
	// The refusal cost the server nothing: it still answers and still writes.
	if got := h.showUpstream(t, "openai"); got.Host != before.Host {
		t.Errorf("after the refusal, upstream show = %+v", got)
	}

	stopServer(t, s)
	oldKey, err := os.ReadFile(filepath.Join(h.configDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	h.mustRun(t, "rekey")
	newKey, err := os.ReadFile(filepath.Join(h.configDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	if string(oldKey) == string(newKey) {
		t.Error("rekey left the master key as it was")
	}

	// serve puts every credential in memory at startup, and refuses to start
	// with one it cannot decrypt.
	restarted := h.serve(t)
	h.login(t, restarted)
	if got := h.showUpstream(t, "openai"); len(got.Credentials) != 1 || got.Credentials[0].ID != before.Credentials[0].ID || got.Credentials[0].Last4 != "WXYZ" {
		t.Errorf("after the rekey, upstream show = %+v", got)
	}
	stopServer(t, restarted)

	// The old key no longer starts it.
	if err := os.WriteFile(filepath.Join(h.configDir(), "master.key"), oldKey, 0o600); err != nil {
		t.Fatal(err)
	}
	h.refused(t, "does not unwrap")
}

func TestRekeyRunsAfterAServerWasKilled(t *testing.T) {
	h := installed(t)
	s := h.serve(t)
	h.login(t, s)
	h.addOpenAI(t)
	if err := s.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-s.exited

	// The operating system dropped its locks, and what is in its write-ahead
	// log comes along.
	h.mustRun(t, "rekey")
	restarted := h.serve(t)
	h.login(t, restarted)
	if got := h.showUpstream(t, "openai"); len(got.Credentials) != 1 {
		t.Errorf("after the rekey, upstream show = %+v", got)
	}
}

func TestServeRefusesWhileAnotherProcessHoldsTheDatabaseExclusively(t *testing.T) {
	h := installed(t)
	// What kman rekey holds, from another process: serve must not start under it.
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(h.dataDir(), "kman.db"))+"?_pragma=locking_mode(EXCLUSIVE)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	h.refused(t, "another process holds the database")
}
