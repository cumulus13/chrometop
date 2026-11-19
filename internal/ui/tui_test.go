package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/cumulus13/chrometop/internal/hub"
	"github.com/cumulus13/chrometop/internal/protocol"
)

func TestFilterAndSortInTUI(t *testing.T) {
	h := hub.New("", nil)
	h.SetStatusBanner("ready")

	m := NewModel(h, "127.0.0.1:9333")
	m.width = 140
	m.height = 36

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = nextModel.(Model)
	if !m.filtering {
		t.Fatal("expected filtering mode to be active after pressing '/'")
	}

	nextModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("github")})
	m = nextModel.(Model)
	nextModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nextModel.(Model)

	if m.filterText != "github" || m.filtering {
		t.Fatalf("unexpected filter state: text=%q filtering=%v", m.filterText, m.filtering)
	}

	view := m.View()
	if !strings.Contains(view, "CHROMETOP") {
		t.Fatalf("expected TUI view to render CHROMETOP header")
	}

	_ = protocol.TabInfo{}
}
