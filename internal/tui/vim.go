package tui

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/textarea"
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

// vimComposerKey processes a key for the chat composer textarea. It is a thin
// wrapper around vimTextareaKey so the chat composer and the compose modal can
// share one vim engine.
func (m *Model) vimComposerKey(msg tea.KeyMsg) bool {
	if !m.cfg.VimMode {
		return false
	}
	return m.vimTextareaKey(msg, &m.input, &m.vimComposer)
}

// vimTextareaKey applies vim behaviour to an arbitrary textarea. ta and mode
// are mutated in place so the same engine drives both the chat composer and
// the compose-modal body field. It returns whether the key was consumed.
//
// Behaviour:
//   - In INSERT, only Esc is consumed (switch to NORMAL). Everything else
//     falls through to the textarea so typing feels natural.
//   - In NORMAL, vim motions/edits are translated into textarea operations.
func (m *Model) vimTextareaKey(msg tea.KeyMsg, ta *textarea.Model, mode *vimMode) bool {
	key := msg.String()

	if *mode == vimModeInsert {
		if key == "esc" {
			*mode = vimModeNormal
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
			m.vimDeleteLine(ta, true)
		case "yy":
			m.vimYankLine(ta)
			m.toast = "line yanked"
		case "gg":
			vimGotoTop(ta)
		case "cc":
			m.vimDeleteLine(ta, false)
			*mode = vimModeInsert
		default:
			// operator + charwise motion: dw de db d$ d0 dl dh and the
			// c/y variants. Linewise combos above are matched first.
			if (pending == "d" || pending == "c" || pending == "y") && vimIsMotion(key) {
				m.vimOperatorMotion(ta, pending, key)
				if pending == "c" {
					*mode = vimModeInsert
				}
			}
		}
		return true
	}

	switch key {
	// --- Movement ---
	case "h", "left":
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyLeft})
	case "l", "right":
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyRight})
	case "j", "down":
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyDown})
	case "k", "up":
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyUp})
	case "w":
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyRight, Alt: true})
	case "b":
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyLeft, Alt: true})
	case "e":
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyRight, Alt: true})
	case "0", "home":
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyHome})
	case "$", "end":
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyEnd})
	case "g":
		m.vimPending = "g"
	case "G":
		vimGotoBottom(ta)

	// --- Mode switches ---
	case "i":
		*mode = vimModeInsert
	case "I":
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyHome})
		*mode = vimModeInsert
	case "a":
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyRight})
		*mode = vimModeInsert
	case "A":
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyEnd})
		*mode = vimModeInsert
	case "o":
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyEnd})
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyEnter})
		*mode = vimModeInsert
	case "O":
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyHome})
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyEnter})
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyUp})
		*mode = vimModeInsert

	// --- Edits ---
	case "x":
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyDelete})
	case "X":
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyBackspace})
	case "d":
		m.vimPending = "d"
	case "y":
		m.vimPending = "y"
	case "c":
		m.vimPending = "c"
	case "D":
		m.vimOperatorMotion(ta, "d", "$")
	case "C":
		m.vimOperatorMotion(ta, "c", "$")
		*mode = vimModeInsert
	case "Y":
		m.vimYankLine(ta)
		m.toast = "line yanked"
	case "p":
		m.vimPaste(ta, true)
	case "P":
		m.vimPaste(ta, false)
	case "u":
		m.toast = "undo not supported"
	default:
		// swallow stray keys so we never accidentally type in normal mode.
	}
	return true
}

// sendToInput forwards a synthesized key to a textarea so we can reuse its
// movement and editing primitives without depending on unexported methods.
func sendToInput(ta *textarea.Model, msg tea.KeyMsg) {
	*ta, _ = ta.Update(msg)
}

func vimLineBounds(ta *textarea.Model) (start, end, lineIdx int, lines []string) {
	value := ta.Value()
	lines = strings.Split(value, "\n")
	lineIdx = ta.Line()
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

func (m *Model) vimYankLine(ta *textarea.Model) {
	_, _, idx, lines := vimLineBounds(ta)
	if idx < 0 || idx >= len(lines) {
		return
	}
	m.vimRegister = lines[idx] + "\n"
	m.vimRegisterLine = true
}

func (m *Model) vimDeleteLine(ta *textarea.Model, yank bool) {
	_, _, idx, lines := vimLineBounds(ta)
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
	ta.SetValue(strings.Join(next, "\n"))
	if idx >= len(next) {
		idx = len(next) - 1
	}
	vimGotoLine(ta, idx)
}

func (m *Model) vimPaste(ta *textarea.Model, after bool) {
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
		_, _, idx, lines := vimLineBounds(ta)
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
		ta.SetValue(strings.Join(next, "\n"))
		vimGotoLine(ta, insertAt)
		return
	}
	if after {
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyRight})
	}
	for _, r := range text {
		if r == '\n' {
			sendToInput(ta, tea.KeyMsg{Type: tea.KeyEnter})
			continue
		}
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func vimGotoTop(ta *textarea.Model) {
	lines := strings.Count(ta.Value(), "\n") + 1
	for range lines {
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyUp})
	}
	sendToInput(ta, tea.KeyMsg{Type: tea.KeyHome})
}

