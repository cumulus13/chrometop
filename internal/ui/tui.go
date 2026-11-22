package ui

import (
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/cumulus13/chrometop/internal/hub"
	"github.com/cumulus13/chrometop/internal/protocol"
)

// Fixed screen layout (in terminal rows). The table body gets whatever is left
// over, so the footer always sits on the last row of the terminal.
//
//	topPanelLines  rounded border + 2 summary lines
//	filterLines    filter / sort hint row
//	headerLines    table column header
//	footer         1 status line + up to maxHintLines key-hint lines
const (
	topPanelLines = 4
	filterLines   = 1
	headerLines   = 1
	maxHintLines  = 2
	minWidth      = 40
	minHeight     = 10
)

// How long a locally generated banner (clipboard result, dispatch error) stays
// visible before the backend event log is shown again.
const bannerTTL = 8 * time.Second

// Table column widths in terminal cells.
const (
	colTab   = 5
	colPID   = 6
	colState = 6
	colCPU   = 7
	colNet   = 9
	colHeap  = 8
	colMem   = 8
)

// fixedColsWidth is the width of everything left of the TITLE column: a leading
// space plus seven columns that are each followed by one separator space.
const fixedColsWidth = 1 + colTab + 1 + colPID + 1 + colState + 1 + colCPU + 1 + colNet + 1 + colHeap + 1 + colMem + 1

const minTitleWidth = 12

const minURLWidth = 12

// hubChangedMsg tells the model that the hub published new state.
type hubChangedMsg struct{}

// tickMsg is the periodic UI clock. Exactly one tick chain exists at any time.
type tickMsg time.Time

// bannerMsg shows a short local status message in the footer.
type bannerMsg struct{ text string }

type sortField int

const (
	sortCPU sortField = iota
	sortNet
	sortFootprint
	sortJSHeap
	sortPID
	sortTabID
	sortTitle
)

const sortFieldCount = 7

func (s sortField) label() string {
	switch s {
	case sortCPU:
		return "CPU%"
	case sortNet:
		return "NET/S"
	case sortFootprint:
		return "EST-MB"
	case sortJSHeap:
		return "JS-HEAP"
	case sortPID:
		return "PID"
	case sortTabID:
		return "TAB-ID"
	case sortTitle:
		return "TITLE"
	default:
		return "CPU%"
	}
}

var (
	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#00E5FF")).
			Padding(0, 1)

	inspectorStyle = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color("#FACC15")).
			Padding(0, 1)

	brandStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#0B0F17")).
			Background(lipgloss.Color("#00E5FF")).
			Padding(0, 1)

	modeBadgeStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#0B0F17")).
			Background(lipgloss.Color("#BD93F9")).
			Padding(0, 1)

	onlineStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#00FF9C"))

	offlineStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FF5555"))

	colHeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#0B0F17")).
			Background(lipgloss.Color("#BD93F9"))

	rowSelectedStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(lipgloss.Color("#283548"))

	rowNormalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E2E8F0"))

	rowDiscardedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#64748B"))

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#64748B"))

	keyCapStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#0B0F17")).
			Background(lipgloss.Color("#FACC15")).
			Padding(0, 1)

	footerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E2E8F0")).
			Background(lipgloss.Color("#111827")).
			Padding(0, 1)
)

// Model is the Bubble Tea model for ChromeTop.
//
// Update and View never block and never touch the network: commands run inside
// tea.Cmd goroutines. View is a pure function of the model, and hub state is
// copied once per hub change (hubChangedMsg) rather than once per frame. Hub
// changes arrive through a coalescing channel, so a burst of backend updates
// results in a single redraw.
type Model struct {
	hub       *hub.Server
	sub       <-chan struct{}
	endpoint  string
	newTabURL string

	width  int
	height int

	snap protocol.Snapshot
	rows []protocol.TabInfo

	cursor       int
	scroll       int
	selectedID   int
	hasSelection bool

	sortBy     sortField
	sortAsc    bool
	filtering  bool
	filterText string
	inspecting bool

	banner   string
	bannerAt time.Time
}

