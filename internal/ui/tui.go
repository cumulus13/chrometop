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

type sortMode int

const (
	sortFootprint sortMode = iota
	sortJSHeap
	sortTabID
	sortTitle
)

func (m sortMode) label() string {
	switch m {
	case sortFootprint:
		return "EST-MB"
	case sortJSHeap:
		return "JS-HEAP"
	case sortTabID:
		return "TAB-ID"
	case sortTitle:
		return "TITLE"
	default:
		return "EST-MB"
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
	bindAddr   string
	width      int
	height     int
	cursor     int
	scroll     int
	sort       sortMode
	filtering  bool
	filterText string
	inspecting bool
	banner     string
}

// NewModel initializes a ChromeTop TUI model.
func NewModel(h *hub.Server, bindAddr string) Model {
	return Model{
		hub:      h,
		bindAddr: bindAddr,
		width:    130,
		height:   34,
		sort:     sortFootprint,
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
		m.sort = (m.sort + 1) % 4
	case "i":
		if _, ok := m.currentTab(list); ok {
			m.inspecting = true
		}
	case "enter", "f":
		if t, ok := m.currentTab(list); ok {
			m.send("FOCUS", t.ID, t.WindowID, "")
		}
	case "x", "delete":
		if t, ok := m.currentTab(list); ok {
			m.send("CLOSE", t.ID, t.WindowID, "")
		}
	case "r":
		if t, ok := m.currentTab(list); ok {
			m.send("RELOAD", t.ID, t.WindowID, "")
		}
	case "d":
		if t, ok := m.currentTab(list); ok {
			m.send("DISCARD", t.ID, t.WindowID, "")
		}
	case "m":
		if t, ok := m.currentTab(list); ok {
			m.send("MUTE", t.ID, t.WindowID, "")
		}
	case "p":
		if t, ok := m.currentTab(list); ok {
			m.send("PIN", t.ID, t.WindowID, "")
		}
	case "c":
		if t, ok := m.currentTab(list); ok {
			m.send("DUPLICATE", t.ID, t.WindowID, "")
		}
	case "n":
		m.send("NEW_TAB", 0, 0, "https://example.com")
	case "y":
		if t, ok := m.currentTab(list); ok {
			if err := copyToClipboard(t.URL); err != nil {
				m.banner = fmt.Sprintf("⚠️ Clipboard error: %v", err)
			} else {
				m.banner = fmt.Sprintf("📋 Copied URL for tab %d", t.ID)
			}
		}
	case "R":
		m.send("SYNC", 0, 0, "")
	}

	m.clampScroll(len(list))
	return m, nil
}

func (m *Model) send(action string, tabID, winID int, url string) {
	cmd := protocol.CommandMsg{
		Type:      "COMMAND",
		Action:    action,
		TabID:     tabID,
		WindowID:  winID,
		URL:       url,
		RequestID: fmt.Sprintf("cmd-%d", time.Now().UnixNano()),
	}
	if err := m.hub.SendCommand(cmd); err != nil {
		m.banner = fmt.Sprintf("⚠️ %s", err.Error())
		return
	}
	m.banner = fmt.Sprintf("🚀 Dispatched %s → tab %d", action, tabID)
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
		switch m.sort {
		case sortFootprint:
			if out[i].EstimatedMB != out[j].EstimatedMB {
				return out[i].EstimatedMB > out[j].EstimatedMB
			}
			return out[i].ID < out[j].ID
		case sortJSHeap:
			if out[i].JSHeapUsedMB != out[j].JSHeapUsedMB {
				return out[i].JSHeapUsedMB > out[j].JSHeapUsedMB
			}
			return out[i].ID < out[j].ID
		case sortTitle:
			return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
		default:
			return out[i].ID < out[j].ID
		}
	})

	return out
}