func vimGotoBottom(ta *textarea.Model) {
	lines := strings.Count(ta.Value(), "\n") + 1
	for range lines {
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyDown})
	}
	sendToInput(ta, tea.KeyMsg{Type: tea.KeyEnd})
}

func vimGotoLine(ta *textarea.Model, target int) {
	current := ta.Line()
	delta := target - current
	for delta > 0 {
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyDown})
		delta--
	}
	for delta < 0 {
		sendToInput(ta, tea.KeyMsg{Type: tea.KeyUp})
		delta++
	}
	sendToInput(ta, tea.KeyMsg{Type: tea.KeyHome})
}

// vimCursorCol returns the cursor's rune offset within its logical line.
// textarea exposes this only indirectly: for the row the cursor sits on,
// StartColumn + ColumnOffset reconstructs the logical column.
func vimCursorCol(ta *textarea.Model) int {
	li := ta.LineInfo()
	return li.StartColumn + li.ColumnOffset
}

// vimIsMotion reports whether key is a charwise motion an operator (d/c/y)
// can act on.
func vimIsMotion(key string) bool {
	switch key {
	case "w", "e", "b", "$", "0", "h", "l":
		return true
	}
	return false
}

// vimNextWordStart returns the column of the next WORD start at or after col.
// WORDs are whitespace-delimited, matching the w motion's Alt+Right behaviour.
func vimNextWordStart(line []rune, col int) int {
	n := len(line)
	i := col
	if i >= n {
		return n
	}
	for i < n && !unicode.IsSpace(line[i]) {
		i++
	}
	for i < n && unicode.IsSpace(line[i]) {
		i++
	}
	return i
}

// vimWordEnd returns the column of the last character of the WORD reached by
// the e motion from col.
func vimWordEnd(line []rune, col int) int {
	n := len(line)
	i := col + 1
	for i < n && unicode.IsSpace(line[i]) {
		i++
	}
	for i+1 < n && !unicode.IsSpace(line[i+1]) {
		i++
	}
	if i >= n {
		i = n - 1
	}
	return i
}

// vimPrevWordStart returns the column of the WORD start reached by the b
// motion from col.
func vimPrevWordStart(line []rune, col int) int {
	i := col
	if i > len(line) {
		i = len(line)
	}
	i--
	for i > 0 && unicode.IsSpace(line[i]) {
		i--
	}
	for i > 0 && !unicode.IsSpace(line[i-1]) {
		i--
	}
	if i < 0 {
		i = 0
	}
	return i
}

// vimMotionRange returns the half-open rune range [lo, hi) an operator should
// act on for motion, starting at cursor column col within line. ok is false
// when motion is not a charwise motion.
func vimMotionRange(line []rune, col int, motion string) (lo, hi int, ok bool) {
	n := len(line)
	if col > n {
		col = n
	}
	switch motion {
	case "w":
		return col, vimNextWordStart(line, col), true
	case "e":
		return col, vimWordEnd(line, col) + 1, true
	case "b":
		return vimPrevWordStart(line, col), col, true
	case "$":
		return col, n, true
	case "0":
		return 0, col, true
	case "l":
		return col, min(col+1, n), true
	case "h":
		return max(col-1, 0), col, true
	}
	return 0, 0, false
}

// vimOperatorMotion applies an operator (d/c/y) over a charwise motion within
// the cursor's current line. Linewise combos (dd/cc/yy) and goto (gg) are
// handled by the caller; this covers dw, de, db, d$, d0, cw, yw, etc. The
// deleted/yanked text lands in the charwise register.
func (m *Model) vimOperatorMotion(ta *textarea.Model, op, motion string) {
	_, _, lineIdx, lines := vimLineBounds(ta)
	if lineIdx < 0 || lineIdx >= len(lines) {
		return
	}
	// cw behaves like ce in vim: it stops at the word end rather than the
	// next word's start.
	if op == "c" && motion == "w" {
		motion = "e"
	}
	runes := []rune(lines[lineIdx])
	lo, hi, ok := vimMotionRange(runes, vimCursorCol(ta), motion)
	if !ok || lo >= hi {
		return
	}
	m.vimRegister = string(runes[lo:hi])
	m.vimRegisterLine = false
	if op == "y" {
		m.toast = "yanked"
		return
	}
	next := append([]rune{}, runes[:lo]...)
	next = append(next, runes[hi:]...)
	lines[lineIdx] = string(next)
	ta.SetValue(strings.Join(lines, "\n"))
	vimGotoLine(ta, lineIdx)
	ta.SetCursor(lo)
}