// NewModel initializes a ChromeTop TUI model.
func NewModel(h *hub.Server, endpoint string) Model {
	m := Model{
		hub:       h,
		sub:       h.Subscribe(),
		endpoint:  endpoint,
		newTabURL: "about:blank",
		width:     136,
		height:    34,
		sortBy:    sortCPU,
	}
	m.snap = h.Snapshot()
	m.refreshRows()
	return m
}

// WithNewTabURL sets the URL opened by the "n" key.
func (m Model) WithNewTabURL(u string) Model {
	if strings.TrimSpace(u) != "" {
		m.newTabURL = u
	}
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(waitForChange(m.sub), tickCmd())
}

// waitForChange blocks in its own goroutine until the hub signals a change.
func waitForChange(ch <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		<-ch
		return hubChangedMsg{}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch ev := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = ev.Width
		m.height = ev.Height
		m.clampScroll()
		return m, nil

	case hubChangedMsg:
		prevLog := m.snap.LastEventLog
		m.snap = m.hub.Snapshot()
		if m.snap.LastEventLog != prevLog {
			// A newer backend event supersedes an older local banner.
			m.banner = ""
		}
		m.refreshRows()
		return m, waitForChange(m.sub)

	case tickMsg:
		// Re-arming happens only here, so there is exactly one tick chain.
		return m, tickCmd()

	case bannerMsg:
		m.setBanner(ev.text)
		return m, nil

	case tea.KeyMsg:
		if ev.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.filtering {
			return m.onFilterKey(ev)
		}
		if m.inspecting {
			switch ev.String() {
			case "esc", "i", "enter", "q":
				m.inspecting = false
			}
			return m, nil
		}
		return m.onKey(ev)
	}
	return m, nil
}

func (m Model) onFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyEnter:
		m.filtering = false
	case tea.KeyBackspace:
		if runes := []rune(m.filterText); len(runes) > 0 {
			m.filterText = string(runes[:len(runes)-1])
			m.resetSelection()
			m.refreshRows()
		}
	case tea.KeySpace:
		m.filterText += " "
		m.resetSelection()
		m.refreshRows()
	case tea.KeyRunes:
		m.filterText += string(msg.Runes)
		m.resetSelection()
		m.refreshRows()
	}
	return m, nil
}

func (m Model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "up", "k":
		m.moveCursor(m.cursor - 1)
	case "down", "j":
		m.moveCursor(m.cursor + 1)
	case "pgup", "ctrl+u":
		m.moveCursor(m.cursor - m.visibleRows())
	case "pgdown", "ctrl+d":
		m.moveCursor(m.cursor + m.visibleRows())
	case "g", "home":
		m.moveCursor(0)
	case "G", "end":
		m.moveCursor(len(m.rows) - 1)
	case "/":
		m.filtering = true
	case "esc":
		if m.filterText != "" {
			m.filterText = ""
			m.resetSelection()
			m.refreshRows()
		}
	case "s":
		m.sortBy = (m.sortBy + 1) % sortFieldCount
		m.refreshRows()
	case "o", "S":
		m.sortAsc = !m.sortAsc
		m.refreshRows()
	case "i":
		if _, ok := m.currentTab(); ok {
			m.inspecting = true
		}
	case "enter", "f":
		return m, m.tabCommand("FOCUS")
	case "x", "delete":
		return m, m.tabCommand("CLOSE")
	case "r":
		return m, m.tabCommand("RELOAD")
	case "d":
		return m, m.tabCommand("DISCARD")
	case "m":
		return m, m.tabCommand("MUTE")
	case "p":
		return m, m.tabCommand("PIN")
	case "c":
		return m, m.tabCommand("DUPLICATE")
	case "n":
		return m, m.urlCommand("NEW_TAB", m.newTabURL)
	case "R":
		return m, m.urlCommand("SYNC", "")
	case "y":
		if t, ok := m.currentTab(); ok {
			return m, copyCmd(t)
		}
	}
	return m, nil
}

