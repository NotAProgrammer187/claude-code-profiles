package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------------------
// Theme — flat and solid: one accent, one warning, three greys. No gradients.
// ---------------------------------------------------------------------------

var (
	cAccent = lipgloss.AdaptiveColor{Light: "#0B7A3B", Dark: "#3DDC84"}
	cText   = lipgloss.AdaptiveColor{Light: "#1A1A1A", Dark: "#E8E8E8"}
	cDim    = lipgloss.AdaptiveColor{Light: "#6B6B6B", Dark: "#7A7A7A"}
	cFaint  = lipgloss.AdaptiveColor{Light: "#9A9A9A", Dark: "#4A4A4A"}
	cWarn   = lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#E0A63B"}
	cErr    = lipgloss.AdaptiveColor{Light: "#B00020", Dark: "#F2555A"}

	sMark    = lipgloss.NewStyle().Foreground(cAccent)
	sTagline = lipgloss.NewStyle().Foreground(cText)
	sTitle   = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	sCount   = lipgloss.NewStyle().Foreground(cFaint)
	sRule    = lipgloss.NewStyle().Foreground(cFaint)
	sName    = lipgloss.NewStyle().Foreground(cText).Bold(true)
	sNameSel = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	sMeta    = lipgloss.NewStyle().Foreground(cDim)
	sWarn    = lipgloss.NewStyle().Foreground(cWarn)
	sErr     = lipgloss.NewStyle().Foreground(cErr)
	sOK      = lipgloss.NewStyle().Foreground(cAccent)
	sHelp    = lipgloss.NewStyle().Foreground(cFaint)
	sKey     = lipgloss.NewStyle().Foreground(cDim).Bold(true)
	sBar     = lipgloss.NewStyle().Foreground(cAccent)
	sPrompt  = lipgloss.NewStyle().Foreground(cText)
	sActive  = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
)

// bigArt is the block-letter "CLAUDE PROFILES" wordmark, 113 columns wide.
var bigArt = []string{
	" ██████╗██╗      █████╗ ██╗   ██╗██████╗ ███████╗    ██████╗ ██████╗  ██████╗ ███████╗██╗██╗     ███████╗███████╗",
	"██╔════╝██║     ██╔══██╗██║   ██║██╔══██╗██╔════╝    ██╔══██╗██╔══██╗██╔═══██╗██╔════╝██║██║     ██╔════╝██╔════╝",
	"██║     ██║     ███████║██║   ██║██║  ██║█████╗      ██████╔╝██████╔╝██║   ██║█████╗  ██║██║     █████╗  ███████╗",
	"██║     ██║     ██╔══██║██║   ██║██║  ██║██╔══╝      ██╔═══╝ ██╔══██╗██║   ██║██╔══╝  ██║██║     ██╔══╝  ╚════██║",
	"╚██████╗███████╗██║  ██║╚██████╔╝██████╔╝███████╗    ██║     ██║  ██║╚██████╔╝██║     ██║███████╗███████╗███████║",
	" ╚═════╝╚══════╝╚═╝  ╚═╝ ╚═════╝ ╚═════╝ ╚══════╝    ╚═╝     ╚═╝  ╚═╝ ╚═════╝ ╚═╝     ╚═╝╚══════╝╚══════╝╚══════╝",
}

// markGradient shades the wordmark top-to-bottom, green into cyan. Chosen to be
// distinct from Anthropic's own brand palette.
var markGradient = []string{
	"#86EFAC", "#4ADE80", "#34D399", "#2DD4BF", "#22D3EE", "#38BDF8",
}

const bigArtWidth = 113

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

type view int

const (
	viewList view = iota
	viewPrompt
	viewConfirm
)

type promptKind int

const (
	promptNew promptKind = iota
	promptImport
	promptRename
)

type model struct {
	profiles []Profile
	cursor   int
	view     view

	prompt     textinput.Model
	promptKind promptKind
	renameFrom string

	notice    string
	err       error
	active    string // profile this shell's CLAUDE_CONFIG_DIR points at, if any
	filter    string // current filter query
	filtering bool   // true while the filter input is capturing keys
	width     int
	height    int
}

// visible is the profile list after applying the filter. The cursor always
// indexes into this slice, so every action operates on what's on screen.
func (m model) visible() []Profile {
	if m.filter == "" {
		return m.profiles
	}
	q := strings.ToLower(m.filter)
	var out []Profile
	for _, p := range m.profiles {
		if strings.Contains(strings.ToLower(p.Name), q) ||
			strings.Contains(strings.ToLower(p.Email), q) {
			out = append(out, p)
		}
	}
	return out
}

