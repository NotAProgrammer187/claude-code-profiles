package main

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// `ccswitch usage` answers "how much have I used?" once and exits, which is the
// wrong shape when the answer is the thing you want to keep half an eye on.
// `ccswitch usage --watch` is a panel you park in a second terminal beside
// Claude Code: every signed-in account, both rate-limit windows as meters, and
// the reset time under each — refreshed on its own so you notice a window
// filling up before it stops you.
//
// It is display-only, like the rest of the usage code: each profile's existing
// token is sent, nothing is written back, and a profile whose fetch fails shows
// a dim line instead of taking the panel down with it.

// ---------------------------------------------------------------------------
// ASCII art header
// ---------------------------------------------------------------------------

// watchArt overrides the animated mascot with art of your own: one string per
// line, centred, in the accent colour. Lines too wide for the panel are dropped
// rather than wrapped, so keep it under about 30 columns.
var watchArt = []string{}

// ---------------------------------------------------------------------------
// The mascot
// ---------------------------------------------------------------------------
//
// Three rows of quadrant block characters. The eyes aren't drawn — they're the
// gaps left by ▛ and ▜ in the top row, so the terminal background shows through
// and the face reads as two dark eyes whatever theme you're on.
//
// Each pose is left/eyes/right for row one and left/right for row two, with a
// solid body between them; row three is the feet. Widths differ per pose
// because the halves (▐ ▌) only fill half a cell, so every frame is padded to
// clawdWidth and centred rather than assumed to be the same length.

const clawdWidth = 9

type clawdPose struct {
	r1L, r1E, r1R string
	r2L, r2R      string
}

var clawdPoses = map[string]clawdPose{
	"default":    {" ▐", "▛███▜", "▌", "▝▜", "▛▘"},
	"look-left":  {" ▐", "▟███▟", "▌", "▝▜", "▛▘"},
	"look-right": {" ▐", "▙███▙", "▌", "▝▜", "▛▘"},
	"arms-up":    {"▗▟", "▛███▜", "▙▖", " ▜", "▛ "},
}

// clawdSequence is the idle loop: mostly still, with the odd glance and a
// stretch. Long runs of "default" are what keep it calm enough to sit beside
// something you're actually reading.
var clawdSequence = []string{
	"default", "default", "default", "default", "default", "default",
	"look-left", "look-left", "default", "default",
	"look-right", "look-right", "default", "default", "default", "default",
	"arms-up", "arms-up", "default", "default",
}

// clawdFrame renders one animation step as three centred lines.
func clawdFrame(step int) []string {
	if len(clawdSequence) == 0 {
		return nil
	}
	p := clawdPoses[clawdSequence[((step%len(clawdSequence))+len(clawdSequence))%len(clawdSequence)]]
	return []string{
		centreTo(p.r1L+p.r1E+p.r1R, clawdWidth),
		centreTo(p.r2L+"█████"+p.r2R, clawdWidth),
		centreTo("  ▘▘ ▝▝  ", clawdWidth),
	}
}

// centreTo pads s to width, keeping any odd column on the right so the mascot
// doesn't appear to drift between frames of different widths.
func centreTo(s string, width int) string {
	gap := width - lipgloss.Width(s)
	if gap <= 0 {
		return s
	}
	left := gap / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", gap-left)
}

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

const (
	watchMinEvery     = 15 * time.Second
	watchDefaultEvery = 60 * time.Second
	watchMinWidth     = 32
	watchMaxWidth     = 54
)

// clawdTick is slow on purpose: this panel sits in the corner of your eye, and
// anything livelier reads as something demanding attention.
const clawdTick = 700 * time.Millisecond

// sTrack is the unfilled part of a meter — present but recessive, so the bar
// reads as a proportion rather than a bar chart on an invisible axis.
var sTrack = lipgloss.NewStyle().Foreground(cFaint)

// The mascot keeps its own colour rather than the panel accent: the shape alone
// doesn't read as anything, the colour is most of what makes it recognisable.
var sClawd = lipgloss.NewStyle().Foreground(
	lipgloss.AdaptiveColor{Light: "#C1603F", Dark: "#D97757"})

