package releasecheck_test

import (
	"slices"
	"strings"
	"testing"
)

const composeFile = "examples/compose/compose.yaml"

type compose struct {
	Services map[string]struct {
		Image       string
		Command     []string
		ReadOnly    bool `yaml:"read_only"`
		Environment map[string]string
		Secrets     []struct{ Source, Target string }
		Volumes     []string
		Tmpfs       []string
		Ports       []string
		CapDrop     []string `yaml:"cap_drop"`
		SecurityOpt []string `yaml:"security_opt"`
	}
	Secrets map[string]struct{ File string }
	Volumes map[string]any
}

func loadCompose(t *testing.T) compose {
	t.Helper()
	var c compose
	decode(t, composeFile, &c)
	if _, ok := c.Services["kman"]; !ok || len(c.Services) != 1 {
		t.Fatalf("services = %v, want one named kman (docker compose run --rm kman init)", c.Services)
	}
	return c
}

// SPEC §3: the key is a secret, mounted where KMAN_MASTER_KEY_FILE points,
// from the master.key that kman keygen wrote next to compose.yaml.
func TestComposeMountsTheKeyAsASecret(t *testing.T) {
	c := loadCompose(t)
	k := c.Services["kman"]
	if len(k.Secrets) != 1 || k.Secrets[0].Source == "" {
		t.Fatalf("kman secrets = %+v, want the master key", k.Secrets)
	}
	s := k.Secrets[0]
	mount := "/run/secrets/" + s.Source
	if s.Target != "" {
		mount = s.Target
	}
	if got := k.Environment["KMAN_MASTER_KEY_FILE"]; got != mount {
		t.Errorf("KMAN_MASTER_KEY_FILE = %q, want the secret's mount %s", got, mount)
	}
	if got := c.Secrets[s.Source].File; got != "./master.key" {
		t.Errorf("secret %s file = %q, want ./master.key (docker run ... keygen > master.key)", s.Source, got)
	}
}

// SPEC §3: the data directory is on a volume and the root filesystem is
// read-only. The image is distroless, so a named volume starts owned by
// UID 65532 only when it is mounted on a directory the image already has:
// the nonroot home, which holds kman's default config and data directories.
func TestComposeKeepsTheDataOnAVolumeAndTheRootReadOnly(t *testing.T) {
	c := loadCompose(t)
	k := c.Services["kman"]
	if !k.ReadOnly {
		t.Error("read_only must be true")
	}
	var named string
	for _, v := range k.Volumes {
		src, dst, _ := strings.Cut(v, ":")
		if dst == "/home/nonroot" {
			named = src
		}
	}
	if _, ok := c.Volumes[named]; named == "" || !ok {
		t.Errorf("volumes = %v, want a named volume on /home/nonroot", k.Volumes)
	}
	if !slices.Contains(k.Tmpfs, "/tmp") {
		t.Errorf("tmpfs = %v, want /tmp (SQLite's temporary files) on a read-only root", k.Tmpfs)
	}
	if !slices.Equal(k.CapDrop, []string{"ALL"}) || !slices.Contains(k.SecurityOpt, "no-new-privileges:true") {
		t.Errorf("cap_drop %v, security_opt %v; want ALL and no-new-privileges:true", k.CapDrop, k.SecurityOpt)
	}
	if !strings.HasPrefix(k.Image, "ghcr.io/kirank55/kman") {
		t.Errorf("image = %q, want ghcr.io/kirank55/kman", k.Image)
	}
	if !slices.Equal(k.Command, []string{"serve"}) {
		t.Errorf("command = %v, want [serve]", k.Command)
	}
}

// SPEC §3 and invariant 8: admin is published on host 127.0.0.1:8081 only,
// and so is the proxy. Inside the container kman listens on every interface,
// so the port mappings are what keeps it off the network, and init is told
// so explicitly.
func TestComposePublishesOnHostLoopbackOnly(t *testing.T) {
	k := loadCompose(t).Services["kman"]
	if !slices.Contains(k.Ports, "127.0.0.1:8081:8081") || !slices.Contains(k.Ports, "127.0.0.1:8080:8080") {
		t.Errorf("ports = %v, want 127.0.0.1:8080:8080 and 127.0.0.1:8081:8081", k.Ports)
	}
	for _, p := range k.Ports {
		if !strings.HasPrefix(p, "127.0.0.1:") {
			t.Errorf("port %q is published beyond host loopback", p)
		}
	}
	for key, want := range map[string]string{
		"KMAN_PROXY_LISTEN": "0.0.0.0:8080", "KMAN_ADMIN_LISTEN": "0.0.0.0:8081", "KMAN_ALLOW_PLAIN_HTTP": "true",
	} {
		if got := k.Environment[key]; got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}
