package cli

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kirank55/kman/internal/admin"
)

func TestDashboardNavigationAndDetails(t *testing.T) {
	m := dashboard{width: 100, height: 20, keys: []admin.Key{{ID: "key-1", Name: "app", Upstream: "openai", Status: admin.KeyActive, ExpiresAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}}}
	if !strings.Contains(m.View(), "app") {
		t.Fatal("key missing from dashboard")
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(dashboard)
	if !strings.Contains(m.View(), "key-1") {
		t.Fatal("key detail missing")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(dashboard)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	m = next.(dashboard)
	if m.tab != 1 || !m.loading || cmd == nil {
		t.Fatal("switching views must load exposure")
	}
}
func TestDashboardIgnoresStaleResultsAndCleansTerminalControls(t *testing.T) {
	m := dashboard{tab: 1, generation: 2, loading: true}
	next, _ := m.Update(dashboardResult{tab: 0, generation: 1, keys: []admin.Key{{Name: "stale"}}})
	m = next.(dashboard)
	if !m.loading || len(m.keys) != 0 {
		t.Fatal("stale result changed current view")
	}
	next, _ = m.Update(dashboardResult{tab: 1, generation: 2, findings: []admin.ExposureFinding{{Message: "bad\x1b[2J"}}})
	m = next.(dashboard)
	if strings.Contains(m.View(), "\x1b[2J") {
		t.Fatal("server data contains terminal escape")
	}
}

func TestDashboardRefreshCannotOpenDisappearingDetail(t *testing.T) {
	m := dashboard{tab: 1, findings: []admin.ExposureFinding{{Message: "old"}}}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = next.(dashboard)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(dashboard)
	if m.detail {
		t.Fatal("opened cached detail while refreshing")
	}
	next, _ = m.Update(dashboardResult{tab: 1, generation: m.generation})
	m = next.(dashboard)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(dashboard)
	if m.detail || m.count() != 0 {
		t.Fatal("empty result should remain in list view")
	}
}

// settle runs cmd and feeds what it answers back to the model, as the program does.
func settle(m dashboard, cmd tea.Cmd) dashboard {
	if cmd == nil {
		return m
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			m = settle(m, c)
		}
	case nil:
	default:
		next, c := m.Update(msg)
		m = settle(next.(dashboard), c)
	}
	return m
}

func press(t *testing.T, m dashboard, keys ...string) dashboard {
	t.Helper()
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "left":
			msg = tea.KeyMsg{Type: tea.KeyLeft}
		case "right":
			msg = tea.KeyMsg{Type: tea.KeyRight}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "shift+tab":
			msg = tea.KeyMsg{Type: tea.KeyShiftTab}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		next, cmd := m.Update(msg)
		m = settle(next.(dashboard), cmd)
	}
	return m
}

func resize(m dashboard, w, h int) dashboard {
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return next.(dashboard)
}

// demoModel is the keys view of the demo data, loaded.
func demoModel(t *testing.T, w, h int) dashboard {
	t.Helper()
	m := dashboard{ctx: t.Context(), demo: newDemoData(time.Now()), width: w, height: h, loading: true}
	return resize(settle(m, m.loadView()), w, h)
}

func TestDashboardDemoNeedsNoServer(t *testing.T) {
	m := demoModel(t, 100, 24)
	for tab := range 4 {
		m = press(t, m, fmt.Sprint(tab+1))
		if m.err != nil || m.count() == 0 || m.loading {
			t.Fatalf("tab %d: err %v, %d rows, loading %v", tab, m.err, m.count(), m.loading)
		}
		if !strings.Contains(m.View(), "demo data") {
			t.Fatalf("tab %d: view does not say it is demo data", tab)
		}
	}
}

func TestDashboardViewsSwitchFromADetail(t *testing.T) {
	for _, k := range []string{"1", "2", "3", "4", "tab", "shift+tab", "left", "right"} {
		for tab := range 4 {
			m := press(t, demoModel(t, 100, 24), fmt.Sprint(tab+1), "enter")
			if !m.detail && m.tab != 1 && !(m.tab == 2) {
				t.Fatalf("tab %d: enter did not open a detail", tab)
			}
			m = press(t, m, k)
			if m.detail || m.loading || m.err != nil {
				t.Fatalf("tab %d, key %q: detail %v loading %v err %v", tab, k, m.detail, m.loading, m.err)
			}
			_ = m.View() // a stale cursor in the new view would panic here
		}
	}
	m := press(t, demoModel(t, 100, 24), "enter", "3")
	if m.tab != 2 || m.detail || !strings.Contains(m.View(), "ACTOR") && !strings.Contains(m.View(), "actor") {
		t.Fatalf("2 from a detail did not reach the audit view:\n%s", m.View())
	}
}