type watchModel struct {
	profiles []Profile
	usage    map[string]*Usage
	errs     map[string]string
	active   string
	linked   string

	every   time.Duration
	updated time.Time
	pending int

	step int // mascot animation frame

	width  int
	height int
}

// watchUsageMsg is one profile's fetch landing. It carries the error text
// rather than the error so the view can show why a row is blank.
type watchUsageMsg struct {
	name string
	u    *Usage
	err  string
}

type watchTickMsg time.Time

// clawdTickMsg advances the mascot. It's a separate clock from the usage
// refresh so the animation stays smooth without polling the endpoint harder.
type clawdTickMsg time.Time

// runUsageWatch drives the panel. It takes the alt screen because this is a
// dashboard that owns its window, not output you scroll back through: the panel
// redraws in place for as long as it runs, and quitting leaves the terminal
// exactly as it was found.
func runUsageWatch(every time.Duration) error {
	_, err := tea.NewProgram(newWatchModel(every), tea.WithAltScreen()).Run()
	return err
}

func newWatchModel(every time.Duration) watchModel {
	m := watchModel{
		usage:  map[string]*Usage{},
		errs:   map[string]string{},
		every:  every,
		width:  40,
		height: 24,
	}
	m.reload()
	return m
}

func (m *watchModel) reload() {
	ps, err := List()
	if err != nil {
		return
	}
	m.profiles = ps
	m.active = ActiveProfileName(ps)
	m.linked, _, _ = CurrentDirProfile()
}

func (m watchModel) Init() tea.Cmd {
	return tea.Batch(m.fetchAll(), m.tick(), m.animate())
}

func (m watchModel) tick() tea.Cmd {
	return tea.Tick(m.every, func(t time.Time) tea.Msg { return watchTickMsg(t) })
}

func (m watchModel) animate() tea.Cmd {
	return tea.Tick(clawdTick, func(t time.Time) tea.Msg { return clawdTickMsg(t) })
}

// fetchAll asks every signed-in profile at once, so N accounts cost one
// round-trip rather than N in series. An expired token is reported instead of
// fetched — refreshing is Claude Code's job, not ours.
func (m watchModel) fetchAll() tea.Cmd {
	var cmds []tea.Cmd
	for _, p := range m.profiles {
		if !p.SignedIn {
			continue
		}
		p := p
		cmds = append(cmds, func() tea.Msg {
			u, err := FetchUsage(p)
			if err != nil {
				return watchUsageMsg{name: p.Name, err: err.Error()}
			}
			return watchUsageMsg{name: p.Name, u: u}
		})
	}
	return tea.Batch(cmds...)
}

func (m watchModel) countFetchable() int {
	n := 0
	for _, p := range m.profiles {
		if p.SignedIn {
			n++
		}
	}
	return n
}

func (m watchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case watchUsageMsg:
		if msg.err != "" {
			m.errs[msg.name] = msg.err
			delete(m.usage, msg.name)
		} else {
			m.usage[msg.name] = msg.u
			delete(m.errs, msg.name)
		}
		if m.pending > 0 {
			m.pending--
		}
		m.updated = time.Now()
		return m, nil

	case clawdTickMsg:
		m.step++
		return m, m.animate()

	case watchTickMsg:
		// Pick up profiles added or signed in since the last pass, so the panel
		// doesn't need restarting after a `ccswitch new`.
		m.reload()
		m.pending = m.countFetchable()
		return m, tea.Batch(m.fetchAll(), m.tick())

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "r":
			m.reload()
			m.pending = m.countFetchable()
			return m, m.fetchAll()
		}
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (m watchModel) contentWidth() int {
	w := m.width - 2 /* border */ - 4 /* padding */
	return clamp(w, watchMinWidth-6, watchMaxWidth-6)
}

