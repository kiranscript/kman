package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kirank55/kman/internal/admin"
	"golang.org/x/term"
)

// The dashboard uses the same authenticated client as the other commands.
//
// The invariants Update keeps (through clamp) so View can be a plain reading
// of the state: cursor is inside the current view's rows; offset is the first
// row the list shows and keeps the cursor in sight; a detail is open only on
// an item that exists, and scroll stays inside it.
type dashboard struct {
	ctx    context.Context
	client *client
	demo   *demoData // sample data in place of a server's

	tab                    int
	cursor, offset, scroll int // the selected row, the first row shown, the first detail line shown
	width, height          int
	generation             int // bumped by every load, so a late answer to an older one is dropped
	loading, detail        bool
	openID                 string // the item the detail shows, found again after a refresh
	jumpKey                string // a key to open as soon as the keys view has loaded

	// The open key's client addresses, loaded when its detail opens. clientsFor is the key
	// they are, or are being, loaded for; empty when none are wanted.
	clientsFor     string
	clients        admin.KeyClients
	clientsErr     error
	clientsLoading bool

	keys      []admin.Key
	findings  []admin.ExposureFinding
	audit     []admin.AuditEntry
	upstreams []admin.Upstream
	err       error
}

var dashboardTabs = []string{"keys", "exposure", "audit", "upstreams"}

type dashboardResult struct {
	tab, generation int
	keys            []admin.Key
	findings        []admin.ExposureFinding
	audit           []admin.AuditEntry
	upstreams       []admin.Upstream
	err             error
}

