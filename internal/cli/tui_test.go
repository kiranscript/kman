package cli

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
