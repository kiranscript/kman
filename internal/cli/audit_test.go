package cli_test

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/cli"
)

func TestAuditListsEntriesWithTheirActor(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.kman(t, "admin-token", "create", "ci")
	out := in.kman(t, "audit")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("audit printed:\n%s\nwant a header and two entries", out)
	}
	for i, want := range [][]string{{"system:init", "admin_token.create", "admin_token/owner"}, {"owner", "admin_token.create", "admin_token/ci"}} {
		f := strings.Fields(lines[i+1])
		for _, w := range want {
			if !contains(f, w) {
				t.Errorf("line %q lacks %q", lines[i+1], w)
			}
		}
	}
	var page admin.AuditPage
	in.kmanJSON(t, &page, "audit")
	if len(page.Entries) != 2 || page.Entries[1].Actor != "owner" {
		t.Errorf("audit --json = %+v", page)
	}
	in.kmanJSON(t, &page, "audit", "--limit", "1")
	if len(page.Entries) != 1 || page.Entries[0].TargetID != "ci" || !page.More {
		t.Errorf("audit --limit 1 --json = %+v, want the newest and more", page)
	}
}

func TestAuditExportWritesEveryEntryAsJSONLines(t *testing.T) {
	defer cli.SetExportPage(2)()
	in := newInstall(t).loggedIn(t)
	for _, name := range []string{"a", "b", "c", "d"} {
		in.kman(t, "admin-token", "create", name)
	}
	for _, args := range [][]string{{"audit", "export"}, {"audit", "export", "--json"}} {
		out := in.kman(t, args...)
		var targets []string
		sc := bufio.NewScanner(strings.NewReader(out))
		for sc.Scan() {
			var e admin.AuditEntry
			if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
				t.Fatalf("line %q: %v", sc.Text(), err)
			}
			if e.Actor == "" {
				t.Errorf("entry %d has no actor", e.ID)
			}
			targets = append(targets, e.TargetID)
		}
		if strings.Join(targets, " ") != "owner a b c d" {
			t.Errorf("%v exported %v, want owner a b c d", args, targets)
		}
	}
	out := in.kman(t, "audit", "export", "--after", "3")
	if n := strings.Count(out, "\n"); n != 2 {
		t.Errorf("export --after 3 wrote %d entries, want 2", n)
	}
}
