package ui

import (
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/cumulus13/chrometop/internal/hub"
	"github.com/cumulus13/chrometop/internal/protocol"
)

// TickMsg triggers periodic TUI updates.
type TickMsg struct{}

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
type Model struct {
	hub        *hub.Server
	endpoint   string
	width      int
	height     int
	cursor     int
	scroll     int
	sortBy     sortField
	sortAsc    bool
	filtering  bool
	filterText string
	inspecting bool
	banner     string
}

// NewModel initializes a ChromeTop TUI model.
func NewModel(h *hub.Server, endpoint string) Model {
	return Model{
		hub:      h,
		endpoint: endpoint,
		width:    136,
		height:   34,
		sortBy:   sortCPU,
		sortAsc:  false,
	}
}

func (m Model) Init() tea.Cmd {
	return scheduleTick()
}

func scheduleTick() tea.Cmd {
	return tea.Tick(1*time.Second, func(time.Time) tea.Msg {
		return TickMsg{}
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch ev := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = ev.Width
		m.height = ev.Height
		m.clampScroll(len(m.filteredSortedTabs()))
		return m, nil

	case TickMsg:
		return m, scheduleTick()

	case tea.KeyMsg:
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
		if len(m.filterText) > 0 {
			runes := []rune(m.filterText)
			m.filterText = string(runes[:len(runes)-1])
			m.cursor = 0
			m.scroll = 0
		}
	case tea.KeyRunes:
		m.filterText += string(msg.Runes)
		m.cursor = 0
		m.scroll = 0
	}
	return m, nil
}

func (m Model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	list := m.filteredSortedTabs()

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(list)-1 {
			m.cursor++
		}
	case "g", "home":
		m.cursor = 0
		m.scroll = 0
	case "G", "end":
		if len(list) > 0 {
			m.cursor = len(list) - 1
		}
	case "/":
		m.filtering = true
	case "esc":
		m.filterText = ""
		m.cursor = 0
		m.scroll = 0
	case "s":
		m.sortBy = (m.sortBy + 1) % sortFieldCount
	case "o", "S":
		m.sortAsc = !m.sortAsc
	case "i":
		if _, ok := m.currentTab(list); ok {
			m.inspecting = true
		}
	case "enter", "f":
		if t, ok := m.currentTab(list); ok {
			m.send("FOCUS", t)
		}
	case "x", "delete":
		if t, ok := m.currentTab(list); ok {
			m.send("CLOSE", t)
		}
	case "r":
		if t, ok := m.currentTab(list); ok {
			m.send("RELOAD", t)
		}
	case "d":
		if t, ok := m.currentTab(list); ok {
			m.send("DISCARD", t)
		}
	case "m":
		if t, ok := m.currentTab(list); ok {
			m.send("MUTE", t)
		}
	case "p":
		if t, ok := m.currentTab(list); ok {
			m.send("PIN", t)
		}
	case "c":
		if t, ok := m.currentTab(list); ok {
			m.send("DUPLICATE", t)
		}
	case "n":
		m.sendURL("NEW_TAB", "https://example.com")
	case "y":
		if t, ok := m.currentTab(list); ok {
			if err := copyToClipboard(t.URL); err != nil {
				m.banner = fmt.Sprintf("⚠️ Clipboard error: %v", err)
			} else {
				m.banner = fmt.Sprintf("📋 Copied URL for tab %d", t.ID)
			}
		}
	case "R":
		m.sendURL("SYNC", "")
	}

	m.clampScroll(len(list))
	return m, nil
}

func (m *Model) send(action string, t protocol.TabInfo) {
	cmd := protocol.CommandMsg{
		Type:      "COMMAND",
		Action:    action,
		TabID:     t.ID,
		TargetID:  t.TargetID,
		WindowID:  t.WindowID,
		RequestID: fmt.Sprintf("cmd-%d", time.Now().UnixNano()),
	}
	if err := m.hub.SendCommand(cmd); err != nil {
		m.banner = fmt.Sprintf("⚠️ %s", err.Error())
		return
	}
	m.banner = fmt.Sprintf("🚀 Dispatched %s → tab %d", action, t.ID)
}

