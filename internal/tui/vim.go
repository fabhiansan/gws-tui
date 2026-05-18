package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type vimMode int

const (
	vimModeInsert vimMode = iota
	vimModeNormal
)

func (v vimMode) String() string {
	switch v {
	case vimModeNormal:
		return "NORMAL"
	default:
		return "INSERT"
	}
}

// vimComposerKey processes a key when the composer (textarea) is focused and
// vim mode is enabled. It returns whether the key was consumed and an optional
// command for follow-up work (currently only used for transient toasts).
//
// Behaviour:
//   - In INSERT, only Esc is consumed (switch to NORMAL). Everything else
//     falls through to the textarea so typing feels natural.
//   - In NORMAL, vim motions/edits are translated into textarea operations.
func (m *Model) vimComposerKey(msg tea.KeyMsg) bool {
	if !m.cfg.VimMode {
		return false
	}
	key := msg.String()

	if m.vimComposer == vimModeInsert {
		if key == "esc" {
			m.vimComposer = vimModeNormal
			m.vimPending = ""
			return true
		}
		return false
	}

	// NORMAL mode below — every key is consumed (vim never types raw chars).
	if m.vimPending != "" {
		pending := m.vimPending
		m.vimPending = ""
		switch pending + key {
		case "dd":
			m.vimDeleteLine(true)
		case "yy":
			m.vimYankLine()
			m.toast = "line yanked"
		case "gg":
			m.vimGotoTop()
		case "cc":
			m.vimDeleteLine(false)
			m.vimComposer = vimModeInsert
		}
		return true
	}

	switch key {
	// --- Movement ---
	case "h", "left":
		m.sendToInput(tea.KeyMsg{Type: tea.KeyLeft})
	case "l", "right":
		m.sendToInput(tea.KeyMsg{Type: tea.KeyRight})
	case "j", "down":
		m.sendToInput(tea.KeyMsg{Type: tea.KeyDown})
	case "k", "up":
		m.sendToInput(tea.KeyMsg{Type: tea.KeyUp})
	case "w":
		m.sendToInput(tea.KeyMsg{Type: tea.KeyRight, Alt: true})
	case "b":
		m.sendToInput(tea.KeyMsg{Type: tea.KeyLeft, Alt: true})
	case "e":
		m.sendToInput(tea.KeyMsg{Type: tea.KeyRight, Alt: true})
	case "0", "home":
		m.sendToInput(tea.KeyMsg{Type: tea.KeyHome})
	case "$", "end":
		m.sendToInput(tea.KeyMsg{Type: tea.KeyEnd})
	case "g":
		m.vimPending = "g"
	case "G":
		m.vimGotoBottom()

	// --- Mode switches ---
	case "i":
		m.vimComposer = vimModeInsert
	case "I":
		m.sendToInput(tea.KeyMsg{Type: tea.KeyHome})
		m.vimComposer = vimModeInsert
	case "a":
		m.sendToInput(tea.KeyMsg{Type: tea.KeyRight})
		m.vimComposer = vimModeInsert
	case "A":
		m.sendToInput(tea.KeyMsg{Type: tea.KeyEnd})
		m.vimComposer = vimModeInsert
	case "o":
		m.sendToInput(tea.KeyMsg{Type: tea.KeyEnd})
		m.sendToInput(tea.KeyMsg{Type: tea.KeyEnter})
		m.vimComposer = vimModeInsert
	case "O":
		m.sendToInput(tea.KeyMsg{Type: tea.KeyHome})
		m.sendToInput(tea.KeyMsg{Type: tea.KeyEnter})
		m.sendToInput(tea.KeyMsg{Type: tea.KeyUp})
		m.vimComposer = vimModeInsert

	// --- Edits ---
	case "x":
		m.sendToInput(tea.KeyMsg{Type: tea.KeyDelete})
	case "X":
		m.sendToInput(tea.KeyMsg{Type: tea.KeyBackspace})
	case "d":
		m.vimPending = "d"
	case "y":
		m.vimPending = "y"
	case "c":
		m.vimPending = "c"
	case "D":
		m.toast = "D not supported (use $ then x)"
	case "Y":
		m.vimYankLine()
		m.toast = "line yanked"
	case "p":
		m.vimPaste(true)
	case "P":
		m.vimPaste(false)
	case "u":
		m.toast = "undo not supported"
	default:
		// swallow stray keys so we never accidentally type in normal mode.
	}
	return true
}