func (m dashboard) load() tea.Cmd {
	tab, generation, cl, ctx, demo := m.tab, m.generation, m.client, m.ctx, m.demo
	return func() tea.Msg {
		r := dashboardResult{tab: tab, generation: generation}
		if demo != nil {
			r.keys, r.findings, r.audit, r.upstreams = demo.keys, demo.exposure.Findings, demo.audit, demo.upstreams
			return r
		}
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

// dashboardClients is the answer to a load of a key's client addresses.
type dashboardClients struct {
	id  string
	got admin.KeyClients
	err error
}

type dashboardBanner struct {
	generation int
	findings   []admin.ExposureFinding
}

func (m dashboard) loadView() tea.Cmd {
	if m.tab != 0 {
		return m.load()
	}
	cl, ctx, generation, demo := m.client, m.ctx, m.generation, m.demo
	return tea.Batch(m.load(), func() tea.Msg {
		if demo != nil {
			return dashboardBanner{generation: generation, findings: demo.exposure.Findings}
		}
		exp, err := cl.Exposure(ctx)
		if err != nil {
			return dashboardBanner{generation: generation}
		}
		return dashboardBanner{generation: generation, findings: exp.Findings}
	})
}

func (m dashboard) Init() tea.Cmd { return m.loadView() }

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

// itemID identifies row i of the current view across refreshes. The audit
// log is shown newest first, so its row i is the entry len-1-i.
func (m dashboard) itemID(i int) string {
	switch m.tab {
	case 0:
		return m.keys[i].ID
	case 1:
		return m.findings[i].KeyID + "/" + m.findings[i].Rule
	case 2:
		return strconv.FormatInt(m.audit[len(m.audit)-1-i].ID, 10)
	default:
		return m.upstreams[i].ID
	}
}

func (m dashboard) indexOf(id string) int {
	for i := range m.count() {
		if m.itemID(i) == id {
			return i
		}
	}
	return -1
}

// bodyHeight is the lines between the header and banner above and the footer
// below. A list spends one of them on its column heads.
func (m dashboard) bodyHeight() int { return max(1, m.height-3) }
func (m dashboard) listRows() int {
	if m.bodyHeight() < 2 {
		return 1 // no room for the column heads
	}
	return m.bodyHeight() - 1
}

// textWidth is what a detail wraps to.
func (m dashboard) textWidth() int {
	if m.width > 0 {
		return m.width
	}
	return 80
}

// ready reports whether the rows on screen are the current view's, so it can
// be navigated.
func (m dashboard) ready() bool { return !m.loading && m.err == nil && m.count() > 0 }

// clamp restores the invariants after any change to the state.
func (m dashboard) clamp() dashboard {
	n := m.count()
	m.cursor = min(max(0, m.cursor), max(0, n-1))
	rows := m.listRows()
	m.offset = max(0, min(m.offset, n-rows))
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+rows {
		m.offset = m.cursor - rows + 1
	}
	if m.detail && n > 0 {
		m.scroll = min(max(0, m.scroll), max(0, len(m.wrappedDetail())-m.bodyHeight()))
	} else {
		m.detail, m.scroll = false, 0
	}
	return m
}

// reconcile is what a load's answer does to the state around the data it
// replaced: an open detail follows its item or closes when the item is gone,
// and a pending jump to a key opens it.
func (m dashboard) reconcile() dashboard {
	if m.err != nil {
		m.detail = false
	}
	if m.detail {
		if i := m.indexOf(m.openID); i >= 0 {
			m.cursor = i
		} else {
			m.detail = false
		}
	}
	if m.jumpKey != "" && m.tab == 0 && !m.loading {
		if m.err == nil {
			if i := m.indexOf(m.jumpKey); i >= 0 {
				m.cursor, m.detail, m.scroll, m.openID = i, true, 0, m.jumpKey
			}
		}
		m.jumpKey = ""
	}
	return m.clamp()
}

// switchTab shows view i from the top, closing any detail, and loads it.
func (m dashboard) switchTab(i int) (dashboard, tea.Cmd) {
	m.tab = i
	m.cursor, m.offset, m.scroll = 0, 0, 0
	m.detail, m.openID, m.jumpKey, m.clientsFor = false, "", "", ""
	return m.refresh()
}

// refresh loads the current view again, keeping the place in it.
func (m dashboard) refresh() (dashboard, tea.Cmd) {
	m.clientsFor = "" // an open key's addresses are loaded again with it
	m.generation++
	m.loading = true
	m.err = nil
	return m, m.loadView()
}

// open shows the selected row in full. A finding or an audit entry about a key
// goes to that key, which lists its findings.
func (m dashboard) open() (dashboard, tea.Cmd) {
	switch {
	case m.tab == 1:
		return m.openKey(m.findings[m.cursor].KeyID)
	case m.tab == 2 && m.audit[len(m.audit)-1-m.cursor].TargetKind == "key":
		return m.openKey(m.audit[len(m.audit)-1-m.cursor].TargetID)
	}
	m.detail, m.scroll, m.openID = true, 0, m.itemID(m.cursor)
	return m.wantClients()
}

// wantClients loads the open key's client addresses, once, when a key's detail is
// open and they are not already loaded or on their way. The admin API gives them
// out only when the install turned that on, and says so when it has not: the
// answer, either way, is what the detail shows.
func (m dashboard) wantClients() (dashboard, tea.Cmd) {
	if !m.detail || m.tab != 0 || m.openID == "" || m.openID == m.clientsFor {
		return m.clamp(), nil
	}
	m.clientsFor, m.clients, m.clientsErr, m.clientsLoading = m.openID, admin.KeyClients{}, nil, true
	id, cl, ctx, demo := m.openID, m.client, m.ctx, m.demo
	return m.clamp(), func() tea.Msg {
		if demo != nil {
			return dashboardClients{id: id, got: demo.clients[id]}
		}
		got, err := cl.KeyClients(ctx, admin.KeyRef{ID: id})
		return dashboardClients{id: id, got: got, err: err}
	}
}

func (m dashboard) openKey(id string) (dashboard, tea.Cmd) {
	m, cmd := m.switchTab(0)
	m.jumpKey = id
	return m, cmd
}

func (m dashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
	case dashboardBanner:
		if m.tab == 0 && v.generation == m.generation {
			m.findings = v.findings
		}
	case dashboardResult:
		if v.generation != m.generation || v.tab != m.tab {
			return m, nil
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
		m = m.reconcile()
		var cmd tea.Cmd
		m, cmd = m.wantClients()
		return m, cmd
	case dashboardClients:
		if v.id == m.clientsFor {
			m.clients, m.clientsErr, m.clientsLoading = v.got, v.err, false
		}
		return m.clamp(), nil
	case tea.KeyMsg:
		var cmd tea.Cmd
		switch s := v.String(); s {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "1", "2", "3", "4":
			m, cmd = m.switchTab(int(s[0] - '1'))
		case "tab", "right":
			m, cmd = m.switchTab((m.tab + 1) % len(dashboardTabs))
		case "shift+tab", "left":
			m, cmd = m.switchTab((m.tab + len(dashboardTabs) - 1) % len(dashboardTabs))
		case "r":
			m, cmd = m.refresh()
		case "esc":
			m.detail, m.clientsFor = false, ""
		case "enter":
			if m.detail {
				m.detail, m.clientsFor = false, ""
			} else if m.ready() {
				m, cmd = m.open()
			}
		case "up", "k":
			if m.detail {
				m.scroll--
			} else if m.ready() {
				m.cursor--
			}
		case "down", "j":
			if m.detail {
				m.scroll++
			} else if m.ready() {
				m.cursor++
			}
		}
		return m.clamp(), cmd
	}
	return m.clamp(), nil
}

var dashboardAccent = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "27", Dark: "75"}).Bold(true)
var dashboardDim = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "242", Dark: "245"})
var dashboardWarn = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "172", Dark: "214"})
var dashboardOK = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "28", Dark: "42"})
var dashboardSelected = lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Light: "254", Dark: "237"}).Bold(true)