// tabCommand returns a Cmd that sends action for the selected tab. The send
// happens off the UI goroutine because backends perform network I/O.
func (m Model) tabCommand(action string) tea.Cmd {
	t, ok := m.currentTab()
	if !ok {
		return nil
	}
	return runCommand(m.hub, protocol.CommandMsg{
		Type:      "COMMAND",
		Action:    action,
		TabID:     t.ID,
		TargetID:  t.TargetID,
		WindowID:  t.WindowID,
		RequestID: newRequestID(),
	})
}

func (m Model) urlCommand(action, target string) tea.Cmd {
	return runCommand(m.hub, protocol.CommandMsg{
		Type:      "COMMAND",
		Action:    action,
		URL:       target,
		RequestID: newRequestID(),
	})
}

// runCommand delivers cmd to the active backend. Success is reported by the
// backend itself through the hub event log, so only failures produce a banner.
func runCommand(h *hub.Server, cmd protocol.CommandMsg) tea.Cmd {
	return func() tea.Msg {
		if err := h.SendCommand(cmd); err != nil {
			return bannerMsg{text: "[!] " + err.Error()}
		}
		return nil
	}
}

func copyCmd(t protocol.TabInfo) tea.Cmd {
	return func() tea.Msg {
		if err := copyToClipboard(t.URL); err != nil {
			return bannerMsg{text: fmt.Sprintf("[!] Clipboard error: %v", err)}
		}
		return bannerMsg{text: fmt.Sprintf("📋 Copied URL for tab %d", t.ID)}
	}
}

func newRequestID() string {
	return fmt.Sprintf("cmd-%d", time.Now().UnixNano())
}

func (m *Model) setBanner(text string) {
	m.banner = text
	m.bannerAt = time.Now()
}

func (m Model) statusLine() string {
	switch {
	case m.banner != "" && time.Since(m.bannerAt) < bannerTTL:
		return m.banner
	case m.snap.LastEventLog != "":
		return m.snap.LastEventLog
	default:
		return "✨ Ready"
	}
}

func (m Model) currentTab() (protocol.TabInfo, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return protocol.TabInfo{}, false
	}
	return m.rows[m.cursor], true
}

// resetSelection makes the next refresh start at the top of the list.
func (m *Model) resetSelection() {
	m.hasSelection = false
	m.cursor = 0
	m.scroll = 0
}

// moveCursor selects row i (clamped) and remembers which tab it is, so the
// selection follows the tab when the sort order changes between refreshes.
func (m *Model) moveCursor(i int) {
	if len(m.rows) == 0 {
		m.cursor, m.scroll, m.hasSelection = 0, 0, false
		return
	}
	m.cursor = max(0, min(i, len(m.rows)-1))
	m.selectedID = m.rows[m.cursor].ID
	m.hasSelection = true
	m.clampScroll()
}

// refreshRows recomputes the filtered and sorted table rows from m.snap.
func (m *Model) refreshRows() {
	m.rows = m.filteredSortedTabs()

	switch {
	case len(m.rows) == 0:
		m.cursor, m.scroll, m.hasSelection = 0, 0, false
		return
	case m.hasSelection:
		for i := range m.rows {
			if m.rows[i].ID == m.selectedID {
				m.cursor = i
				m.clampScroll()
				return
			}
		}
	}

	m.cursor = max(0, min(m.cursor, len(m.rows)-1))
	m.selectedID = m.rows[m.cursor].ID
	m.hasSelection = true
	m.clampScroll()
}

func (m *Model) clampScroll() {
	total := len(m.rows)
	if total == 0 {
		m.cursor, m.scroll = 0, 0
		return
	}
	m.cursor = max(0, min(m.cursor, total-1))

	visible := m.visibleRows()
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	} else if m.cursor >= m.scroll+visible {
		m.scroll = m.cursor - visible + 1
	}

	// Never scroll past the end: a shrinking list must not leave blank rows.
	m.scroll = max(0, min(m.scroll, total-visible))
}