func TestDashboardRefreshKeepsTheOpenItem(t *testing.T) {
	m := press(t, demoModel(t, 100, 24), "down", "enter")
	id := m.openID
	if id == "" || !m.detail {
		t.Fatal("no detail open")
	}
	// The list gains an item ahead of it, so the cursor's index is stale.
	m.demo.keys = append([]admin.Key{{ID: "new", Name: "new", Status: admin.KeyActive}}, m.demo.keys...)
	m = press(t, m, "r")
	if !m.detail || m.keys[m.cursor].ID != id {
		t.Fatalf("detail moved to %q, want %q", m.keys[m.cursor].ID, id)
	}
	// The item is deleted: the detail closes.
	m.demo.keys = m.demo.keys[:1]
	m = press(t, m, "r")
	if m.detail {
		t.Fatal("detail stayed open on a key that is gone")
	}
}

func TestDashboardFitsEveryTerminalSize(t *testing.T) {
	for h := 4; h <= 30; h += 2 {
		for _, w := range []int{20, 30, 40, 53, 80, 120} {
			for tab := range 4 {
				m := press(t, demoModel(t, w, h), fmt.Sprint(tab+1))
				for step := 0; step <= m.count()+1; step++ {
					view := m.View()
					lines := strings.Split(view, "\n")
					if len(lines) != h {
						t.Fatalf("%dx%d tab %d step %d: %d lines", w, h, tab, step, len(lines))
					}
					for _, l := range lines {
						if lipgloss.Width(l) > w {
							t.Fatalf("%dx%d tab %d step %d: line %q is %d wide", w, h, tab, step, l, lipgloss.Width(l))
						}
					}
					if !strings.Contains(view, "›") {
						t.Fatalf("%dx%d tab %d step %d: no selected row:\n%s", w, h, tab, step, view)
					}
					m = press(t, m, "down")
				}
			}
		}
	}
}

func TestDashboardListWindowOnlyMovesAtTheEdge(t *testing.T) {
	m := demoModel(t, 100, 8) // 4 rows of 7 keys
	m = press(t, m, "down", "down", "down", "down", "down")
	if m.offset != 2 || m.cursor != 5 {
		t.Fatalf("offset %d cursor %d", m.offset, m.cursor)
	}
	m = press(t, m, "up")
	if m.offset != 2 || m.cursor != 4 {
		t.Fatalf("moving up inside the window moved it: offset %d cursor %d", m.offset, m.cursor)
	}
}

func TestDashboardResizeKeepsDetailInRange(t *testing.T) {
	m := press(t, demoModel(t, 100, 8), "enter")
	for range 30 {
		m = press(t, m, "down")
	}
	m = resize(m, 100, 40)
	m = m.clamp()
	if m.scroll != 0 {
		t.Fatalf("scroll %d after the terminal grew", m.scroll)
	}
	if !strings.Contains(m.View(), "KEY") {
		t.Fatal("the top of the detail is hidden")
	}
}

func TestDashboardDetailWrapsLongText(t *testing.T) {
	m := press(t, demoModel(t, 30, 40), "2", "enter") // a finding opens its key
	if !m.detail || m.tab != 0 {
		t.Fatalf("tab %d detail %v", m.tab, m.detail)
	}
	view := m.View()
	for _, l := range strings.Split(view, "\n") {
		if lipgloss.Width(l) > 30 {
			t.Fatalf("line %q is %d wide", l, lipgloss.Width(l))
		}
	}
	flat := strings.Join(strings.Fields(view), " ")
	if !strings.Contains(flat, "network it is used from.") {
		t.Fatalf("the end of the message is not shown:\n%s", view)
	}
}