// cleanDashboard replaces what a terminal would act on in text from the
// server, which a hostile upstream or key name could carry.
func cleanDashboard(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

// cut shortens plain text to n columns, ending in … when it cuts.
func cut(s string, n int) string {
	s = cleanDashboard(s)
	if n <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= n {
		return s
	}
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if w+rw > n-1 {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String() + "…"
}

// fit is s in exactly n columns, by display width and not by character count.
func fit(s string, n int) string {
	s = cut(s, n)
	return s + strings.Repeat(" ", max(0, n-lipgloss.Width(s)))
}

// clip cuts styled text to width columns, when the width is known.
func (m dashboard) clip(s string) string {
	if m.width <= 0 {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(m.width).Render(s)
}

// keyShown is a key's status as the dashboard shows it. A key with no expiry
// has not expired, whatever the server's flag says of the zero time.
func keyShown(k admin.Key) string {
	if k.Expired && !k.ExpiresAt.IsZero() && k.Status == admin.KeyActive {
		return "expired"
	}
	return k.Status
}

func expiryShown(k admin.Key) string {
	if k.ExpiresAt.IsZero() {
		return "never"
	}
	return when(&k.ExpiresAt)
}

func (m dashboard) header() string {
	title := lipgloss.NewStyle().Bold(true).Render("kman")
	sub := ""
	switch {
	case m.demo != nil:
		sub = dashboardDim.Render(" · demo data")
	case m.client != nil:
		sub = dashboardDim.Render(" · " + cut(m.client.shown, 40))
	}
	tabs := func(short bool) string {
		var out []string
		for i, t := range dashboardTabs {
			label := strconv.Itoa(i+1) + " " + t
			if short {
				label = strconv.Itoa(i + 1)
			}
			if i == m.tab {
				out = append(out, dashboardAccent.Render(label))
			} else {
				out = append(out, dashboardDim.Render(label))
			}
		}
		return strings.Join(out, "  ")
	}
	// Drop what matters least until the header fits.
	for _, c := range [][2]string{{title + sub, tabs(false)}, {title, tabs(false)}, {title, tabs(true)}, {"", tabs(true)}} {
		gap := m.width - lipgloss.Width(c[0]) - lipgloss.Width(c[1])
		if m.width <= 0 {
			gap = 2
		}
		if gap >= 2 || c[0] == "" {
			return m.clip(c[0] + strings.Repeat(" ", max(gap, 0)) + c[1])
		}
	}
	return ""
}

func (m dashboard) banner() string {
	if m.tab != 0 || m.loading || m.err != nil || m.detail || len(m.findings) == 0 {
		return ""
	}
	n := len(m.findings)
	noun := "findings"
	if n == 1 {
		noun = "finding"
	}
	return m.clip(dashboardWarn.Render(fmt.Sprintf("! %d exposure %s · press 2 to inspect", n, noun)))
}

func (m dashboard) footer() string {
	text := "1–4 views · ↑/↓ select · enter open · r refresh · q quit"
	if m.detail {
		text = "↑/↓ scroll · 1–4 views · r refresh · esc back · q quit"
	}
	return m.clip(dashboardDim.Render(text))
}

// head and row are the list's columns. Every column but the last is a fixed
// number of display columns, so wide characters and long names keep them in line.
func (m dashboard) head() string {
	switch m.tab {
	case 0:
		return "  " + fit("name", 20) + " " + fit("status", 11) + " " + fit("upstream", 20) + " expires"
	case 1:
		return "  " + fit("key", 20) + " finding"
	case 2:
		return "  " + fit("time", 17) + " " + fit("actor", 20) + " action"
	default:
		return "  " + fit("name", 20) + " " + fit("profile", 13) + " host"
	}
}

func (m dashboard) row(i int, selected bool) string {
	mark := "  "
	if selected {
		mark = "› "
	}
	switch m.tab {
	case 0:
		k := m.keys[i]
		state := fit(keyShown(k), 11)
		if !selected {
			style := dashboardWarn
			if keyShown(k) == admin.KeyActive {
				style = dashboardOK
			}
			state = style.Render(state)
		}
		return mark + fit(orDash(k.Name), 20) + " " + state + " " + fit(orDash(k.Upstream), 20) + " " + expiryShown(k)
	case 1:
		f := m.findings[i]
		room := m.width - 2 - 21
		if m.width <= 0 {
			room = 1000
		}
		return mark + fit(orDash(f.Name), 20) + " " + cut(f.Message, room)
	case 2:
		a := m.audit[len(m.audit)-1-i]
		return mark + fit(when(&a.Time), 17) + " " + fit(a.Actor, 20) + " " + cleanDashboard(a.Action)
	default:
		u := m.upstreams[i]
		return mark + fit(u.Name, 20) + " " + fit(u.Profile, 13) + " " + cleanDashboard(u.Host) + ":" + strconv.Itoa(u.Port)
	}
}

func (m dashboard) body() []string {
	switch {
	case m.loading && !m.detail:
		return []string{"Loading…"}
	case m.err != nil:
		return []string{"Could not load: " + cleanDashboard(m.err.Error()), "Press r to retry."}
	case m.count() == 0:
		return []string{"Nothing to show."}
	case m.detail:
		lines := m.wrappedDetail()
		return lines[min(m.scroll, len(lines)):]
	}
	var lines []string
	if m.bodyHeight() >= 2 {
		lines = append(lines, dashboardDim.Render(m.clip(m.head())))
	}
	for i := m.offset; i < min(m.count(), m.offset+m.listRows()); i++ {
		line := m.clip(m.row(i, false))
		if i == m.cursor {
			row := m.clip(m.row(i, true))
			if pad := m.width - lipgloss.Width(row); pad > 0 {
				row += strings.Repeat(" ", pad)
			}
			line = dashboardSelected.Render(row)
		}
		lines = append(lines, line)
	}
	return lines
}

func (m dashboard) View() string {
	body := m.body()
	if len(body) > m.bodyHeight() {
		body = body[:m.bodyHeight()]
	}
	var b strings.Builder
	b.WriteString(m.header() + "\n")
	b.WriteString(m.banner() + "\n")
	for _, line := range body {
		b.WriteString(m.clip(line) + "\n")
	}
	b.WriteString(strings.Repeat("\n", m.bodyHeight()-len(body)))
	b.WriteString(m.footer())
	return b.String()
}

// wrappedDetail is the detail's lines wrapped to the width, since a long
// message, policy or command has to be readable and the detail only scrolls
// down.
func (m dashboard) wrappedDetail() []string {
	var out []string
	wrap := lipgloss.NewStyle().Width(m.textWidth())
	for _, line := range m.detailLines() {
		for _, l := range strings.Split(wrap.Render(line), "\n") {
			out = append(out, strings.TrimRight(l, " "))
		}
	}
	return out
}

func (m dashboard) detailLines() []string {
	switch m.tab {
	case 0:
		k := m.keys[m.cursor]
		lines := []string{"KEY  " + cleanDashboard(k.Name), "", "ID:          " + cleanDashboard(k.ID), "Upstream:    " + cleanDashboard(k.Upstream), "Credential:  " + cleanDashboard(k.Credential), "Status:      " + keyShown(k), "Expires:     " + expiryShown(k), "Tags:        " + cleanDashboard(strings.Join(k.Tags, ", ")), "Policy:      " + cleanDashboard(string(k.Policy))}
		if k.Spend != nil {
			lines = append(lines, "Today:       "+spendLine(k.Spend.Today), "Month:       "+spendLine(k.Spend.Month))
		}
		lines = append(lines, clientsSection(m.clients, m.clientsErr, m.clientsLoading || m.clientsFor != k.ID)...)
		for _, f := range m.findings {
			if f.KeyID == k.ID {
				lines = append(lines, "", "! "+cleanDashboard(f.Message), "  Fix: "+cleanDashboard(f.Fix))
			}
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

// clientsSection is a key's client addresses as lines of the detail: a table of
// the addresses it was used from, or why there is none. The admin API refuses to
// give addresses out until the install turns that on, and says how.
func clientsSection(got admin.KeyClients, err error, loading bool) []string {
	head := "CLIENTS  by address, from the events kman keeps"
	var ae *admin.Error
	switch {
	case loading:
		return []string{"", "CLIENTS  loading…"}
	case errors.As(err, &ae) && ae.Code == admin.CodeConflict:
		return []string{"", "CLIENTS  not shown", "  " + cleanDashboard(ae.Message)}
	case err != nil:
		return []string{"", "CLIENTS  could not be loaded: " + cleanDashboard(err.Error())}
	case len(got.Clients) == 0:
		return []string{"", "CLIENTS  none in the events kman keeps"}
	}
	width := len("ADDRESS")
	addrs := make([]string, len(got.Clients))
	for i, c := range got.Clients {
		addrs[i] = cut(cleanDashboard(c.Address), 24)
		width = max(width, len([]rune(addrs[i])))
	}
	lines := []string{"", head, fmt.Sprintf("  %-*s  %8s  %7s  %-10s  %s", width, "ADDRESS", "REQUESTS", "BLOCKED", "LAST SEEN", "WHERE")}
	for i, c := range got.Clients {
		where := cleanDashboard(c.Country)
		if c.ASN != 0 {
			where = strings.TrimSpace(where + fmt.Sprintf(" AS%d", c.ASN))
		}
		lines = append(lines, fmt.Sprintf("  %-*s  %8d  %7d  %-10s  %s", width, addrs[i], c.Requests, c.Blocked, c.LastSeen.UTC().Format("2006-01-02"), where))
	}
	if got.Total > len(got.Clients) {
		lines = append(lines, fmt.Sprintf("  and %d more addresses", got.Total-len(got.Clients)))
	}
	return lines
}

// checkTerminal fails unless both ends are terminals: the program reads keys
// from stdin, and with a pipe or /dev/null it fails or ignores every key.
func checkTerminal(in io.Reader, out io.Writer) error {
	o, ok := out.(*os.File)
	if !ok || !term.IsTerminal(int(o.Fd())) {
		return errors.New("dashboard needs an interactive terminal")
	}
	if i, ok := in.(*os.File); ok && !term.IsTerminal(int(i.Fd())) {
		return errors.New("dashboard needs an interactive terminal: stdin is not one")
	}
	return nil
}

func cmdDashboard(ctx context.Context, c *call) error {
	fs := c.flags("tui [--demo] [--config-dir <dir>]", false)
	demo := fs.Bool("demo", false, "browse built-in sample data instead of a server's; needs no install or server, and changes nothing")
	if _, err := c.parse(fs, 0, 0); err != nil {
		return err
	}
	if err := checkTerminal(c.env.Stdin, c.env.Stdout); err != nil {
		return err
	}
	m := dashboard{ctx: ctx, loading: true, width: 80, height: 24}
	if *demo {
		var now time.Time
		if c.env.Now != nil {
			now = c.env.Now()
		}
		m.demo = newDemoData(now)
	} else {
		cl, _, err := c.connect()
		if err != nil {
			return err
		}
		m.client = cl
	}
	// Ask the terminal for its background colour now. Left to the first
	// draw, the question waits for an answer a terminal may never give while
	// the program holds the keyboard, and keys pressed meanwhile are lost.
	lipgloss.SetHasDarkBackground(lipgloss.HasDarkBackground())
	opts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithOutput(c.env.Stdout), tea.WithContext(ctx)}
	if c.env.Stdin != nil {
		opts = append(opts, tea.WithInput(c.env.Stdin))
	}
	_, err := tea.NewProgram(m, opts...).Run()
	return err
}