func (m Model) View() string {
	snap := m.hub.Snapshot()
	tabs := m.filteredSortedTabs()
	w := maxVal(60, m.width)

	connBadge := offlineStyle.Render("🔴 OFFLINE")
	if snap.Connected {
		connBadge = onlineStyle.Render("🟢 ONLINE")
	}

	meter := renderBar(snap.Summary.EstimatedTotal, 2048, 18)
	syncTime := "never"
	if !snap.LastSync.IsZero() {
		syncTime = snap.LastSync.Format("15:04:05")
	}

	line1 := fmt.Sprintf(
		"%s %s  🔌 ws://%s/ws  |  🔀 Sort: %s  |  📑 Tabs: %d  🪟 Win: %d  🔊 %d  💤 %d",
		brandStyle.Render("⚡ CHROMETOP"),
		connBadge,
		m.bindAddr,
		m.sort.label(),
		snap.Summary.TotalTabs,
		snap.Summary.TotalWindows,
		snap.Summary.AudibleTabs,
		snap.Summary.DiscardedTabs,
	)

	line2 := fmt.Sprintf(
		"📊 Footprint: %s %.1f MB  |  🧠 JS Heap: %.1f MB  |  🕒 Sync: %s",
		meter,
		snap.Summary.EstimatedTotal,
		snap.Summary.TotalJSHeapMB,
		syncTime,
	)

	topPanel := panelStyle.Width(w - 2).Render(line1 + "\n" + line2)

	if m.inspecting {
		if sel, ok := m.currentTab(tabs); ok {
			detail := strings.Join([]string{
				fmt.Sprintf("🔎 TAB INSPECTOR — ID %d (Window %d, Index %d)", sel.ID, sel.WindowID, sel.Index),
				fmt.Sprintf("📌 Title     : %s", sel.Title),
				fmt.Sprintf("🔗 URL       : %s", sel.URL),
				fmt.Sprintf("⚡ Status    : %s | Active=%v | Pinned=%v | Discarded=%v", sel.Status, sel.Active, sel.Pinned, sel.Discarded),
				fmt.Sprintf("🔊 Audio     : Audible=%v | Muted=%v", sel.Audible, sel.Muted),
				fmt.Sprintf("🧠 JS Heap   : %.2f MB / %.2f MB used/total | DOM Nodes: %d", sel.JSHeapUsedMB, sel.JSHeapTotalMB, sel.DOMNodes),
				fmt.Sprintf("📊 Est. Foot : %.1f MB", sel.EstimatedMB),
				dimStyle.Render("Press [Esc] / [i] / [Enter] to return to table"),
			}, "\n")
			return inspectorStyle.Width(w - 2).Render(detail)
		}
	}

	filterRow := dimStyle.Render("🔎 Press '/' to filter by title or URL")
	if m.filtering || m.filterText != "" {
		caret := ""
		if m.filtering {
			caret = "▌"
		}
		filterRow = fmt.Sprintf("🔎 Filter: %s%s  [%d/%d tabs]", m.filterText, caret, len(tabs), len(snap.Tabs))
	}

	titleW, urlW := m.calcCols(w)
	hdr := colHeaderStyle.Width(w).Render(
		fmt.Sprintf(" %-6s %-4s %-7s %-8s %-9s %-*s %-*s", "TAB", "WIN", "STATE", "JS-HEAP", "EST-MB", titleW, "TITLE", urlW, "URL"),
	)

	maxRows := m.visibleRows()
	var body []string

	if len(tabs) == 0 {
		msg := fmt.Sprintf("⏳ Waiting for Chrome extension on ws://%s/ws ...", m.bindAddr)
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
		keyCapStyle.Render("↑/↓") + " Move",
		keyCapStyle.Render("Enter") + " Focus",
		keyCapStyle.Render("i") + " Inspect",
		keyCapStyle.Render("r") + " Reload",
		keyCapStyle.Render("d") + " Discard",
		keyCapStyle.Render("m") + " Mute",
		keyCapStyle.Render("p") + " Pin",
		keyCapStyle.Render("c") + " Dup",
		keyCapStyle.Render("n") + " New",
		keyCapStyle.Render("y") + " CopyURL",
		keyCapStyle.Render("x") + " Close",
		keyCapStyle.Render("s") + " Sort",
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

	heapStr := "-"
	if t.JSHeapUsedMB > 0 {
		heapStr = fmt.Sprintf("%.1fM", t.JSHeapUsedMB)
	}

	return fmt.Sprintf(
		" %-6d %-4d %-7s %-8s %7.1fM  %-*s %-*s",
		t.ID,
		t.WindowID,
		stateCol,
		heapStr,
		t.EstimatedMB,
		titleW, clipText(t.Title, titleW),
		urlW, clipText(t.URL, urlW),
	)
}

func (m Model) calcCols(totalW int) (int, int) {
	overhead := 42
	avail := totalW - overhead
	if avail < 24 {
		return 12, 12
	}
	titleW := (avail * 45) / 100
	urlW := avail - titleW
	return titleW, urlW
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