func (m watchModel) View() string {
	cw := m.contentWidth()
	art := m.artLines(cw)

	// Prefer the roomy layout, but a panel that overflows the window is worse
	// than a terse one — with several accounts on a short terminal the reset
	// lines and subtitles are the first things worth losing.
	lines := append(append([]string{}, art...), m.body(cw, false)...)
	if m.height > 0 && len(lines)+2 > m.height {
		lines = append(append([]string{}, art...), m.body(cw, true)...)
	}

	inner := cw + 4
	out := []string{panelTop("ccswitch usage", inner)}
	for _, l := range lines {
		out = append(out, panelLine(l, cw, inner))
	}
	out = append(out, panelBottom(inner))
	return strings.Join(out, "\n") + "\n"
}

// body is everything between the rules: the accounts, then the footer.
func (m watchModel) body(cw int, compact bool) []string {
	lines := []string{""}

	signedIn := 0
	for _, p := range m.profiles {
		if p.SignedIn {
			signedIn++
		}
	}
	switch {
	case len(m.profiles) == 0:
		lines = append(lines, sMeta.Render("no profiles yet"), "")
	case signedIn == 0:
		lines = append(lines, sMeta.Render("no profile is signed in"), "")
	}

	for i, p := range m.profiles {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, m.profileBlock(p, cw, compact)...)
	}

	lines = append(lines, "")
	return append(lines, m.footer(cw)...)
}

// profileBlock is one account: who it is, then a meter per rate-limit window.
func (m watchModel) profileBlock(p Profile, cw int, compact bool) []string {
	var out []string

	tag := ""
	switch {
	case strings.EqualFold(p.Name, m.active):
		tag = sActive.Render("◂ shell")
	case strings.EqualFold(p.Name, m.linked):
		tag = sMeta.Render("◂ here")
	}
	out = append(out, spread(sNameSel.Render(statusDot(p)+" "+trunc(p.Name, cw-10)), tag, cw))

	if !compact {
		if sub := profileSubtitle(p, cw-2); sub != "" {
			out = append(out, "  "+sMeta.Render(sub))
		}
	}

	if !p.SignedIn {
		out = append(out, "", "  "+sMeta.Render("not signed in"))
		return out
	}
	if e, ok := m.errs[p.Name]; ok {
		out = append(out, "", "  "+sMeta.Render(trunc(e, cw-2)))
		return out
	}
	u, ok := m.usage[p.Name]
	if !ok {
		out = append(out, "", "  "+sMeta.Render("checking…"))
		return out
	}

	for _, w := range []struct {
		label string
		short string
		win   *UsageWindow
		// Opus only earns a meter once it's actually been used; on plans
		// without a separate Opus window it would otherwise sit at a
		// permanent 0% and imply something is wrong.
		onlyIfUsed bool
	}{
		{"5 hours", "5h", u.Session, false},
		{"This week", "week", u.Week, false},
		{"Opus this week", "opus", u.Opus, true},
	} {
		if w.win == nil || (w.onlyIfUsed && w.win.Utilization <= 0) {
			continue
		}
		if compact {
			out = append(out, meterRow(w.short, w.win, cw-2))
			continue
		}
		out = append(out, "")
		out = append(out, meterBlock(w.label, w.win, cw-2)...)
	}
	return out
}

// meterBlock is the roomy form: a label/percent row, a full-width bar, and the
// reset time under it.
func meterBlock(label string, w *UsageWindow, cw int) []string {
	right := meterStyle(w.Utilization).Render(fmt.Sprintf("%d%%", pct(w.Utilization)))
	out := []string{
		"  " + spread(sName.Render(label), right, cw),
		"  " + meterBar(cw, w.Utilization),
	}
	if !w.ResetsAt.IsZero() {
		out = append(out, "  "+sMeta.Render("resets "+resetClock(w.ResetsAt)))
	}
	return out
}

// meterRow is the one-line form used when the panel has to fit a short window:
// label, bar and percentage side by side, reset time dropped.
func meterRow(label string, w *UsageWindow, cw int) string {
	const labelCol, pctCol = 6, 5
	bar := cw - labelCol - pctCol
	if bar < 4 {
		bar = 4
	}
	right := fmt.Sprintf("%4d%%", pct(w.Utilization))
	return "  " + sMeta.Render(pad(label, labelCol-1)) + " " +
		meterBar(bar, w.Utilization) +
		meterStyle(w.Utilization).Render(right)
}