// sendToInput forwards a synthesized key to the textarea so we can reuse its
// movement and editing primitives without depending on unexported methods.
func (m *Model) sendToInput(msg tea.KeyMsg) {
	m.input, _ = m.input.Update(msg)
}

func (m *Model) vimLineBounds() (start, end, lineIdx int, lines []string) {
	value := m.input.Value()
	lines = strings.Split(value, "\n")
	lineIdx = m.input.Line()
	if lineIdx < 0 {
		lineIdx = 0
	}
	if lineIdx >= len(lines) {
		lineIdx = len(lines) - 1
	}
	start = 0
	for i := 0; i < lineIdx; i++ {
		start += len(lines[i]) + 1
	}
	end = start + len(lines[lineIdx])
	return
}

func (m *Model) vimYankLine() {
	_, _, idx, lines := m.vimLineBounds()
	if idx < 0 || idx >= len(lines) {
		return
	}
	m.vimRegister = lines[idx] + "\n"
	m.vimRegisterLine = true
}

func (m *Model) vimDeleteLine(yank bool) {
	_, _, idx, lines := m.vimLineBounds()
	if idx < 0 || idx >= len(lines) {
		return
	}
	if yank {
		m.vimRegister = lines[idx] + "\n"
		m.vimRegisterLine = true
	}
	next := append([]string{}, lines[:idx]...)
	next = append(next, lines[idx+1:]...)
	if len(next) == 0 {
		next = []string{""}
	}
	m.input.SetValue(strings.Join(next, "\n"))
	if idx >= len(next) {
		idx = len(next) - 1
	}
	m.vimGotoLine(idx)
}


func (m *Model) vimPaste(after bool) {
	text := m.vimRegister
	if text == "" {
		clip, err := pasteText()
		if err != nil || clip == "" {
			m.toast = "clipboard empty"
			return
		}
		text = clip
	}
	if m.vimRegisterLine && strings.HasSuffix(text, "\n") {
		_, _, idx, lines := m.vimLineBounds()
		line := strings.TrimRight(text, "\n")
		insertAt := idx
		if after {
			insertAt = idx + 1
		}
		if insertAt > len(lines) {
			insertAt = len(lines)
		}
		next := append([]string{}, lines[:insertAt]...)
		next = append(next, line)
		next = append(next, lines[insertAt:]...)
		m.input.SetValue(strings.Join(next, "\n"))
		m.vimGotoLine(insertAt)
		return
	}
	if after {
		m.sendToInput(tea.KeyMsg{Type: tea.KeyRight})
	}
	for _, r := range text {
		if r == '\n' {
			m.sendToInput(tea.KeyMsg{Type: tea.KeyEnter})
			continue
		}
		m.sendToInput(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func (m *Model) vimGotoTop() {
	lines := strings.Count(m.input.Value(), "\n") + 1
	for i := 0; i < lines; i++ {
		m.sendToInput(tea.KeyMsg{Type: tea.KeyUp})
	}
	m.sendToInput(tea.KeyMsg{Type: tea.KeyHome})
}

func (m *Model) vimGotoBottom() {
	lines := strings.Count(m.input.Value(), "\n") + 1
	for i := 0; i < lines; i++ {
		m.sendToInput(tea.KeyMsg{Type: tea.KeyDown})
	}
	m.sendToInput(tea.KeyMsg{Type: tea.KeyEnd})
}

func (m *Model) vimGotoLine(target int) {
	current := m.input.Line()
	delta := target - current
	for delta > 0 {
		m.sendToInput(tea.KeyMsg{Type: tea.KeyDown})
		delta--
	}
	for delta < 0 {
		m.sendToInput(tea.KeyMsg{Type: tea.KeyUp})
		delta++
	}
	m.sendToInput(tea.KeyMsg{Type: tea.KeyHome})
}