func (m Model) effWidth() int {
	return max(minWidth, m.width)
}

func (m Model) effHeight() int {
	return max(minHeight, m.height)
}

// visibleRows is the number of table rows that fit between the header and the
// footer. It is derived from the real footer height, not from a magic number.
func (m Model) visibleRows() int {
	rows := m.effHeight() - topPanelLines - filterLines - headerLines - footerHeight(m.effWidth())
	return max(1, rows)
}

func footerHeight(w int) int {
	return 1 + len(hintLines(w))
}

// filteredSortedTabs applies the current filter and sort order to the last
// snapshot taken from the hub.
func (m Model) filteredSortedTabs() []protocol.TabInfo {
	needle := strings.ToLower(strings.TrimSpace(m.filterText))

	out := make([]protocol.TabInfo, 0, len(m.snap.Tabs))
	for _, t := range m.snap.Tabs {
		if needle != "" &&
			!strings.Contains(strings.ToLower(t.Title), needle) &&
			!strings.Contains(strings.ToLower(t.URL), needle) {
			continue
		}
		out = append(out, t)
	}

	sort.SliceStable(out, func(i, j int) bool {
		var less bool
		var equal bool

		switch m.sortBy {
		case sortCPU:
			equal = out[i].CPUPercent == out[j].CPUPercent
			less = out[i].CPUPercent < out[j].CPUPercent
		case sortNet:
			equal = out[i].NetBytesPerSec == out[j].NetBytesPerSec
			less = out[i].NetBytesPerSec < out[j].NetBytesPerSec
		case sortFootprint:
			equal = out[i].EstimatedMB == out[j].EstimatedMB
			less = out[i].EstimatedMB < out[j].EstimatedMB
		case sortJSHeap:
			equal = out[i].JSHeapUsedMB == out[j].JSHeapUsedMB
			less = out[i].JSHeapUsedMB < out[j].JSHeapUsedMB
		case sortPID:
			equal = out[i].ProcessID == out[j].ProcessID
			less = out[i].ProcessID < out[j].ProcessID
		case sortTitle:
			a := strings.ToLower(out[i].Title)
			b := strings.ToLower(out[j].Title)
			equal = a == b
			less = a < b
		default:
			equal = out[i].ID == out[j].ID
			less = out[i].ID < out[j].ID
		}

		if equal {
			return out[i].ID < out[j].ID
		}
		if m.sortAsc {
			return less
		}
		return !less
	})

	return out
}

func (m Model) sortDirectionArrow() string {
	if m.sortAsc {
		return "▲ ASC"
	}
	return "▼ DESC"
}

// View renders exactly effHeight() rows, each at most effWidth() cells wide.
func (m Model) View() string {
	w, h := m.effWidth(), m.effHeight()

	if m.inspecting {
		if sel, ok := m.currentTab(); ok {
			return fitHeight(m.renderInspector(sel, w), h)
		}
	}

	frame := strings.Join([]string{
		m.renderTopPanel(w),
		m.renderFilterRow(w),
		m.renderHeader(w),
		m.renderBody(w, m.visibleRows()),
		m.renderFooter(w),
	}, "\n")

	return fitHeight(frame, h)
}