func TestDashboardOpensTheKeyOfAFindingOrAuditEntry(t *testing.T) {
	m := press(t, demoModel(t, 100, 30), "2", "enter")
	if m.tab != 0 || !m.detail || m.keys[m.cursor].ID != "k_c81e55" {
		t.Fatalf("finding opened tab %d detail %v", m.tab, m.detail)
	}
	if !strings.Contains(m.View(), "Fix:") {
		t.Fatalf("the key's findings are missing:\n%s", m.View())
	}
	// The newest audit entry is a setting, which has no key to go to.
	m = press(t, m, "3", "enter")
	if m.tab != 2 || !m.detail || !strings.Contains(m.View(), "AUDIT") {
		t.Fatalf("setting entry: tab %d detail %v", m.tab, m.detail)
	}
	m = press(t, m, "esc", "down", "enter") // key.revoke k_e15c08
	if m.tab != 0 || !m.detail || m.keys[m.cursor].ID != "k_e15c08" {
		t.Fatalf("audit entry opened tab %d detail %v", m.tab, m.detail)
	}
}

func TestDashboardIgnoresNavigationWithNothingToNavigate(t *testing.T) {
	m := demoModel(t, 100, 24)
	m.loading = true
	m = press(t, dashboard{ctx: m.ctx, demo: m.demo, width: 100, height: 24, keys: m.keys, loading: true}, "down", "enter")
	if m.cursor != 0 || m.detail {
		t.Fatalf("moved while loading: cursor %d detail %v", m.cursor, m.detail)
	}
	m = dashboard{width: 100, height: 24, keys: demoModel(t, 100, 24).keys, err: fmt.Errorf("down")}
	m = press(t, m, "down", "enter")
	if m.cursor != 0 || m.detail {
		t.Fatalf("moved behind an error: cursor %d detail %v", m.cursor, m.detail)
	}
}

func TestDashboardBannerCountsFindings(t *testing.T) {
	m := demoModel(t, 100, 24)
	if !strings.Contains(m.View(), "2 exposure findings") {
		t.Fatalf("banner:\n%s", m.View())
	}
	m.findings = m.findings[:1]
	if v := m.View(); !strings.Contains(v, "1 exposure finding ·") {
		t.Fatalf("banner:\n%s", v)
	}
}

func TestDashboardColumnsAlignWhateverTheText(t *testing.T) {
	for _, s := range []string{"plain", "日本語のキー", "a-name-that-is-much-longer-than-the-column", "e\u0301"} {
		if got := lipgloss.Width(fit(s, 20)); got != 20 {
			t.Errorf("fit(%q, 20) is %d wide", s, got)
		}
	}
}

func TestDashboardShowsAKeyWithNoExpiryAsNever(t *testing.T) {
	m := dashboard{width: 100, height: 24, keys: []admin.Key{{ID: "k", Name: "forever", Status: admin.KeyActive, Expired: true}}}
	v := m.View()
	if !strings.Contains(v, "never") || strings.Contains(v, "expired") || strings.Contains(v, "0001") {
		t.Fatalf("view:\n%s", v)
	}
}

func TestDashboardNeedsTerminals(t *testing.T) {
	if err := checkTerminal(strings.NewReader(""), &bytes.Buffer{}); err == nil {
		t.Fatal("a buffer is not a terminal")
	}
}

func TestDashboardKeyDetailShowsWhereTheKeyIsUsedFrom(t *testing.T) {
	m := demoModel(t, 120, 60)
	i := slices.IndexFunc(m.keys, func(k admin.Key) bool { return k.ID == "k_0a44f9" })
	m.cursor = i
	m = press(t, m, "enter")
	view := m.View()
	for _, want := range []string{"WHERE IT IS USED FROM", "185.220.101.0/24", "AS60729 Stiftung Erneuerbare Freiheit", "0 forwarded, 57 refused", "client   curl/8", "client   anthropic-python/2"} {
		if !strings.Contains(view, want) {
			t.Errorf("the key's detail lacks %q:\n%s", want, view)
		}
	}

	m = press(t, m, "esc")
	m.cursor = slices.IndexFunc(m.keys, func(k admin.Key) bool { return k.ID == "k_c81e55" })
	if m = press(t, m, "enter"); !strings.Contains(m.View(), "Not used yet.") {
		t.Errorf("a key with no places:\n%s", m.View())
	}
}

