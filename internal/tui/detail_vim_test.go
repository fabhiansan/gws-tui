package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/fabhiansan/gws-tui/internal/tui/theme"
)

func newDetailVimModel(t *testing.T, lines int) *Model {
	t.Helper()
	vp := viewport.New(40, 5)
	plain := make([]string, lines)
	for i := range plain {
		plain[i] = "line " + string(rune('A'+i))
	}
	return &Model{
		cfg:             Config{VimMode: true, NoColor: true, NoIcons: true},
		theme:           theme.New("", true),
		detail:          vp,
		focusedPane:     paneDetail,
		detailLines:     plain,
		detailLineCount: lines,
	}
}

func TestDetailVimJKMovesCursor(t *testing.T) {
	m := newDetailVimModel(t, 5)
	next, _, handled := m.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if !handled {
		t.Fatal("j should be handled in detail vim mode")
	}
	if next.detailCursor != 1 {
		t.Fatalf("cursor after j: got %d want 1", next.detailCursor)
	}
	next, _, _ = next.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if next.detailCursor != 0 {
		t.Fatalf("cursor after k: got %d want 0", next.detailCursor)
	}
}

func TestDetailVimTextMotions(t *testing.T) {
	m := newDetailVimModel(t, 1)
	m.detailLines = []string{"hello brave world"}
	m.detailLineCount = 1

	next, _, _ := m.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("w")})
	if next.detailCursor != 0 || next.detailCol != 6 {
		t.Fatalf("w should move to next word: line=%d col=%d", next.detailCursor, next.detailCol)
	}
	next, _, _ = next.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("w")})
	if next.detailCol != 12 {
		t.Fatalf("second w should move to third word, got col=%d", next.detailCol)
	}
	next, _, _ = next.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	if next.detailCol != 6 {
		t.Fatalf("b should move back to previous word, got col=%d", next.detailCol)
	}
	next, _, _ = next.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if next.detailCol != 10 {
		t.Fatalf("e should move to word end, got col=%d", next.detailCol)
	}
}

func TestDetailVimColumnMotions(t *testing.T) {
	m := newDetailVimModel(t, 2)
	m.detailLines = []string{"alpha", "x"}
	m.detailLineCount = 2

	next, _, _ := m.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("$")})
	if next.detailCol != 4 {
		t.Fatalf("$ should move to line end, got col=%d", next.detailCol)
	}
	next, _, _ = next.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if next.detailCursor != 1 || next.detailCol != 0 {
		t.Fatalf("j should clamp column on shorter line: line=%d col=%d", next.detailCursor, next.detailCol)
	}
	next, _, _ = next.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("0")})
	if next.detailCol != 0 {
		t.Fatalf("0 should move to line start, got col=%d", next.detailCol)
	}
}

func TestDetailVimGotoTopBottom(t *testing.T) {
	m := newDetailVimModel(t, 10)
	next, _, _ := m.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	if next.detailCursor != 9 {
		t.Fatalf("G should jump to last line, got %d", next.detailCursor)
	}
	next, _, _ = next.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	if next.detailPending != "g" {
		t.Fatalf("first g should set pending, got %q", next.detailPending)
	}
	next, _, _ = next.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	if next.detailCursor != 0 {
		t.Fatalf("gg should jump to top, got %d", next.detailCursor)
	}
}

func TestDetailVimCursorClamps(t *testing.T) {
	m := newDetailVimModel(t, 3)
	for i := 0; i < 10; i++ {
		next, _, _ := m.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		m = &next
	}
	if m.detailCursor != 2 {
		t.Fatalf("cursor should clamp at last line, got %d", m.detailCursor)
	}
	for i := 0; i < 10; i++ {
		next, _, _ := m.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
		m = &next
	}
	if m.detailCursor != 0 {
		t.Fatalf("cursor should clamp at 0, got %d", m.detailCursor)
	}
}

func TestDetailVimVisualToggle(t *testing.T) {
	m := newDetailVimModel(t, 5)
	m.detailCursor = 2
	next, _, _ := m.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("V")})
	if !next.detailVisual {
		t.Fatal("V should enter visual mode")
	}
	if next.detailAnchor != 2 {
		t.Fatalf("anchor should be at cursor on enter, got %d", next.detailAnchor)
	}
	next, _, _ = next.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("V")})
	if next.detailVisual {
		t.Fatal("V again should exit visual mode")
	}
}

func TestDetailVimYankSingleLine(t *testing.T) {
	m := newDetailVimModel(t, 4)
	m.detailLines = []string{"hello", "world", "foo", "bar"}
	m.detailLineCount = 4
	m.detailCursor = 1
	next, _, _ := m.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if next.vimRegister != "" || next.detailPending != "y" {
		t.Fatalf("first y should wait for a motion, register=%q pending=%q", next.vimRegister, next.detailPending)
	}
	next, _, _ = next.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if next.vimRegister != "world" {
		t.Fatalf("yy should yank current line: got %q", next.vimRegister)
	}
	if !next.vimRegisterLine {
		t.Fatal("yanked line should set vimRegisterLine=true")
	}
}