func (m *model) clampCursor() {
	n := len(m.visible())
	switch {
	case m.cursor < 0:
		m.cursor = 0
	case m.cursor >= n:
		m.cursor = max(0, n-1)
	}
}

type reloadMsg struct{}
type execDoneMsg struct{ err error }

func newModel() model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 32
	ti.Width = 30

	m := model{prompt: ti, width: 78, height: 24}
	m.reload()
	return m
}

func (m *model) reload() {
	ps, err := List()
	if err != nil {
		m.err = err
		return
	}
	m.profiles = ps
	m.active = ActiveProfileName(ps)
	m.clampCursor()
}

func (m model) Init() tea.Cmd { return textinput.Blink }

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = clamp(msg.Width-4, 40, 118)
		m.height = msg.Height
		return m, nil

	case reloadMsg:
		m.reload()
		return m, nil

	case execDoneMsg:
		if msg.err != nil {
			m.err = msg.err
		}
		m.reload()
		return m, nil

	case tea.KeyMsg:
		switch m.view {
		case viewPrompt:
			return m.updatePrompt(msg)
		case viewConfirm:
			return m.updateConfirm(msg)
		default:
			return m.updateList(msg)
		}
	}
	return m, nil
}

func (m model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.notice, m.err = "", nil

	if m.filtering {
		return m.updateFilter(msg)
	}

	vis := m.visible()
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit

	case "/":
		m.filtering = true
		return m, nil

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}

	case "down", "j":
		if m.cursor < len(vis)-1 {
			m.cursor++
		}

	case "enter":
		return m.launchSelected()

	case "n":
		return m.openPrompt(promptNew, "Name for the new profile")

	case "i":
		return m.openPrompt(promptImport, "Import current ~/.claude as")

	case "r":
		if len(vis) == 0 {
			return m, nil
		}
		m.renameFrom = vis[m.cursor].Name
		mm, cmd := m.openPrompt(promptRename, "Rename "+m.renameFrom+" to")
		m2 := mm.(model)
		m2.prompt.SetValue(m.renameFrom)
		m2.prompt.CursorEnd()
		return m2, cmd

	case "d":
		if len(vis) == 0 {
			return m, nil
		}
		m.view = viewConfirm

	default:
		// Digit keys 1-9 jump straight to that profile.
		if s := msg.String(); len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
			if idx := int(s[0] - '1'); idx < len(vis) {
				m.cursor = idx
			}
		}
	}
	return m, nil
}

// updateFilter handles keys while the filter input is active: typing narrows
// the list, arrows move within results, enter launches, esc clears.
func (m model) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.filter = ""
		m.filtering = false
		m.clampCursor()

	case "enter":
		m.filtering = false
		return m.launchSelected()

	case "up", "ctrl+p":
		if m.cursor > 0 {
			m.cursor--
		}

	case "down", "ctrl+n":
		if m.cursor < len(m.visible())-1 {
			m.cursor++
		}

	case "backspace":
		if r := []rune(m.filter); len(r) > 0 {
			m.filter = string(r[:len(r)-1])
			m.cursor = 0
		}

	default:
		if s := msg.String(); len([]rune(s)) == 1 {
			m.filter += s
			m.cursor = 0
		}
	}
	return m, nil
}

func (m model) launchSelected() (tea.Model, tea.Cmd) {
	vis := m.visible()
	if len(vis) == 0 {
		if len(m.profiles) == 0 {
			m.notice = "No profiles yet — press n to add one."
		}
		return m, nil
	}
	p := vis[m.cursor]
	cmd, err := Command(p, nil)
	if err != nil {
		m.err = err
		return m, nil
	}
	TouchProfile(p.Name)
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return execDoneMsg{err}
	})
}

func (m model) openPrompt(k promptKind, label string) (tea.Model, tea.Cmd) {
	m.view = viewPrompt
	m.promptKind = k
	m.notice = label
	m.prompt.SetValue("")
	m.prompt.Focus()
	return m, textinput.Blink
}