func (m Model) renderTopPanel(w int) string {
	snap := m.snap

	connBadge := offlineStyle.Render("🔴 OFFLINE")
	if snap.Connected {
		connBadge = onlineStyle.Render("🟢 ONLINE")
	}

	modeText := snap.Mode
	if modeText == "" {
		modeText = "EXTENSION"
	}

	cpuMeter := renderBar(snap.Summary.TotalCPUPerc, 100.0, 14)
	memMeter := renderBar(snap.Summary.EstimatedTotal, 2048.0, 14)

	syncTime := "never"
	if !snap.LastSync.IsZero() {
		syncTime = snap.LastSync.Format("15:04:05")
	}

	line1 := fmt.Sprintf(
		"%s %s %s  🔌 %s  |  🔀 Sort: %s (%s)  |  📑 Tabs: %d  🔊 %d  💤 %d",
		brandStyle.Render("⚡ CHROMETOP"),
		modeBadgeStyle.Render(modeText),
		connBadge,
		sanitize(m.endpoint),
		m.sortBy.label(),
		m.sortDirectionArrow(),
		snap.Summary.TotalTabs,
		snap.Summary.AudibleTabs,
		snap.Summary.DiscardedTabs,
	)

	line2 := fmt.Sprintf(
		"🔥 CPU: %s %5.1f%%  |  📊 Mem: %s %.1f MB  |  🌐 Net: %s/s  |  🧠 Heap: %.1f MB  |  🕒 %s",
		cpuMeter,
		snap.Summary.TotalCPUPerc,
		memMeter,
		snap.Summary.EstimatedTotal,
		formatBytes(snap.Summary.TotalNetRate),
		snap.Summary.TotalJSHeapMB,
		syncTime,
	)

	// Border (2) + padding (2) leave w-4 cells for text. Truncating here keeps
	// the panel at a constant height; wrapping would push the footer off screen.
	inner := w - 4
	content := ansi.Truncate(line1, inner, "…") + "\n" + ansi.Truncate(line2, inner, "…")
	return panelStyle.Width(w - 2).Render(content)
}

func (m Model) renderFilterRow(w int) string {
	row := dimStyle.Render("🔎 Press '/' to filter  |  [s] Cycle Sort Column  |  [o] Toggle ASC ▲ / DESC ▼")
	if m.filtering || m.filterText != "" {
		caret := ""
		if m.filtering {
			caret = "▌"
		}
		row = fmt.Sprintf("🔎 Filter: %s%s  [%d/%d tabs]", sanitize(m.filterText), caret, len(m.rows), len(m.snap.Tabs))
	}
	return ansi.Truncate(row, w, "…")
}

func (m Model) renderHeader(w int) string {
	titleW, urlW := calcCols(w)

	cells := []string{
		col(m.colLabel(sortTabID, "TAB"), colTab),
		col(m.colLabel(sortPID, "PID"), colPID),
		col("STATE", colState),
		col(m.colLabel(sortCPU, "CPU%"), colCPU),
		col(m.colLabel(sortNet, "NET/S"), colNet),
		col(m.colLabel(sortJSHeap, "HEAP"), colHeap),
		col(m.colLabel(sortFootprint, "MEM"), colMem),
		col(m.colLabel(sortTitle, "TITLE"), titleW),
	}
	if urlW > 0 {
		cells = append(cells, col("URL", urlW))
	}

	text := clip(joinRow(cells), w)
	return colHeaderStyle.Width(w).Render(text)
}

