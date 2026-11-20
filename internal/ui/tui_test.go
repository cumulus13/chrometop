package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