func (m model) updatePrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.view = viewList
		m.notice = ""
		return m, nil

	case "enter":
		name := strings.TrimSpace(m.prompt.Value())
		m.view = viewList
		m.notice = ""

		switch m.promptKind {
		case promptNew:
			p, err := Create(name)
			if err != nil {
				m.err = err
				return m, nil
			}
			m.reload()
			// Launch straight into it so Claude Code runs its login flow.
			cmd, err := Command(p, nil)
			if err != nil {
				m.err = err
				return m, nil
			}
			TouchProfile(p.Name)
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
				return execDoneMsg{err}
			})

		case promptImport:
			p, err := Import(name)
			if err != nil {
				m.err = err
				return m, nil
			}
			m.reload()
			m.notice = "Imported existing config into " + p.Name + "."
			return m, nil

		case promptRename:
			if err := Rename(m.renameFrom, name); err != nil {
				m.err = err
				return m, nil
			}
			m.reload()
			return m, nil
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.prompt, cmd = m.prompt.Update(msg)
	return m, cmd
}

func (m model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		name := m.visible()[m.cursor].Name
		if err := Delete(name); err != nil {
			m.err = err
		} else {
			m.notice = "Deleted " + name + "."
		}
		m.view = viewList
		m.reload()
	default:
		m.view = viewList
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// Banner
// ---------------------------------------------------------------------------

// Vertical cost of the big banner and the framed footer, blanks included.
const bigChrome = 16

func (m model) signedIn() int {
	n := 0
	for _, p := range m.profiles {
		if p.SignedIn {
			n++
		}
	}
	return n
}

func (m model) stats() string {
	s := plural(len(m.profiles), "profile")
	if n := m.signedIn(); n > 0 {
		s += fmt.Sprintf(" · %d signed in", n)
	}
	return s
}

// bigMark renders the block wordmark as a vertical green-to-cyan gradient,
// with a dim byline right-aligned underneath.
func bigMark() string {
	rows := make([]string, len(bigArt))
	for i, r := range bigArt {
		c := markGradient[i%len(markGradient)]
		rows[i] = lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Render(r)
	}
	art := strings.Join(rows, "\n")

	const byline = "by NotAProgrammer187"
	if pad := bigArtWidth - lipgloss.Width(byline); pad > 0 {
		art += "\n" + strings.Repeat(" ", pad) + sCount.Render(byline)
	}
	return lipgloss.NewStyle().PaddingLeft(1).Render(art)
}

// banner shows the framed wordmark when the terminal can hold it without
// pushing the profile list off screen, and a single line when it can't.
func (m model) banner() string {
	meta := "v" + version + " · " + m.stats()

	if m.width >= bigArtWidth-2 && m.height >= bigChrome+3*len(m.profiles) {
		return bigMark() + "\n\n" +
			"  " + sTagline.Render("Switch Claude Code accounts without logging out") + "\n" +
			"  " + sCount.Render(meta)
	}

	return "  " + sTitle.Render("claude code profiles") + "  " + sCount.Render(meta)
}

func (m model) rule() string {
	return "  " + sRule.Render(strings.Repeat("─", clamp(m.width, 40, bigArtWidth)))
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (m model) View() string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(m.banner())
	b.WriteString("\n\n")
	b.WriteString(m.rule())
	b.WriteString("\n\n")

	if m.filtering || m.filter != "" {
		cur := ""
		if m.filtering {
			cur = sActive.Render("▊")
		}
		b.WriteString("  " + sKey.Render("/") + " " + sPrompt.Render(m.filter) + cur + "\n\n")
	}

	vis := m.visible()
	switch {
	case len(m.profiles) == 0:
		b.WriteString("  " + sMeta.Render("No profiles yet.") + "\n\n")
		b.WriteString("  " + sKey.Render("i") + sHelp.Render("  import the account you're already signed into") + "\n")
		b.WriteString("  " + sKey.Render("n") + sHelp.Render("  add a fresh one") + "\n")
	case len(vis) == 0:
		b.WriteString("  " + sMeta.Render("No profiles match ") + sPrompt.Render(m.filter) + sMeta.Render(".") + "\n")
	}

	for i, p := range vis {
		sel := i == m.cursor && m.view == viewList
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(profileRow(i, p, sel, p.Name == m.active))
	}

	// Modal views take over the area below the list.
	switch m.view {
	case viewPrompt:
		b.WriteString("\n")
		b.WriteString(m.promptBox())
		return b.String()
	case viewConfirm:
		b.WriteString("\n")
		b.WriteString(m.confirmBox())
		return b.String()
	}

	// One reserved status line: error, notice, or API-key warning.
	b.WriteString("\n")
	switch {
	case m.err != nil:
		b.WriteString("  " + sErr.Render("✗ "+m.err.Error()) + "\n")
	case m.notice != "":
		b.WriteString("  " + sOK.Render("✓ "+m.notice) + "\n")
	default:
		if k := ApiKeyInEnv(); k != "" {
			b.WriteString("  " + sWarn.Render("! "+k+" is set in your shell — ccswitch unsets it for the session.") + "\n")
		} else {
			b.WriteString("\n")
		}
	}

	b.WriteString(m.rule() + "\n")
	if m.filtering {
		b.WriteString("  " + filterHelp() + "\n")
	} else {
		b.WriteString("  " + help() + "\n")
	}
	return b.String()
}

// statusDot is a single glyph that encodes auth state at a glance:
// filled green = signed in, filled amber = token will refresh, hollow = idle.
func statusDot(p Profile) string {
	_, kind := p.Status()
	switch kind {
	case StatusOK:
		return sOK.Render("●")
	case StatusWarn:
		return sWarn.Render("●")
	default:
		return sMeta.Render("○")
	}
}

// profileRow renders a two-line card:  [bar] [n] [dot] name  email
//                                                  plan · status · used · org
func profileRow(i int, p Profile, sel, active bool) string {
	bar := " "
	name := sName
	if sel {
		bar = sBar.Render("▌")
		name = sNameSel
	}
	num := sMeta.Render(" ")
	if i < 9 {
		num = sKey.Render(fmt.Sprintf("%d", i+1))
	}
	head := "  " + bar + " " + num + " " + statusDot(p) + " " +
		name.Render(pad(p.Name, 16)) + sMeta.Render(p.Label())
	if active {
		head += sActive.Render("  ◂ this shell")
	}
	// Indent the subline to sit under the name (8 cols of prefix above).
	sub := strings.Repeat(" ", 8) + sMeta.Render(subline(p))
	return head + "\n" + sub + "\n"
}

// promptBox is the bordered text-input modal used for new / import / rename.
func (m model) promptBox() string {
	inner := sPrompt.Render(m.notice) + "\n" + sTitle.Render("› ") + m.prompt.View()
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cAccent).
		Padding(0, 1).
		Render(inner)
	hint := "    " + sKey.Render("⏎") + sHelp.Render(" confirm   ") + sKey.Render("esc") + sHelp.Render(" cancel")
	return lipgloss.NewStyle().PaddingLeft(2).Render(box) + "\n" + hint + "\n"
}