func (m *Model) sendURL(action, targetURL string) {
	cmd := protocol.CommandMsg{
		Type:      "COMMAND",
		Action:    action,
		URL:       targetURL,
		RequestID: fmt.Sprintf("cmd-%d", time.Now().UnixNano()),
	}
	if err := m.hub.SendCommand(cmd); err != nil {
		m.banner = fmt.Sprintf("⚠️ %s", err.Error())
		return
	}
	m.banner = fmt.Sprintf("🚀 Dispatched %s", action)
}

func (m Model) currentTab(tabs []protocol.TabInfo) (protocol.TabInfo, bool) {
	if len(tabs) == 0 || m.cursor < 0 || m.cursor >= len(tabs) {
		return protocol.TabInfo{}, false
	}
	return tabs[m.cursor], true
}

func (m *Model) clampScroll(total int) {
	if total == 0 {
		m.cursor = 0
		m.scroll = 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= total {
		m.cursor = total - 1
	}
	visible := m.visibleRows()
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	} else if m.cursor >= m.scroll+visible {
		m.scroll = m.cursor - visible + 1
	}
}

func (m Model) visibleRows() int {
	rows := m.height - 12
	if rows < 4 {
		return 4
	}
	return rows
}

func (m Model) filteredSortedTabs() []protocol.TabInfo {
	snap := m.hub.Snapshot()
	needle := strings.ToLower(strings.TrimSpace(m.filterText))

	out := make([]protocol.TabInfo, 0, len(snap.Tabs))
	for _, t := range snap.Tabs {
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

func (m Model) View() string {
	snap := m.hub.Snapshot()
	tabs := m.filteredSortedTabs()
	w := maxVal(72, m.width)

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
		m.endpoint,
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

	topPanel := panelStyle.Width(w - 2).Render(line1 + "\n" + line2)

	if m.inspecting {
		if sel, ok := m.currentTab(tabs); ok {
			detail := strings.Join([]string{
				fmt.Sprintf("🔎 TAB INSPECTOR — ID %d (Window %d, PID %d, Target %s)", sel.ID, sel.WindowID, sel.ProcessID, fallbackStr(sel.TargetID, "n/a")),
				fmt.Sprintf("📌 Title     : %s", sel.Title),
				fmt.Sprintf("🔗 URL       : %s", sel.URL),
				fmt.Sprintf("⚡ Status    : %s | Active=%v | Pinned=%v | Discarded=%v", sel.Status, sel.Active, sel.Pinned, sel.Discarded),
				fmt.Sprintf("🔥 CPU       : %.1f%%", sel.CPUPercent),
				fmt.Sprintf("🌐 Network   : %s/s (Total transferred: %s)", formatBytes(sel.NetBytesPerSec), formatBytes(float64(sel.NetTotalBytes))),
				fmt.Sprintf("🧠 JS Heap   : %.2f MB / %.2f MB used/total | DOM Nodes: %d", sel.JSHeapUsedMB, sel.JSHeapTotalMB, sel.DOMNodes),
				fmt.Sprintf("📊 Est. Mem  : %.1f MB", sel.EstimatedMB),
				dimStyle.Render("Press [Esc] / [i] / [Enter] to return to table"),
			}, "\n")
			return inspectorStyle.Width(w - 2).Render(detail)
		}
	}

	filterRow := dimStyle.Render("🔎 Press '/' to filter  |  [s] Cycle Sort Column  |  [o] Toggle ASC ▲ / DESC ▼")
	if m.filtering || m.filterText != "" {
		caret := ""
		if m.filtering {
			caret = "▌"
		}
		filterRow = fmt.Sprintf("🔎 Filter: %s%s  [%d/%d tabs]", m.filterText, caret, len(tabs), len(snap.Tabs))
	}

	titleW, urlW := m.calcCols(w)
	hdr := colHeaderStyle.Width(w).Render(
		fmt.Sprintf(
			" %-5s %-6s %-6s %-7s %-9s %-8s %-8s %-*s %-*s",
			m.colLabel(sortTabID, "TAB"),
			m.colLabel(sortPID, "PID"),
			"STATE",
			m.colLabel(sortCPU, "CPU%"),
			m.colLabel(sortNet, "NET/S"),
			m.colLabel(sortJSHeap, "HEAP"),
			m.colLabel(sortFootprint, "MEM"),
			titleW, m.colLabel(sortTitle, "TITLE"),
			urlW, "URL",
		),
	)

	maxRows := m.visibleRows()
	var body []string

	if len(tabs) == 0 {
		msg := fmt.Sprintf("⏳ Waiting for Chrome/Chromium telemetry on %s ...", m.endpoint)
		if snap.Connected {
			msg = "📭 No matching tabs found."
		}
		body = append(body, dimStyle.Render("  "+msg))
	} else {
		end := minVal(len(tabs), m.scroll+maxRows)
		for i := m.scroll; i < end; i++ {
			t := tabs[i]
			formatted := renderTabLine(t, titleW, urlW)
			switch {
			case i == m.cursor:
				body = append(body, rowSelectedStyle.Width(w).Render(formatted))
			case t.Discarded:
				body = append(body, rowDiscardedStyle.Render(formatted))
			default:
				body = append(body, rowNormalStyle.Render(formatted))
			}
		}
	}

	for len(body) < maxRows {
		body = append(body, "")
	}

	eventStatus := snap.LastEventLog
	if m.banner != "" {
		eventStatus = m.banner
	}
	if eventStatus == "" {
		eventStatus = "✨ Ready"
	}

	keys := strings.Join([]string{
		keyCapStyle.Render("↑/↓") + " Nav",
		keyCapStyle.Render("Enter") + " Focus",
		keyCapStyle.Render("i") + " Inspect",
		keyCapStyle.Render("r") + " Reload",
		keyCapStyle.Render("d") + " Discard",
		keyCapStyle.Render("m") + " Mute",
		keyCapStyle.Render("p") + " Pin",
		keyCapStyle.Render("c") + " Dup",
		keyCapStyle.Render("n") + " New",
		keyCapStyle.Render("y") + " Copy",
		keyCapStyle.Render("x") + " Close",
		keyCapStyle.Render("s") + " SortCol",
		keyCapStyle.Render("o") + " Asc/Desc",
		keyCapStyle.Render("/") + " Filter",
		keyCapStyle.Render("q") + " Quit",
	}, " ")

	bottomPanel := footerStyle.Width(w).Render(
		fmt.Sprintf("📡 %s\n%s", eventStatus, keys),
	)

	return strings.Join([]string{
		topPanel,
		filterRow,
		hdr,
		strings.Join(body, "\n"),
		bottomPanel,
	}, "\n")
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
	stateCol := icon + badge

	pidStr := "-"
	if t.ProcessID > 0 {
		pidStr = fmt.Sprintf("%d", t.ProcessID)
	}

	cpuStr := fmt.Sprintf("%5.1f%%", t.CPUPercent)
	netStr := formatBytes(t.NetBytesPerSec) + "/s"

	heapStr := "-"
	if t.JSHeapUsedMB > 0 {
		heapStr = fmt.Sprintf("%.1fM", t.JSHeapUsedMB)
	}

	return fmt.Sprintf(
		" %-5d %-6s %-6s %-7s %-9s %-8s %7.1fM %-*s %-*s",
		t.ID,
		pidStr,
		stateCol,
		cpuStr,
		netStr,
		heapStr,
		t.EstimatedMB,
		titleW, clipText(t.Title, titleW),
		urlW, clipText(t.URL, urlW),
	)
}

func (m Model) calcCols(totalW int) (int, int) {
	overhead := 58
	avail := totalW - overhead
	if avail < 24 {
		return 12, 12
	}
	titleW := (avail * 45) / 100
	urlW := avail - titleW
	return titleW, urlW
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

func clipText(s string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxChars {
		return s
	}
	if maxChars == 1 {
		return "…"
	}
	return string(r[:maxChars-1]) + "…"
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
		if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		} else if _, err := exec.LookPath("xsel"); err == nil {
			cmd = exec.Command("xsel", "--clipboard", "--input")
		} else {
			return fmt.Errorf("no clipboard utility (xclip/xsel) found")
		}
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

func minVal(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxVal(a, b int) int {
	if a > b {
		return a
	}
	return b
}
