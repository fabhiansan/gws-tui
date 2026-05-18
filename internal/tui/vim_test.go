package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

func newVimTestModel(t *testing.T) *Model {
	t.Helper()
	input := textarea.New()
	input.SetWidth(80)
	input.SetHeight(5)
	input.ShowLineNumbers = false
	input.Focus()
	return &Model{
		cfg:         Config{VimMode: true},
		input:       input,
		focusedPane: paneAction,
		vimComposer: vimModeNormal,
	}
}

func sendKey(t *testing.T, m *Model, key string) {
	t.Helper()
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	if !m.vimComposerKey(msg) && m.vimComposer != vimModeInsert {
		t.Fatalf("expected key %q to be handled in normal mode", key)
	}
}

func TestVimEnterInsertAndType(t *testing.T) {
	m := newVimTestModel(t)
	// Press i to enter insert mode
	if !m.vimComposerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")}) {
		t.Fatal("i should be handled in normal mode")
	}
	if m.vimComposer != vimModeInsert {
		t.Fatalf("expected INSERT, got %v", m.vimComposer)
	}
	// In INSERT mode the call should report not handled so caller forwards to textarea.
	if m.vimComposerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}) {
		t.Fatal("INSERT mode should fall through (return false) for typing keys")
	}
}

func TestVimEscFromInsertToNormal(t *testing.T) {
	m := newVimTestModel(t)
	m.vimComposer = vimModeInsert
	if !m.vimComposerKey(tea.KeyMsg{Type: tea.KeyEsc}) {
		t.Fatal("esc in INSERT should be consumed")
	}
	if m.vimComposer != vimModeNormal {
		t.Fatalf("expected NORMAL after esc, got %v", m.vimComposer)
	}
}

func TestVimDeleteLine(t *testing.T) {
	m := newVimTestModel(t)
	m.input.SetValue("alpha\nbeta\ngamma")
	// SetValue leaves cursor at end of buffer; rewind to top, then go down once.
	m.vimGotoTop()
	m.sendToInput(tea.KeyMsg{Type: tea.KeyDown})
	if m.input.Line() != 1 {
		t.Fatalf("setup: expected cursor on line 1, got %d", m.input.Line())
	}
	// dd
	sendKey(t, m, "d")
	sendKey(t, m, "d")
	if got := m.input.Value(); got != "alpha\ngamma" {
		t.Fatalf("dd should remove middle line, got %q", got)
	}
	if m.vimRegister != "beta\n" || !m.vimRegisterLine {
		t.Fatalf("dd should yank line into register, got %q linewise=%v", m.vimRegister, m.vimRegisterLine)
	}
}

func TestVimYankAndPasteLine(t *testing.T) {
	m := newVimTestModel(t)
	m.input.SetValue("alpha\nbeta")
	m.vimGotoTop()
	if m.input.Line() != 0 {
		t.Fatalf("setup: expected line 0, got %d", m.input.Line())
	}
	// yy
	sendKey(t, m, "y")
	sendKey(t, m, "y")
	if m.vimRegister != "alpha\n" {
		t.Fatalf("yy register: got %q", m.vimRegister)
	}
	// p — paste after current line (line 0 -> insert at line 1)
	sendKey(t, m, "p")
	got := m.input.Value()
	want := "alpha\nalpha\nbeta"
	if got != want {
		t.Fatalf("p result: got %q want %q", got, want)
	}
}

func TestVimGotoTopBottom(t *testing.T) {
	m := newVimTestModel(t)
	m.input.SetValue("one\ntwo\nthree")
	m.sendToInput(tea.KeyMsg{Type: tea.KeyEnd})
	// G — bottom
	sendKey(t, m, "G")
	if m.input.Line() != 2 {
		t.Fatalf("G should move to last line, got %d", m.input.Line())
	}
	// gg — top
	sendKey(t, m, "g")
	sendKey(t, m, "g")
	if m.input.Line() != 0 {
		t.Fatalf("gg should move to first line, got %d", m.input.Line())
	}
}

func TestVimDeleteChar(t *testing.T) {
	m := newVimTestModel(t)
	m.input.SetValue("hello")
	m.vimGotoTop()
	sendKey(t, m, "x")
	if got := m.input.Value(); got != "ello" {
		t.Fatalf("x should delete forward char, got %q", got)
	}
}

func TestVimModeDisabledFallsThrough(t *testing.T) {
	m := newVimTestModel(t)
	m.cfg.VimMode = false
	if m.vimComposerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")}) {
		t.Fatal("disabled vim mode should return false")
	}
}

func TestVimActionTitleShowsMode(t *testing.T) {
	m := newVimTestModel(t)
	m.feature = FeatureChat
	got := m.actionTitle()
	if !strings.Contains(got, "NORMAL") {
		t.Fatalf("actionTitle should include mode label: %q", got)
	}
	m.vimComposer = vimModeInsert
	if !strings.Contains(m.actionTitle(), "INSERT") {
		t.Fatalf("actionTitle should reflect INSERT, got %q", m.actionTitle())
	}
}
