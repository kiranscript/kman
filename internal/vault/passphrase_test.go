package vault_test

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/vault"
)

// prompter answers each prompt with the next passphrase and keeps every
// buffer it handed out, so a test can check they were wiped.
type prompter struct {
	answers []string
	issued  [][]byte
}

func (p *prompter) prompt() ([]byte, error) {
	if len(p.answers) == 0 {
		return nil, errors.New("no more answers")
	}
	b := []byte(p.answers[0])
	p.answers = p.answers[1:]
	p.issued = append(p.issued, b)
	return b, nil
}

func sealedKeyFile(t *testing.T, mk []byte, passphrase string) string {
	t.Helper()
	sealed, err := vault.SealMasterKey(mk, []byte(passphrase))
	if err != nil {
		t.Fatalf("SealMasterKey: %v", err)
	}
	return writeFile(t, sealed)
}

func TestThePassphraseSourcePromptsOnEveryReadAndUnsealsTheKey(t *testing.T) {
	mk := vault.NewMasterKey()
	path := sealedKeyFile(t, mk, "correct horse battery staple")
	p := &prompter{answers: []string{"correct horse battery staple", "correct horse battery staple"}}
	src := vault.PassphraseSource(path, p.prompt)
	for i := range 2 {
		got, err := src.MasterKey()
		if err != nil || !bytes.Equal(got, mk) {
			t.Fatalf("read %d: MasterKey = %x, %v; want the sealed key", i, got, err)
		}
	}
	if len(p.issued) != 2 {
		t.Fatalf("prompted %d times in two reads, want 2", len(p.issued))
	}
	for i, b := range p.issued {
		if !allZero(b) {
			t.Errorf("passphrase %d was not wiped", i)
		}
	}
}

func TestTheSealedFileHoldsNeitherTheKeyNorThePassphrase(t *testing.T) {
	mk := vault.NewMasterKey()
	sealed, err := os.ReadFile(sealedKeyFile(t, mk, "correct horse battery staple"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range [][]byte{mk, bytes.TrimSpace(vault.EncodeMasterKey(mk)), []byte("correct horse")} {
		if bytes.Contains(sealed, secret) {
			t.Fatalf("the sealed file holds %q", secret)
		}
	}
	if !bytes.Contains(sealed, []byte("argon2id")) {
		t.Errorf("the sealed file does not name its KDF:\n%s", sealed)
	}
}

func TestAWrongPassphraseIsRefused(t *testing.T) {
	path := sealedKeyFile(t, vault.NewMasterKey(), "correct horse battery staple")
	p := &prompter{answers: []string{"Tr0ub4dor&3"}}
	_, err := vault.PassphraseSource(path, p.prompt).MasterKey()
	if !errors.Is(err, vault.ErrWrongPassphrase) {
		t.Fatalf("MasterKey with a wrong passphrase = %v, want ErrWrongPassphrase", err)
	}
}

func TestThePassphraseSourceRefusesBadInput(t *testing.T) {
	if _, err := vault.SealMasterKey(vault.NewMasterKey(), nil); err == nil {
		t.Error("SealMasterKey accepted an empty passphrase")
	}
	if _, err := vault.SealMasterKey([]byte("short"), []byte("pass")); err == nil {
		t.Error("SealMasterKey accepted a key of the wrong size")
	}

	path := sealedKeyFile(t, vault.NewMasterKey(), "pass")
	if _, err := vault.PassphraseSource(path, (&prompter{answers: []string{""}}).prompt).MasterKey(); err == nil {
		t.Error("an empty passphrase was accepted")
	}
	if _, err := vault.PassphraseSource(path, (&prompter{}).prompt).MasterKey(); err == nil || !strings.Contains(err.Error(), "no more answers") {
		t.Errorf("a failed prompt: err = %v, want the prompt's error", err)
	}
	for name, content := range map[string]string{
		"a plain key file": string(vault.EncodeMasterKey(vault.NewMasterKey())),
		"not json":         "{",
		"huge memory":      `{"format":"kman-sealed-master-key","version":1,"kdf":"argon2id","time":1,"memory_kib":1073741824,"threads":1,"salt":"AAAAAAAAAAAAAAAAAAAAAA==","nonce":"AAAAAAAAAAAAAAAA","ciphertext":"AA=="}`,
		"other version":    `{"format":"kman-sealed-master-key","version":2}`,
	} {
		p := &prompter{answers: []string{"pass"}}
		if _, err := vault.PassphraseSource(writeFile(t, []byte(content)), p.prompt).MasterKey(); err == nil {
			t.Errorf("%s: MasterKey succeeded, want an error", name)
		}
		if len(p.issued) != 0 {
			t.Errorf("%s: prompted for a passphrase before checking the file", name)
		}
	}
}