// renderBody renders exactly rows lines of table content.
func (m Model) renderBody(w, rows int) string {
	lines := make([]string, 0, rows)

	if len(m.rows) == 0 {
		msg := fmt.Sprintf("⏳ Waiting for Chrome/Chromium telemetry on %s ...", sanitize(m.endpoint))
		if m.snap.Connected {
			msg = "📭 No matching tabs found."
		}
		lines = append(lines, dimStyle.Render(clip("  "+msg, w)))
	} else {
		titleW, urlW := calcCols(w)
		end := min(len(m.rows), m.scroll+rows)
		for i := m.scroll; i < end; i++ {
			t := m.rows[i]
			text := clip(renderTabLine(t, titleW, urlW), w)
			switch {
			case i == m.cursor:
				lines = append(lines, rowSelectedStyle.Width(w).Render(text))
			case t.Discarded:
				lines = append(lines, rowDiscardedStyle.Render(text))
			default:
				lines = append(lines, rowNormalStyle.Render(text))
			}
		}
	}

	for len(lines) < rows {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderFooter(w int) string {
	inner := w - 2 // horizontal padding of footerStyle
	status := ansi.Truncate("📡 "+sanitize(m.statusLine()), inner, "…")

	lines := append([]string{status}, hintLines(w)...)
	return footerStyle.Width(w).Render(strings.Join(lines, "\n"))
}

func (m Model) renderInspector(sel protocol.TabInfo, w int) string {
	lines := []string{
		fmt.Sprintf("🔎 TAB INSPECTOR — ID %d (Window %d, PID %d, Target %s)", sel.ID, sel.WindowID, sel.ProcessID, fallbackStr(sel.TargetID, "n/a")),
		fmt.Sprintf("📌 Title     : %s", sanitize(sel.Title)),
		fmt.Sprintf("🔗 URL       : %s", sanitize(sel.URL)),
		fmt.Sprintf("⚡ Status    : %s | Active=%v | Pinned=%v | Discarded=%v", sel.Status, sel.Active, sel.Pinned, sel.Discarded),
		fmt.Sprintf("🔥 CPU       : %.1f%%", sel.CPUPercent),
		fmt.Sprintf("🌐 Network   : %s/s (Total transferred: %s)", formatBytes(sel.NetBytesPerSec), formatBytes(float64(sel.NetTotalBytes))),
		fmt.Sprintf("🧠 JS Heap   : %.2f MB / %.2f MB used/total | DOM Nodes: %d", sel.JSHeapUsedMB, sel.JSHeapTotalMB, sel.DOMNodes),
		fmt.Sprintf("📊 Est. Mem  : %.1f MB", sel.EstimatedMB),
		dimStyle.Render("Press [Esc] / [i] / [Enter] to return to table"),
	}

	inner := w - 4
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], inner, "…")
	}
	return inspectorStyle.Width(w - 2).Render(strings.Join(lines, "\n"))
}

// fitHeight pads or cuts s so that it has exactly h lines.
func fitHeight(s string, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// hintChips lists the key hints shown in the footer, most important first so
// that narrow terminals keep the essential ones.
func hintChips() []string {
	chip := func(key, label string) string {
		return keyCapStyle.Render(key) + " " + label
	}
	return []string{
		chip("↑/↓", "Nav"),
		chip("Enter", "Focus"),
		chip("i", "Inspect"),
		chip("r", "Reload"),
		chip("x", "Close"),
		chip("/", "Filter"),
		chip("s", "SortCol"),
		chip("o", "Asc/Desc"),
		chip("q", "Quit"),
		chip("d", "Discard"),
		chip("m", "Mute"),
		chip("p", "Pin"),
		chip("c", "Dup"),
		chip("n", "New"),
		chip("y", "Copy"),
	}
}

// hintLines packs the key hints into at most maxHintLines lines that fit the
// footer's inner width. Hints that do not fit are dropped.
func hintLines(w int) []string {
	inner := w - 2 // horizontal padding of footerStyle

	var lines []string
	cur := ""
	curW := 0

	for _, chip := range hintChips() {
		cw := lipgloss.Width(chip)
		if curW > 0 && curW+1+cw > inner {
			lines = append(lines, cur)
			cur, curW = "", 0
			if len(lines) == maxHintLines {
				return lines
			}
		}
		if curW > 0 {
			cur += " "
			curW++
		}
		cur += chip
		curW += cw
	}
	if curW > 0 {
		lines = append(lines, cur)
	}
	return lines
}

func (m Model) colLabel(field sortField, base string) string {
	if m.sortBy != field {
		return base
	}
	if m.sortAsc {
		return base + "▲"
	}
	return base + "▼"
}

func renderTabLine(t protocol.TabInfo, titleW, urlW int) string {
	icon := "🌐"
	switch {
	case t.Discarded:
		icon = "💤"
	case t.Status == "loading":
		icon = "⏳"
	case t.Audible && !t.Muted:
		icon = "🔊"
	case t.Muted:
		icon = "🔇"
	case t.Active:
		icon = "🔥"
	}

	badge := " "
	if t.Pinned {
		badge = "📌"
	}

	pidStr := "-"
	if t.ProcessID > 0 {
		pidStr = strconv.Itoa(t.ProcessID)
	}

	heapStr := "-"
	if t.JSHeapUsedMB > 0 {
		heapStr = fmt.Sprintf("%.1fM", t.JSHeapUsedMB)
	}

	cells := []string{
		col(strconv.Itoa(t.ID), colTab),
		col(pidStr, colPID),
		col(icon+badge, colState),
		col(fmt.Sprintf("%5.1f%%", t.CPUPercent), colCPU),
		col(formatBytes(t.NetBytesPerSec)+"/s", colNet),
		col(heapStr, colHeap),
		padLeft(clip(fmt.Sprintf("%.1fM", t.EstimatedMB), colMem), colMem),
		col(sanitize(t.Title), titleW),
	}
	if urlW > 0 {
		cells = append(cells, col(sanitize(t.URL), urlW))
	}
	return joinRow(cells)
}

// joinRow joins already padded cells with single spaces and a leading space.
func joinRow(cells []string) string {
	return " " + strings.Join(cells, " ")
}

// calcCols splits the width left of the fixed columns between TITLE and URL.
// The URL column is dropped on narrow terminals.
func calcCols(totalW int) (titleW, urlW int) {
	avail := totalW - fixedColsWidth
	switch {
	case avail < minTitleWidth:
		return minTitleWidth, 0
	case avail < minTitleWidth+1+minURLWidth:
		return avail, 0
	default:
		titleW = (avail - 1) * 45 / 100
		urlW = avail - 1 - titleW
		return titleW, urlW
	}
}

// col clips s to width terminal cells and pads it with spaces to exactly width.
// Unlike fmt's %-Ns it measures display width, so emoji and CJK text stay aligned.
func col(s string, width int) string {
	return padRight(clip(s, width), width)
}

func clip(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "…")
}