// meterBar draws the proportion filled. Any non-zero usage keeps at least one
// cell, so 1% reads as "started" rather than as nothing at all.
func meterBar(width int, util float64) string {
	if width < 4 {
		width = 4
	}
	frac := util / 100
	switch {
	case frac < 0:
		frac = 0
	case frac > 1:
		frac = 1
	}
	filled := int(frac*float64(width) + 0.5)
	if filled == 0 && util > 0 {
		filled = 1
	}
	if filled > width {
		filled = width
	}
	return meterStyle(util).Render(strings.Repeat("█", filled)) +
		sTrack.Render(strings.Repeat("░", width-filled))
}

// meterStyle uses the same thresholds as the picker's rows: amber at 70%, red
// at 90%, so the two views never disagree about what counts as "getting close".
func meterStyle(util float64) lipgloss.Style {
	switch {
	case util >= 90:
		return sErr
	case util >= 70:
		return sWarn
	default:
		return sOK
	}
}

func (m watchModel) footer(cw int) []string {
	stamp := "checking…"
	if m.pending > 0 {
		stamp = "refreshing…"
	} else if !m.updated.IsZero() {
		stamp = "updated " + m.updated.Local().Format("3:04 PM")
	}
	return []string{
		sMeta.Render(trunc(stamp+" · every "+compactDuration(m.every), cw)),
		renderKeys([][2]string{{"r", "refresh"}, {"q", "quit"}}),
	}
}

func profileSubtitle(p Profile, max int) string {
	var parts []string
	if p.Plan != "" {
		parts = append(parts, p.Plan)
	}
	if p.Email != "" {
		parts = append(parts, p.Email)
	}
	return trunc(strings.Join(parts, " · "), max)
}

// ---------------------------------------------------------------------------
// Panel chrome
// ---------------------------------------------------------------------------
//
// The border is drawn by hand rather than with lipgloss's box, because the
// title belongs *in* the top rule — that's what makes it read like Claude
// Code's own panels rather than a generic framed box.

func panelTop(title string, inner int) string {
	fill := inner - 3 - lipgloss.Width(title)
	if fill < 0 {
		fill = 0
	}
	return sRule.Render("╭─ ") + sTitle.Render(title) +
		sRule.Render(" "+strings.Repeat("─", fill)+"╮")
}

func panelBottom(inner int) string {
	return sRule.Render("╰" + strings.Repeat("─", inner) + "╯")
}

// panelLine pads content to the panel's inner width. Width is measured in
// display columns and ignores styling, so colour never shifts the right edge.
func panelLine(content string, cw, inner int) string {
	gap := cw - lipgloss.Width(content)
	if gap < 0 {
		gap = 0
	}
	return sRule.Render("│") + "  " + content + strings.Repeat(" ", gap) + "  " +
		sRule.Render("│")
}

// spread puts left and right on one line with the right edge flush, falling
// back to a single space when they'd collide.
func spread(left, right string, cw int) string {
	if right == "" {
		return left
	}
	gap := cw - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

// artLines is the panel header: your own art if you set watchArt, otherwise the
// mascot on its current frame. Either way lines too wide to fit are dropped
// rather than wrapped into nonsense.
func (m watchModel) artLines(cw int) []string {
	art, style := watchArt, sMark
	if len(art) == 0 {
		art, style = clawdFrame(m.step), sClawd
	}

	var out []string
	for _, l := range art {
		if lipgloss.Width(l) > cw {
			continue
		}
		pad := (cw - lipgloss.Width(l)) / 2
		out = append(out, strings.Repeat(" ", pad)+style.Render(l))
	}
	return out
}

// trunc shortens plain text to max display columns. It is only ever called on
// unstyled strings — cutting a styled one would slice an escape sequence.
func trunc(s string, max int) string {
	if max < 1 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}

func compactDuration(d time.Duration) string {
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}