// confirmBox is the bordered destructive-action modal used for delete.
func (m model) confirmBox() string {
	n := m.visible()[m.cursor].Name
	inner := sWarn.Render("Delete profile ") + sName.Render(n) + sWarn.Render("?") + "\n" +
		sMeta.Render("Removes its saved login and history — this can't be undone.")
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cWarn).
		Padding(0, 1).
		Render(inner)
	hint := "    " + sKey.Render("y") + sHelp.Render(" delete   ") + sKey.Render("n") + sHelp.Render(" cancel")
	return lipgloss.NewStyle().PaddingLeft(2).Render(box) + "\n" + hint + "\n"
}

func subline(p Profile) string {
	var parts []string
	if p.Plan != "" {
		parts = append(parts, p.Plan)
	}
	status, kind := p.Status()
	switch kind {
	case StatusWarn:
		status = sWarn.Render(status)
	case StatusOK:
		status = sOK.Render(status)
	}
	parts = append(parts, status)
	if !p.LastUsed.IsZero() {
		parts = append(parts, "used "+ago(p.LastUsed))
	}
	if p.Org != "" {
		parts = append(parts, p.Org)
	}
	return strings.Join(parts, " · ")
}

func help() string {
	pairs := [][2]string{
		{"↑↓", "move"}, {"/", "filter"}, {"⏎", "launch"}, {"n", "new"},
		{"i", "import"}, {"r", "rename"}, {"d", "delete"}, {"q", "quit"},
	}
	return renderKeys(pairs)
}

func filterHelp() string {
	return renderKeys([][2]string{
		{"type", "filter"}, {"↑↓", "move"}, {"⏎", "launch"}, {"esc", "clear"},
	})
}

func renderKeys(pairs [][2]string) string {
	var out []string
	for _, p := range pairs {
		out = append(out, sKey.Render(p[0])+sHelp.Render(" "+p[1]))
	}
	return strings.Join(out, sHelp.Render("   "))
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// pad measures display columns, not bytes, so non-ASCII names still align.
func pad(s string, n int) string {
	w := lipgloss.Width(s)
	if w >= n {
		return s + " "
	}
	return s + strings.Repeat(" ", n-w)
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
