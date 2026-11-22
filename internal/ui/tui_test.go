package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/cumulus13/chrometop/internal/hub"
	"github.com/cumulus13/chrometop/internal/protocol"
)

func TestFilterAndSortOrderToggleInTUI(t *testing.T) {
	h := hub.New("", nil)
	h.UpdateDirectState(true, "127.0.0.1:9222", protocol.Summary{
		Mode:      "CDP",
		TotalTabs: 2,
	}, []protocol.TabInfo{
		{ID: 1, Title: "Alpha", CPUPercent: 5.0, NetBytesPerSec: 1000, EstimatedMB: 40},
		{ID: 2, Title: "Beta", CPUPercent: 25.0, NetBytesPerSec: 5000, EstimatedMB: 80},
	}, "ready")

	m := NewModel(h, "http://127.0.0.1:9222")
	m.width = 140
	m.height = 36

	// Default sort is CPU DESC -> Beta (25%) first
	tabs := m.filteredSortedTabs()
	if len(tabs) != 2 || tabs[0].ID != 2 {
		t.Fatalf("expected tab 2 first on CPU DESC, got %+v", tabs)
	}

	// Press 'o' to toggle ASC -> Alpha (5%) first
	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	m = nextModel.(Model)
	if !m.sortAsc {
		t.Fatal("expected sortAsc=true after pressing 'o'")
	}
	tabs = m.filteredSortedTabs()
	if tabs[0].ID != 1 {
		t.Fatalf("expected tab 1 first on CPU ASC, got %+v", tabs)
	}

	// Press 's' to cycle sort field to NET/S
	nextModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = nextModel.(Model)
	if m.sortBy != sortNet {
		t.Fatalf("expected sortNet after pressing 's', got %v", m.sortBy)
	}

	view := m.View()
	if !strings.Contains(view, "CHROMETOP") || !strings.Contains(view, "NET/S▲") {
		t.Fatalf("expected header with NET/S▲ indicator")
	}
}

// makeTabs builds n tabs with long, emoji-heavy and CJK titles and long URLs so
// that every width-related code path is exercised.
func makeTabs(n int) []protocol.TabInfo {
	tabs := make([]protocol.TabInfo, 0, n)
	for i := 1; i <= n; i++ {
		tabs = append(tabs, protocol.TabInfo{
			ID:             i,
			ProcessID:      1000 + i,
			Title:          fmt.Sprintf("Tab %d 🔥 日本語のとても長いタイトル %s", i, strings.Repeat("title ", 12)),
			URL:            "https://example.com/" + strings.Repeat("path/", 20),
			CPUPercent:     float64(i),
			NetBytesPerSec: float64(i * 1024),
			EstimatedMB:    float64(40 + i),
			JSHeapUsedMB:   float64(i) / 2,
			Pinned:         i%3 == 0,
			Audible:        i%4 == 0,
			Discarded:      i%7 == 0,
		})
	}
	return tabs
}

func TestViewFillsTerminalExactly(t *testing.T) {
	sizes := [][2]int{{60, 12}, {80, 24}, {100, 30}, {120, 40}, {200, 60}}
	tabCounts := []int{0, 1, 5, 60}

	for _, size := range sizes {
		for _, n := range tabCounts {
			h := hub.New("", nil)
			h.UpdateDirectState(true, "http://127.0.0.1:9222", protocol.Summary{Mode: "CDP", TotalTabs: n}, makeTabs(n), "ready")

			m := NewModel(h, "http://127.0.0.1:9222")
			next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m = next.(Model)

			view := m.View()
			if got := lipgloss.Height(view); got != size[1] {
				t.Fatalf("size %dx%d tabs=%d: view height = %d, want %d", size[0], size[1], n, got, size[1])
			}
			for i, line := range strings.Split(view, "\n") {
				if w := lipgloss.Width(line); w > size[0] {
					t.Fatalf("size %dx%d tabs=%d: line %d is %d cells wide, max %d", size[0], size[1], n, i, w, size[0])
				}
			}
			if !strings.Contains(view, "CHROMETOP") {
				t.Fatalf("size %dx%d tabs=%d: top panel missing", size[0], size[1], n)
			}
			if !strings.Contains(view, "Quit") {
				t.Fatalf("size %dx%d tabs=%d: footer hints missing", size[0], size[1], n)
			}
		}
	}
}

