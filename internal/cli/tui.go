package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kirank55/kman/internal/admin"
	"golang.org/x/term"
)

// The dashboard uses the same authenticated client as the other commands.
type dashboard struct {
	ctx                                    context.Context
	client                                 *client
	tab, cursor, width, height, generation int
	scroll                                 int
	loading, detail                        bool
	keys                                   []admin.Key
	findings                               []admin.ExposureFinding
	audit                                  []admin.AuditEntry
	upstreams                              []admin.Upstream
	err                                    error
}
type dashboardResult struct {
	tab, generation int
	keys            []admin.Key
	findings        []admin.ExposureFinding
	audit           []admin.AuditEntry
	upstreams       []admin.Upstream
	err             error
}

func (m dashboard) load() tea.Cmd {
	tab, generation, cl, ctx := m.tab, m.generation, m.client, m.ctx
	return func() tea.Msg {
		r := dashboardResult{tab: tab, generation: generation}
		switch tab {
		case 0:
			var v admin.KeyList
			v, r.err = cl.ListKeys(ctx, admin.KeyQuery{})
			r.keys = v.Keys
		case 1:
			var v admin.Exposure
			v, r.err = cl.Exposure(ctx)
			r.findings = v.Findings
		case 2:
			var v admin.AuditPage
			v, r.err = cl.Audit(ctx, admin.AuditQuery{Latest: true, Limit: 100})
			r.audit = v.Entries
		case 3:
			var v admin.UpstreamList
			v, r.err = cl.ListUpstreams(ctx)
			r.upstreams = v.Upstreams
		}
		return r
	}
}
func (m dashboard) Init() tea.Cmd { return m.load() }
func (m dashboard) count() int {
	switch m.tab {
	case 0:
		return len(m.keys)
	case 1:
		return len(m.findings)
	case 2:
		return len(m.audit)
	default:
		return len(m.upstreams)
	}
}
func (m dashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
	case dashboardResult:
		if v.generation != m.generation || v.tab != m.tab {
			break
		}
		m.loading = false
		m.err = v.err
		if v.err == nil {
			switch v.tab {
			case 0:
				m.keys = v.keys
			case 1:
				m.findings = v.findings
			case 2:
				m.audit = v.audit
			case 3:
				m.upstreams = v.upstreams
			}
		}
		if m.count() == 0 {
			m.detail = false
		}
		if m.cursor >= m.count() {
			m.cursor = max(0, m.count()-1)
		}
	case tea.KeyMsg:
		s := v.String()
		if s == "ctrl+c" || s == "q" {
			return m, tea.Quit
		}
		if m.detail {
			if s == "esc" || s == "enter" {
				m.detail = false
			} else if s == "down" || s == "j" {
				m.scroll = min(max(0, len(m.detailLines())-max(1, m.height-5)), m.scroll+1)
			} else if s == "up" || s == "k" {
				m.scroll = max(0, m.scroll-1)
			}
			return m, nil
		}
		switch s {
		case "1", "2", "3", "4":
			m.tab = int(s[0] - '1')
			m.cursor = 0
		case "tab", "right":
			m.tab = (m.tab + 1) % 4
			m.cursor = 0
		case "shift+tab", "left":
			m.tab = (m.tab + 3) % 4
			m.cursor = 0
		case "up", "k":
			m.cursor = max(0, m.cursor-1)
			return m, nil
		case "down", "j":
			m.cursor = min(max(0, m.count()-1), m.cursor+1)
			return m, nil
		case "enter":
			if !m.loading && m.err == nil && m.count() > 0 {
				m.detail = true
				m.scroll = 0
			}
			return m, nil
		case "r":
		default:
			return m, nil
		}
		m.generation++
		m.loading = true
		m.err = nil
		return m, m.load()
	}
	return m, nil
}

var dashboardAccent = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "27", Dark: "75"}).Bold(true)
var dashboardDim = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "242", Dark: "245"})