func TestDetailVimVisualCharYankRange(t *testing.T) {
	m := newDetailVimModel(t, 1)
	m.detailLines = []string{"alpha beta"}
	m.detailLineCount = 1
	next, _, _ := m.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	next, _, _ = next.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	next, _, _ = next.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	next, _, _ = next.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if next.vimRegister != "alp" {
		t.Fatalf("visual char yank: got %q want %q", next.vimRegister, "alp")
	}
	if next.vimRegisterLine {
		t.Fatal("charwise yank should not set vimRegisterLine")
	}
}

func TestDetailVimVisualYankRange(t *testing.T) {
	m := newDetailVimModel(t, 4)
	m.detailLines = []string{"alpha", "beta", "gamma", "delta"}
	m.detailLineCount = 4
	m.detailCursor = 0
	next, _, _ := m.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("V")})
	next, _, _ = next.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	next, _, _ = next.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	next, _, _ = next.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	want := "alpha\nbeta\ngamma"
	if next.vimRegister != want {
		t.Fatalf("visual yank: got %q want %q", next.vimRegister, want)
	}
	if next.detailVisual {
		t.Fatal("yank should exit visual mode")
	}
}

func TestDetailVimVisualYankReverseRange(t *testing.T) {
	m := newDetailVimModel(t, 4)
	m.detailLines = []string{"alpha", "beta", "gamma", "delta"}
	m.detailLineCount = 4
	m.detailCursor = 2
	next, _, _ := m.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("V")})
	next, _, _ = next.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	next, _, _ = next.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	want := "beta\ngamma"
	if next.vimRegister != want {
		t.Fatalf("reverse visual yank: got %q want %q", next.vimRegister, want)
	}
}

func TestDetailVimEscExitsVisualThenFallsThrough(t *testing.T) {
	m := newDetailVimModel(t, 3)
	next, _, _ := m.updateDetailVim(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("V")})
	if !next.detailVisual {
		t.Fatal("setup: V should enter visual")
	}
	next, _, handled := next.updateDetailVim(tea.KeyMsg{Type: tea.KeyEsc})
	if !handled {
		t.Fatal("Esc in visual should be handled")
	}
	if next.detailVisual {
		t.Fatal("Esc should exit visual mode")
	}
	_, _, handled = next.updateDetailVim(tea.KeyMsg{Type: tea.KeyEsc})
	if handled {
		t.Fatal("Esc with no visual should fall through to global handler")
	}
}

func TestStripANSIRemovesColorCodes(t *testing.T) {
	in := "\x1b_Ga=T;payload\x1b\\\x1b[31mhello\x1b[0m \x1b[1;32mworld\x1b[0m" + string(kitty.Placeholder) + string(kitty.Diacritic(0)) + string(kitty.Diacritic(1))
	got := stripANSI(in)
	if got != "hello world" {
		t.Fatalf("stripANSI: got %q want %q", got, "hello world")
	}
}

func TestDecorateDetailMarksCursorLine(t *testing.T) {
	m := newDetailVimModel(t, 3)
	m.detailLines = []string{"a", "b", "c"}
	m.detailLineCount = 3
	m.detailCursor = 1
	decorated, plain := m.decorateDetail("a\nb\nc")
	if len(plain) != 3 || plain[1] != "b" {
		t.Fatalf("plain lines unexpected: %+v", plain)
	}
	lines := strings.Split(decorated, "\n")
	if !strings.Contains(lines[1], "b") {
		t.Fatalf("cursor line should still contain 'b': %q", lines[1])
	}
	if len(lines[1]) <= len(lines[0]) {
		t.Fatalf("cursor line should be padded to full width vs non-cursor: %q vs %q", lines[1], lines[0])
	}
	if !strings.HasPrefix(lines[0], "  ") || !strings.HasPrefix(lines[2], "  ") {
		t.Fatalf("non-cursor lines should keep blank prefix: %q %q", lines[0], lines[2])
	}
}

func TestDecorateDetailMarksVisualSelection(t *testing.T) {
	m := newDetailVimModel(t, 2)
	m.cfg.NoIcons = false
	m.detailLines = []string{"alpha", "beta"}
	m.detailLineCount = 2
	m.detailVisual = true
	m.detailAnchor = 0
	m.detailAnchorCol = 0
	m.detailCursor = 1
	m.detailCol = 3
	decorated, _ := m.decorateDetail("alpha\nbeta")
	lines := strings.Split(decorated, "\n")
	if !strings.HasPrefix(lines[0], "█ ") {
		t.Fatalf("selected non-cursor line should get visual marker, got %q", lines[0])
	}
}