func TestFooterIsOnLastRow(t *testing.T) {
	h := hub.New("", nil)
	h.UpdateDirectState(true, "x", protocol.Summary{Mode: "CDP"}, makeTabs(3), "ready")

	m := NewModel(h, "x")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m = next.(Model)

	lines := strings.Split(m.View(), "\n")
	if len(lines) != 40 {
		t.Fatalf("expected 40 lines, got %d", len(lines))
	}
	// With 140 columns every hint fits on one line, so the last row is the hints.
	if !strings.Contains(lines[len(lines)-1], "Quit") {
		t.Fatalf("expected key hints on the last terminal row, got %q", lines[len(lines)-1])
	}
}

func TestVisibleRowsMatchRenderedBody(t *testing.T) {
	h := hub.New("", nil)
	h.UpdateDirectState(true, "x", protocol.Summary{Mode: "CDP"}, makeTabs(100), "ready")

	m := NewModel(h, "x")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = next.(Model)

	want := 30 - topPanelLines - filterLines - headerLines - footerHeight(120)
	if got := m.visibleRows(); got != want {
		t.Fatalf("visibleRows = %d, want %d", got, want)
	}
}

func TestSelectionFollowsTabAcrossRefresh(t *testing.T) {
	h := hub.New("", nil)
	h.UpdateDirectState(true, "x", protocol.Summary{Mode: "CDP"}, []protocol.TabInfo{
		{ID: 1, Title: "One", CPUPercent: 50},
		{ID: 2, Title: "Two", CPUPercent: 10},
	}, "")

	m := NewModel(h, "x")
	m.width, m.height = 100, 30

	// Default order is CPU DESC: [1, 2]. Move the cursor to tab 2.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = next.(Model)
	if sel, _ := m.currentTab(); sel.ID != 2 {
		t.Fatalf("expected tab 2 selected, got %d", sel.ID)
	}

	// Tab 2 becomes the busiest and moves to the top of the list.
	h.UpdateDirectState(true, "x", protocol.Summary{Mode: "CDP"}, []protocol.TabInfo{
		{ID: 1, Title: "One", CPUPercent: 5},
		{ID: 2, Title: "Two", CPUPercent: 90},
	}, "")
	next, _ = m.Update(hubChangedMsg{})
	m = next.(Model)

	if m.cursor != 0 {
		t.Fatalf("expected cursor to follow tab 2 to row 0, got row %d", m.cursor)
	}
	if sel, _ := m.currentTab(); sel.ID != 2 {
		t.Fatalf("selection jumped to tab %d", sel.ID)
	}
}

type slowDispatcher struct{ delay time.Duration }

func (d slowDispatcher) Dispatch(protocol.CommandMsg) error {
	time.Sleep(d.delay)
	return nil
}

func TestCommandsDoNotBlockUpdate(t *testing.T) {
	h := hub.New("", nil)
	h.SetDispatcher("CDP", slowDispatcher{delay: 500 * time.Millisecond})
	h.UpdateDirectState(true, "x", protocol.Summary{Mode: "CDP"}, makeTabs(3), "")

	m := NewModel(h, "x")

	start := time.Now()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("Update blocked for %v; backend I/O must run inside a tea.Cmd", elapsed)
	}
	if cmd == nil {
		t.Fatal("expected a command to be returned for the reload action")
	}
}

func TestSingleTickChain(t *testing.T) {
	h := hub.New("", nil)
	m := NewModel(h, "x")

	// A hub change must re-arm the hub listener only, never the clock.
	_, cmd := m.Update(hubChangedMsg{})
	if cmd == nil {
		t.Fatal("expected the model to keep listening for hub changes")
	}

	// A tick re-arms exactly one command (the next tick).
	_, cmd = m.Update(tickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("expected the next tick to be scheduled")
	}
}

func TestSanitizeStripsControlSequences(t *testing.T) {
	got := sanitize("evil\x1b[31m title\nsecond\tline\x07")
	if strings.ContainsAny(got, "\x1b\n\t\x07") {
		t.Fatalf("control characters survived sanitize: %q", got)
	}
}

func TestFilterAcceptsSpaces(t *testing.T) {
	h := hub.New("", nil)
	h.UpdateDirectState(true, "x", protocol.Summary{Mode: "CDP"}, []protocol.TabInfo{
		{ID: 1, Title: "Hello World"},
		{ID: 2, Title: "Other"},
	}, "")

	m := NewModel(h, "x")
	for _, k := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("/")},
		{Type: tea.KeyRunes, Runes: []rune("hello")},
		{Type: tea.KeySpace, Runes: []rune(" ")},
		{Type: tea.KeyRunes, Runes: []rune("world")},
	} {
		next, _ := m.Update(k)
		m = next.(Model)
	}

	if len(m.rows) != 1 || m.rows[0].ID != 1 {
		t.Fatalf("expected only 'Hello World' to match, got %+v", m.rows)
	}
}