func TestDashboardDropsTheAnswerForAKeyNoLongerOpen(t *testing.T) {
	m := dashboard{width: 100, height: 40, keys: []admin.Key{{ID: "key-1"}, {ID: "key-2"}}, detail: true, openID: "key-2", placesFor: "key-2"}
	next, _ := m.Update(dashboardPlaces{keyID: "key-1", places: []admin.Place{{Kind: "country", Value: "FR"}}})
	if m = next.(dashboard); m.places != nil {
		t.Fatalf("places of another key were kept: %+v", m.places)
	}
	m.cursor = 1
	if !strings.Contains(m.View(), "Loading...") {
		t.Errorf("places still on their way:\n%s", m.View())
	}
}

func TestDashboardListsTenPlacesAndPointsAtTheRest(t *testing.T) {
	var many []admin.Place
	for i := range 13 {
		many = append(many, admin.Place{Kind: "network", Value: fmt.Sprintf("10.0.%d.0/24", i)})
	}
	m := dashboard{width: 100, height: 60, keys: []admin.Key{{ID: "key-1", Name: "app"}}, detail: true, openID: "key-1", placesFor: "key-1", places: many}
	view := m.View()
	if !strings.Contains(view, "10.0.9.0/24") || strings.Contains(view, "10.0.10.0/24") || !strings.Contains(view, "...and 3 more: kman key places app") {
		t.Errorf("view:\n%s", view)
	}
}

func TestDashboardKeyDetailListsItsClientsByAddress(t *testing.T) {
	m := press(t, demoModel(t, 100, 40), "enter")
	if !m.detail || m.keys[m.cursor].ID != "k_9f2a1c" {
		t.Fatalf("no detail of the first key: detail %v", m.detail)
	}
	v := m.View()
	for _, want := range []string{"CLIENTS", "ADDRESS", "REQUESTS", "BLOCKED", "LAST SEEN", "10.0.4.17", "203.0.113.44", "DE AS3320"} {
		if !strings.Contains(v, want) {
			t.Errorf("the key's detail lacks %q:\n%s", want, v)
		}
	}
	// Close it and open another key: its own addresses replace the first key's.
	m = press(t, m, "esc", "down", "enter")
	if v := m.View(); !strings.Contains(v, "198.51.100.23") || strings.Contains(v, "10.0.4.17") {
		t.Errorf("the second key's detail shows the wrong addresses:\n%s", v)
	}
	// A key with none says so instead of showing an empty table.
	m = press(t, m, "esc", "down", "enter")
	if v := m.View(); !strings.Contains(v, "none in the events kman keeps") {
		t.Errorf("a key with no addresses:\n%s", v)
	}
}

func TestDashboardClientsSectionSaysWhyThereAreNone(t *testing.T) {
	off := &admin.Error{Code: admin.CodeConflict, Message: "the admin API does not give out client addresses until you turn that on: kman settings set expose_client_addresses true"}
	cases := []struct {
		name    string
		got     admin.KeyClients
		err     error
		loading bool
		want    string
	}{
		{"loading", admin.KeyClients{}, nil, true, "loading"},
		{"the setting is off", admin.KeyClients{}, off, false, "kman settings set expose_client_addresses true"},
		{"another failure", admin.KeyClients{}, fmt.Errorf("boom\x1b[2J"), false, "could not be loaded: boom"},
		{"too many to list", admin.KeyClients{Total: 80, Clients: []admin.KeyClient{{Address: "203.0.113.7", Requests: 1}}}, nil, false, "and 79 more addresses"},
	}
	for _, c := range cases {
		out := strings.Join(clientsSection(c.got, c.err, c.loading), "\n")
		if !strings.Contains(out, c.want) {
			t.Errorf("%s: %q lacks %q", c.name, out, c.want)
		}
		if strings.Contains(out, "\x1b") {
			t.Errorf("%s: a terminal control got through: %q", c.name, out)
		}
	}
}

func TestDashboardSaysNetworksAreNotShownInsteadOfNotUsedYet(t *testing.T) {
	m := dashboard{width: 100, height: 40, keys: []admin.Key{{ID: "key-1", Name: "app"}}, detail: true, openID: "key-1", placesFor: "key-1", places: []admin.Place{}, placesHidden: true}
	view := m.View()
	if strings.Contains(view, "Not used yet.") || !strings.Contains(view, "Networks are not shown") {
		t.Errorf("a key whose only places are withheld networks:\n%s", view)
	}
	m.places = []admin.Place{{Kind: "country", Value: "FR"}}
	if view := m.View(); !strings.Contains(view, "FR") || !strings.Contains(view, "Networks are not shown") {
		t.Errorf("places shown with networks withheld:\n%s", view)
	}
}