func padRight(s string, width int) string {
	if gap := width - ansi.StringWidth(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

func padLeft(s string, width int) string {
	if gap := width - ansi.StringWidth(s); gap > 0 {
		return strings.Repeat(" ", gap) + s
	}
	return s
}

// sanitize makes untrusted text (page titles, URLs, error messages) safe to
// print: control characters, including ESC, are removed so a malicious page
// cannot inject terminal escape sequences or break the layout with newlines.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return ' '
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			return -1
		}
		return r
	}, s)
}

func formatBytes(bps float64) string {
	if bps <= 0 {
		return "0B"
	}
	units := []string{"B", "KB", "MB", "GB"}
	idx := 0
	val := bps
	for val >= 1024 && idx < len(units)-1 {
		val /= 1024
		idx++
	}
	if idx == 0 {
		return fmt.Sprintf("%.0fB", val)
	}
	return fmt.Sprintf("%.1f%s", val, units[idx])
}

func renderBar(current, scaleMax float64, width int) string {
	if scaleMax <= 0 {
		scaleMax = 1
	}
	ratio := current / scaleMax
	if ratio > 1 {
		ratio = 1
	}
	if ratio < 0 {
		ratio = 0
	}
	done := int(ratio * float64(width))
	rem := width - done

	color := lipgloss.Color("#00FF9C")
	if ratio > 0.75 {
		color = lipgloss.Color("#FF5555")
	} else if ratio > 0.45 {
		color = lipgloss.Color("#FACC15")
	}

	bar := lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("█", done))
	pad := dimStyle.Render(strings.Repeat("░", rem))
	return "[" + bar + pad + "]"
}

func fallbackStr(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func copyToClipboard(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "windows":
		cmd = exec.Command("cmd", "/c", "clip")
	default:
		switch {
		case hasBinary("wl-copy"):
			cmd = exec.Command("wl-copy")
		case hasBinary("xclip"):
			cmd = exec.Command("xclip", "-selection", "clipboard")
		case hasBinary("xsel"):
			cmd = exec.Command("xsel", "--clipboard", "--input")
		default:
			return fmt.Errorf("no clipboard utility (wl-copy/xclip/xsel) found")
		}
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

func hasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
