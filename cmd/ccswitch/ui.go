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
)

// bigArt is the full framed wordmark, 82 columns wide. It contains backticks
// and backslashes, so it has to be written as interpreted string literals.
var bigArt = []string{
	" _____                                                                      _____ ",
	"( ___ )--------------------------------------------------------------------( ___ )",
	" |   |                                                                      |   | ",
	" |   |   ____ _                 _        ____             __ _ _            |   | ",
	" |   |  / ___| | __ _ _   _  __| | ___  |  _ \\ _ __ ___  / _(_) | ___  ___  |   | ",
	" |   | | |   | |/ _` | | | |/ _` |/ _ \\ | |_) | '__/ _ \\| |_| | |/ _ \\/ __| |   | ",
	" |   | | |___| | (_| | |_| | (_| |  __/ |  __/| | | (_) |  _| | |  __/\\__ \\ |   | ",
	" |   |  \\____|_|\\__,_|\\__,_|\\__,_|\\___| |_|   |_|  \\___/|_| |_|_|\\___||___/ |   | ",
	" |   |                                                by NotAProgrammer187  |   | ",
	" |___|                                                                      |___| ",
	"(_____)--------------------------------------------------------------------(_____)",
}

// bylineRow is rendered dimmer than the rest of the frame.
const bylineRow = 8

const bigArtWidth = 82

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

	notice string
	err    error
	width  int
	height int
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
	if m.cursor >= len(ps) {
		m.cursor = max(0, len(ps)-1)
	}
}

func (m model) Init() tea.Cmd { return textinput.Blink }

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = clamp(msg.Width-4, 40, 96)
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

	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}

	case "down", "j":
		if m.cursor < len(m.profiles)-1 {
			m.cursor++
		}

	case "enter":
		if len(m.profiles) == 0 {
			m.notice = "No profiles yet — press n to add one."
			return m, nil
		}
		p := m.profiles[m.cursor]
		cmd, err := Command(p, nil)
		if err != nil {
			m.err = err
			return m, nil
		}
		TouchProfile(p.Name)
		return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
			return execDoneMsg{err}
		})

	case "n":
		return m.openPrompt(promptNew, "Name for the new profile")

	case "i":
		return m.openPrompt(promptImport, "Import current ~/.claude as")

	case "r":
		if len(m.profiles) == 0 {
			return m, nil
		}
		m.renameFrom = m.profiles[m.cursor].Name
		mm, cmd := m.openPrompt(promptRename, "Rename "+m.renameFrom+" to")
		m2 := mm.(model)
		m2.prompt.SetValue(m.renameFrom)
		m2.prompt.CursorEnd()
		return m2, cmd

	case "d":
		if len(m.profiles) == 0 {
			return m, nil
		}
		m.view = viewConfirm
	}
	return m, nil
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
		name := m.profiles[m.cursor].Name
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

// Vertical cost of the framed banner, including the rule and its blank lines.
const bigChrome = 17

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

// bigMark renders the framed wordmark, dimming the byline row so the
// signature reads as a credit rather than part of the logo.
func bigMark() string {
	rows := make([]string, len(bigArt))
	for i, r := range bigArt {
		if i == bylineRow {
			rows[i] = sCount.Render(r)
			continue
		}
		rows[i] = sMark.Render(r)
	}
	return lipgloss.NewStyle().PaddingLeft(1).Render(strings.Join(rows, "\n"))
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

	if len(m.profiles) == 0 {
		b.WriteString(sMeta.Render("  No profiles yet.") + "\n")
		b.WriteString(sMeta.Render("  Press i to import the account you're already signed into,") + "\n")
		b.WriteString(sMeta.Render("  or n to add a fresh one.") + "\n")
	}

	for i, p := range m.profiles {
		sel := i == m.cursor && m.view == viewList
		bar, name := "  ", sName
		if sel {
			bar, name = sBar.Render("▌ "), sNameSel
		}
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("  " + bar + name.Render(pad(p.Name, 18)) + sMeta.Render(p.Label()) + "\n")
		b.WriteString("    " + sMeta.Render(subline(p)) + "\n")
	}

	b.WriteString("\n")

	switch m.view {
	case viewPrompt:
		b.WriteString("  " + sPrompt.Render(m.notice+": ") + m.prompt.View() + "\n\n")
		b.WriteString("  " + sHelp.Render("enter confirm · esc cancel") + "\n")
		return b.String()

	case viewConfirm:
		n := m.profiles[m.cursor].Name
		b.WriteString("  " + sWarn.Render("Delete "+n+" and its saved login? (y/n)") + "\n\n")
		return b.String()
	}

	if m.err != nil {
		b.WriteString("  " + sErr.Render("✗ "+m.err.Error()) + "\n\n")
	} else if m.notice != "" {
		b.WriteString("  " + sOK.Render("• "+m.notice) + "\n\n")
	} else if k := ApiKeyInEnv(); k != "" {
		b.WriteString("  " + sWarn.Render("! "+k+" is set in your shell; it would override the") + "\n")
		b.WriteString("  " + sWarn.Render("  subscription login. ccswitch unsets it for the session.") + "\n\n")
	}

	b.WriteString("  " + help() + "\n")
	return b.String()
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
		{"↑↓", "move"}, {"⏎", "launch"}, {"n", "new"},
		{"i", "import"}, {"r", "rename"}, {"d", "delete"}, {"q", "quit"},
	}
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