func cleanDashboard(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}
func (m dashboard) View() string {
	var b strings.Builder
	b.WriteString(dashboardAccent.Render("kman"))
	b.WriteString("    ")
	for i, t := range []string{"1 keys", "2 exposure", "3 audit", "4 setup"} {
		if i == m.tab {
			b.WriteString(dashboardAccent.Underline(true).Render(t))
		} else {
			b.WriteString(dashboardDim.Render(t))
		}
		b.WriteString("    ")
	}
	b.WriteString("\n\n")
	var lines []string
	if m.loading {
		lines = []string{"Loading…"}
	} else if m.err != nil {
		lines = []string{"Could not load: " + cleanDashboard(m.err.Error()), "Press r to retry."}
	} else if m.count() == 0 {
		lines = []string{"Nothing to show."}
	} else if m.detail {
		lines = m.detailLines()
		lines = lines[min(m.scroll, len(lines)):]
	} else {
		switch m.tab {
		case 0:
			lines = append(lines, "  NAME                 UPSTREAM           STATUS     EXPIRES")
			for _, k := range m.keys {
				lines = append(lines, fmt.Sprintf("  %-20s %-18s %-10s %s", cleanDashboard(orDash(k.Name)), cleanDashboard(orDash(k.Upstream)), keyState(k), when(&k.ExpiresAt)))
			}
		case 1:
			lines = append(lines, "  KEY                  FINDING")
			for _, f := range m.findings {
				lines = append(lines, fmt.Sprintf("  %-20s %s", cleanDashboard(orDash(f.Name)), cleanDashboard(f.Message)))
			}
		case 2:
			lines = append(lines, "  TIME                      ACTOR                ACTION")
			for i := len(m.audit) - 1; i >= 0; i-- {
				a := m.audit[i]
				lines = append(lines, fmt.Sprintf("  %-25s %-20s %s", when(&a.Time), cleanDashboard(a.Actor), cleanDashboard(a.Action)))
			}
		case 3:
			lines = append(lines, "  NAME                 PROFILE       HOST")
			for _, u := range m.upstreams {
				lines = append(lines, fmt.Sprintf("  %-20s %-13s %s:%d", cleanDashboard(u.Name), cleanDashboard(u.Profile), cleanDashboard(u.Host), u.Port))
			}
		}
		if m.cursor+1 < len(lines) {
			lines[m.cursor+1] = "›" + strings.TrimPrefix(lines[m.cursor+1], " ")
		}
		// Keep the selection visible in short terminals.
		available := max(1, m.height-6)
		if len(lines) > available && available > 1 {
			start := max(1, m.cursor-available+3)
			end := min(len(lines), start+available-1)
			lines = append(lines[:1:1], lines[start:end]...)
		}
	}
	available := max(1, m.height-5)
	for i, line := range lines {
		if i >= available {
			break
		}
		if m.width > 0 {
			line = lipgloss.NewStyle().MaxWidth(m.width).Render(line)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString("\n")
	if m.detail {
		b.WriteString(dashboardDim.Render("↑/↓ scroll · esc back · q quit"))
	} else {
		b.WriteString(dashboardDim.Render("1–4 views · ↑/↓ select · enter details · r refresh · q quit"))
	}
	return b.String()
}
func (m dashboard) detailLines() []string {
	switch m.tab {
	case 0:
		k := m.keys[m.cursor]
		lines := []string{"KEY  " + cleanDashboard(k.Name), "", "ID:          " + cleanDashboard(k.ID), "Upstream:    " + cleanDashboard(k.Upstream), "Credential:  " + cleanDashboard(k.Credential), "Status:      " + keyState(k), "Expires:     " + when(&k.ExpiresAt), "Tags:        " + cleanDashboard(strings.Join(k.Tags, ", ")), "Policy:      " + cleanDashboard(string(k.Policy))}
		if k.Spend != nil {
			lines = append(lines, "Today:       "+spendLine(k.Spend.Today), "Month:       "+spendLine(k.Spend.Month))
		}
		return lines
	case 1:
		f := m.findings[m.cursor]
		return []string{"EXPOSURE", "", cleanDashboard(f.Message), "Key: " + cleanDashboard(f.KeyID), "Fix: " + cleanDashboard(f.Fix)}
	case 2:
		a := m.audit[len(m.audit)-1-m.cursor]
		return []string{"AUDIT", "", "Time: " + when(&a.Time), "Actor: " + cleanDashboard(a.Actor), "Action: " + cleanDashboard(a.Action), "Target: " + cleanDashboard(a.TargetKind) + " " + cleanDashboard(a.TargetID)}
	default:
		u := m.upstreams[m.cursor]
		return []string{"UPSTREAM  " + cleanDashboard(u.Name), "", "Profile: " + cleanDashboard(u.Profile), fmt.Sprintf("Host: %s:%d", cleanDashboard(u.Host), u.Port), fmt.Sprintf("TLS: %t", u.TLS), "Paths: " + cleanDashboard(strings.Join(u.Paths, ", "))}
	}
}
func cmdDashboard(ctx context.Context, c *call) error {
	fs := c.flags("tui [--config-dir <dir>]", false)
	if _, err := c.parse(fs, 0, 0); err != nil {
		return err
	}
	out, ok := c.env.Stdout.(*os.File)
	if !ok || !term.IsTerminal(int(out.Fd())) {
		return fmt.Errorf("dashboard needs an interactive terminal")
	}
	cl, _, err := c.connect()
	if err != nil {
		return err
	}
	m := dashboard{ctx: ctx, client: cl, loading: true, width: 80, height: 24}
	_, err = tea.NewProgram(m, tea.WithAltScreen(), tea.WithInput(c.env.Stdin), tea.WithOutput(c.env.Stdout), tea.WithContext(ctx)).Run()
	return err
}
