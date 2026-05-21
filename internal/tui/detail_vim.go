package tui

import (
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
)

type detailPoint struct {
	line int
	col  int
}

func stripANSI(s string) string {
	return stripKittyPlaceholders(ansi.Strip(s))
}

func stripKittyPlaceholders(s string) string {
	var b strings.Builder
	skippingPlaceholder := false
	for _, r := range s {
		if r == kitty.Placeholder {
			skippingPlaceholder = true
			continue
		}
		if skippingPlaceholder && unicode.IsMark(r) {
			continue
		}
		skippingPlaceholder = false
		b.WriteRune(r)
	}
	return b.String()
}

// detailVimEnabled is true when the user has vim mode on. We keep the 2-char
// left margin reserved whether or not the detail pane is focused so the layout
// does not jump when focus changes.
func (m Model) detailVimEnabled() bool {
	return m.cfg.VimMode
}

// detailVimActive is true when key presses should drive the detail cursor —
// vim mode on AND the detail pane has focus.
func (m Model) detailVimActive() bool {
	return m.cfg.VimMode && m.focusedPane == paneDetail
}

// detailTextWidth is the usable width for content fed into the viewport,
// after subtracting the 2-char left margin reserved by decorateDetail in vim
// mode. Wrapping and right-alignment should target this width — using
// m.detail.Width directly causes the rightmost 2 cells to be clipped by the
// viewport's MaxWidth once decorateDetail prepends its margin.
func (m Model) detailTextWidth() int {
	w := m.detail.Width
	if m.detailVimEnabled() {
		w -= 2
	}
	if w < 1 {
		return 1
	}
	return w
}

// decorateDetail prepends a 2-char left-margin marker to every line of the
// styled content. When the detail pane is focused, the cursor line gets a
// distinct marker and (in visual mode) the selected range gets its own. Plain
// (ANSI-stripped) lines are returned alongside so the caller can use them for
// cursor bookkeeping and yanking.
func (m *Model) decorateDetail(content string) (string, []string) {
	styled := strings.Split(content, "\n")
	plain := make([]string, len(styled))
	for i, line := range styled {
		plain[i] = strings.TrimRight(stripANSI(line), " ")
	}
	if !m.detailVimEnabled() {
		return content, plain
	}

	cursor := detailClampPoint(plain, detailPoint{line: m.detailCursor, col: m.detailCol})
	selStart, selEnd := m.detailVisualBoundsFor(plain)

	blank := "  "
	cursorMark := m.accent(m.icon("▎", ">")) + " "
	selectMark := m.accent(m.icon("█", "|")) + " "

	out := make([]string, len(styled))
	for i, line := range styled {
		if !m.detailVimActive() {
			out[i] = blank + line
			continue
		}

		cursorCol := -1
		if i == cursor.line {
			cursorCol = cursor.col
		}
		selectionStart, selectionEnd, selected := m.detailSelectionColumnsFor(plain, i, selStart, selEnd)
		if cursorCol < 0 && !selected {
			out[i] = blank + line
			continue
		}

		mark := blank
		if cursorCol >= 0 {
			mark = cursorMark
		} else if selected {
			mark = selectMark
		}
		if cursorCol >= 0 && !m.detailVisual {
			out[i] = blank + m.renderCursorLine(plain[i], cursorCol)
			continue
		}
		out[i] = mark + m.renderDetailTextLine(plain[i], cursorCol, selectionStart, selectionEnd, selected)
	}
	return strings.Join(out, "\n"), plain
}

func detailLineLen(lines []string, line int) int {
	if line < 0 || line >= len(lines) {
		return 0
	}
	return len([]rune(lines[line]))
}

func detailLineLastCol(lines []string, line int) int {
	n := detailLineLen(lines, line)
	if n <= 0 {
		return 0
	}
	return n - 1
}

func detailClampPoint(lines []string, p detailPoint) detailPoint {
	if len(lines) == 0 {
		return detailPoint{}
	}
	if p.line < 0 {
		p.line = 0
	} else if p.line >= len(lines) {
		p.line = len(lines) - 1
	}
	last := detailLineLastCol(lines, p.line)
	if p.col < 0 {
		p.col = 0
	} else if p.col > last {
		p.col = last
	}
	return p
}

func detailPointLess(a, b detailPoint) bool {
	if a.line != b.line {
		return a.line < b.line
	}
	return a.col < b.col
}

func (m *Model) detailVisualBoundsFor(lines []string) (start, end detailPoint) {
	cursor := detailClampPoint(lines, detailPoint{line: m.detailCursor, col: m.detailCol})
	anchor := detailClampPoint(lines, detailPoint{line: m.detailAnchor, col: m.detailAnchorCol})
	if !m.detailVisual {
		return cursor, cursor
	}
	if detailPointLess(cursor, anchor) {
		return cursor, anchor
	}
	return anchor, cursor
}

func (m *Model) detailSelectionColumnsFor(lines []string, line int, start, end detailPoint) (int, int, bool) {
	if !m.detailVisual || line < start.line || line > end.line {
		return 0, 0, false
	}
	last := detailLineLastCol(lines, line)
	if m.detailVisualLine {
		return 0, last, true
	}
	if start.line == end.line {
		return min(start.col, last), min(end.col, last), true
	}
	if line == start.line {
		return min(start.col, last), last, true
	}
	if line == end.line {
		return 0, min(end.col, last), true
	}
	return 0, last, true
}

// renderCursorLine draws the NORMAL-mode cursor line: a full-width bar in the
// Selected style with the cursor cell highlighted. The bar is measured in
// display columns, not runes — a wide glyph (emoji, CJK, box-drawing/ambiguous
// chars) eats two columns of the budget. Counting runes here would let the bar
// overflow detailTextWidth, which makes lipgloss wrap the line inside the
// viewport and desyncs the whole frame, leaving stale "ghost" cells behind.
func (m Model) renderCursorLine(line string, cursorCol int) string {
	runes := []rune(line)
	width := m.detailTextWidth()
	if strings.TrimSpace(line) == "" {
		return m.detailCursorStyle().Render(" ") + strings.Repeat(" ", max(0, width-1))
	}
	bg := m.theme.Selected
	cursor := m.detailCursorStyle()
	var out strings.Builder
	used := 0 // display columns emitted so far
	for idx := 0; used < width; idx++ {
		cell, cw := " ", 1
		if idx < len(runes) {
			r := runes[idx]
			rw := wideCond.RuneWidth(r)
			if rw < 1 {
				rw = 1 // zero-width runes still take a cell so the budget advances
			}
			// Skip a wide glyph that would spill past the last column; the
			// trailing space keeps the bar exactly `width` columns wide.
			if used+rw <= width {
				cell, cw = string(r), rw
			}
		}
		style := bg
		if idx == cursorCol {
			style = cursor
		}
		out.WriteString(style.Render(cell))
		used += cw
	}
	return out.String()
}

func (m Model) renderDetailTextLine(line string, cursorCol, selectionStart, selectionEnd int, selected bool) string {
	runes := []rune(line)
	cols := len(runes)
	if cols == 0 && (cursorCol == 0 || selected) {
		cols = 1
	}
	var out strings.Builder
	for col := 0; col < cols; col++ {
		cell := " "
		if col < len(runes) {
			cell = string(runes[col])
		}
		switch {
		case col == cursorCol:
			out.WriteString(m.detailCursorStyle().Render(cell))
		case selected && col >= selectionStart && col <= selectionEnd:
			out.WriteString(m.detailSelectionStyle().Render(cell))
		default:
			out.WriteString(cell)
		}
	}
	return out.String()
}

func (m Model) detailSelectionStyle() lipgloss.Style {
	if m.cfg.NoColor {
		return lipgloss.NewStyle().Reverse(true)
	}
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color("#4338CA"))
}

func (m Model) detailCursorStyle() lipgloss.Style {
	if m.cfg.NoColor {
		return lipgloss.NewStyle().Reverse(true).Bold(true)
	}
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("#111827")).
		Background(lipgloss.Color(m.theme.Warn)).
		Bold(true)
}

func (m *Model) detailClampCursor() {
	n := m.detailLineCount
	if n <= 0 {
		m.detailCursor = 0
		m.detailCol = 0
		m.detailAnchor = 0
		m.detailAnchorCol = 0
		m.detailVisual = false
		m.detailVisualLine = false
		return
	}
	if m.detailCursor < 0 {
		m.detailCursor = 0
	} else if m.detailCursor >= n {
		m.detailCursor = n - 1
	}
	m.detailCol = detailClampPoint(m.detailLines, detailPoint{line: m.detailCursor, col: m.detailCol}).col
	if m.detailAnchor < 0 {
		m.detailAnchor = 0
	} else if m.detailAnchor >= n {
		m.detailAnchor = n - 1
	}
	m.detailAnchorCol = detailClampPoint(m.detailLines, detailPoint{line: m.detailAnchor, col: m.detailAnchorCol}).col
}

func (m *Model) detailEnsureCursorVisible() {
	if m.detail.Height <= 0 {
		return
	}
	if m.detailCursor < m.detail.YOffset {
		m.detail.SetYOffset(m.detailCursor)
		return
	}
	if m.detailCursor >= m.detail.YOffset+m.detail.Height {
		m.detail.SetYOffset(m.detailCursor - m.detail.Height + 1)
	}
}

func (m *Model) detailMoveCursor(delta int) {
	if m.detailLineCount <= 0 {
		return
	}
	target := m.detailCursor + delta
	if target < 0 {
		target = 0
	}
	if target >= m.detailLineCount {
		target = m.detailLineCount - 1
	}
	m.detailCursor = target
	m.detailCol = detailClampPoint(m.detailLines, detailPoint{line: m.detailCursor, col: m.detailCol}).col
	m.detailEnsureCursorVisible()
}

func (m *Model) detailMoveColumn(delta int) {
	if m.detailLineCount <= 0 {
		return
	}
	target := m.detailCol + delta
	last := detailLineLastCol(m.detailLines, m.detailCursor)
	if target < 0 {
		target = 0
	} else if target > last {
		target = last
	}
	m.detailCol = target
	m.detailEnsureCursorVisible()
}

func (m *Model) detailGotoLineStart() {
	m.detailCol = 0
	m.detailEnsureCursorVisible()
}

func (m *Model) detailGotoLineEnd() {
	m.detailCol = detailLineLastCol(m.detailLines, m.detailCursor)
	m.detailEnsureCursorVisible()
}

func (m *Model) detailGotoTop() {
	if m.detailLineCount <= 0 {
		return
	}
	m.detailCursor = 0
	m.detailCol = 0
	m.detail.SetYOffset(0)
}

func (m *Model) detailGotoBottom() {
	if m.detailLineCount <= 0 {
		return
	}
	m.detailCursor = m.detailLineCount - 1
	m.detailCol = detailClampPoint(m.detailLines, detailPoint{line: m.detailCursor, col: m.detailCol}).col
	m.detailEnsureCursorVisible()
}

func (m *Model) detailText() string {
	return strings.Join(m.detailLines, "\n")
}

func detailAbsPos(lines []string, p detailPoint) int {
	p = detailClampPoint(lines, p)
	pos := 0
	for i := 0; i < p.line; i++ {
		pos += detailLineLen(lines, i) + 1
	}
	return pos + p.col
}

func detailPointForAbs(lines []string, pos int) detailPoint {
	if len(lines) == 0 {
		return detailPoint{}
	}
	if pos < 0 {
		pos = 0
	}
	offset := 0
	for line := range lines {
		n := detailLineLen(lines, line)
		if pos < offset+n {
			return detailClampPoint(lines, detailPoint{line: line, col: pos - offset})
		}
		if pos == offset+n {
			if line == len(lines)-1 {
				return detailClampPoint(lines, detailPoint{line: line, col: n})
			}
			return detailPoint{line: line + 1, col: 0}
		}
		offset += n + 1
	}
	return detailClampPoint(lines, detailPoint{line: len(lines) - 1, col: detailLineLastCol(lines, len(lines)-1)})
}

func detailTokenRune(r rune) bool {
	return !unicode.IsSpace(r)
}

func (m *Model) detailSetPoint(p detailPoint) {
	p = detailClampPoint(m.detailLines, p)
	m.detailCursor = p.line
	m.detailCol = p.col
	m.detailEnsureCursorVisible()
}

func (m *Model) detailMoveWordForward() {
	text := []rune(m.detailText())
	if len(text) == 0 {
		return
	}
	pos := detailAbsPos(m.detailLines, detailPoint{line: m.detailCursor, col: m.detailCol})
	if pos >= len(text)-1 {
		m.detailSetPoint(detailPointForAbs(m.detailLines, len(text)-1))
		return
	}
	if detailTokenRune(text[pos]) {
		for pos < len(text) && detailTokenRune(text[pos]) {
			pos++
		}
	}
	for pos < len(text) && !detailTokenRune(text[pos]) {
		pos++
	}
	if pos >= len(text) {
		pos = len(text) - 1
	}
	m.detailSetPoint(detailPointForAbs(m.detailLines, pos))
}

func (m *Model) detailMoveWordBackward() {
	text := []rune(m.detailText())
	if len(text) == 0 {
		return
	}
	pos := detailAbsPos(m.detailLines, detailPoint{line: m.detailCursor, col: m.detailCol})
	if pos <= 0 {
		m.detailSetPoint(detailPointForAbs(m.detailLines, 0))
		return
	}
	pos--
	for pos > 0 && !detailTokenRune(text[pos]) {
		pos--
	}
	for pos > 0 && detailTokenRune(text[pos-1]) {
		pos--
	}
	m.detailSetPoint(detailPointForAbs(m.detailLines, pos))
}

func (m *Model) detailMoveWordEnd() {
	text := []rune(m.detailText())
	if len(text) == 0 {
		return
	}
	pos := detailAbsPos(m.detailLines, detailPoint{line: m.detailCursor, col: m.detailCol})
	if pos >= len(text)-1 {
		m.detailSetPoint(detailPointForAbs(m.detailLines, len(text)-1))
		return
	}
	if detailTokenRune(text[pos]) {
		pos++
	}
	for pos < len(text) && !detailTokenRune(text[pos]) {
		pos++
	}
	for pos < len(text)-1 && detailTokenRune(text[pos+1]) {
		pos++
	}
	m.detailSetPoint(detailPointForAbs(m.detailLines, pos))
}

// detailResetCursor is called when the displayed item changes (different chat
// space, mail thread, calendar event, feature switch) so the cursor doesn't
// stay parked on a stale line.
func (m *Model) detailResetCursor() {
	m.detailCursor = 0
	m.detailCol = 0
	m.detailAnchor = 0
	m.detailAnchorCol = 0
	m.detailVisual = false
	m.detailVisualLine = false
	m.detailPending = ""
}

func (m *Model) detailVisualBounds() (start, end int) {
	if !m.detailVisual {
		return m.detailCursor, m.detailCursor
	}
	if m.detailAnchor <= m.detailCursor {
		return m.detailAnchor, m.detailCursor
	}
	return m.detailCursor, m.detailAnchor
}

func (m *Model) detailStartVisual(linewise bool) {
	m.detailVisual = true
	m.detailVisualLine = linewise
	m.detailAnchor = m.detailCursor
	m.detailAnchorCol = m.detailCol
	if linewise {
		m.toast = "-- VISUAL LINE --"
	} else {
		m.toast = "-- VISUAL --"
	}
}

func (m *Model) detailYankLine() {
	if len(m.detailLines) == 0 || m.detailCursor < 0 || m.detailCursor >= len(m.detailLines) {
		m.toast = "nothing to yank"
		return
	}
	m.detailYankText(m.detailLines[m.detailCursor], true, 1)
}

func (m *Model) detailYankText(text string, linewise bool, lines int) {
	if strings.TrimSpace(text) == "" {
		m.toast = "nothing to yank"
		return
	}
	m.vimRegister = text
	m.vimRegisterLine = linewise
	if err := copyText(text); err != nil {
		m.toast = "yank: " + err.Error()
		return
	}
	if lines == 1 {
		m.toast = "selection yanked"
		if linewise {
			m.toast = "line yanked"
		}
		return
	}
	m.toast = fmt.Sprintf("%d lines yanked", lines)
}

func (m *Model) detailYankSelection() {
	if len(m.detailLines) == 0 {
		m.toast = "nothing to yank"
		return
	}
	startPoint, endPoint := m.detailVisualBoundsFor(m.detailLines)
	if m.detailVisualLine {
		text := strings.Join(m.detailLines[startPoint.line:endPoint.line+1], "\n")
		m.detailYankText(text, true, endPoint.line-startPoint.line+1)
		m.detailVisual = false
		m.detailVisualLine = false
		return
	}

	parts := make([]string, 0, endPoint.line-startPoint.line+1)
	for line := startPoint.line; line <= endPoint.line; line++ {
		runes := []rune(m.detailLines[line])
		if len(runes) == 0 {
			parts = append(parts, "")
			continue
		}
		from := 0
		to := len(runes) - 1
		if line == startPoint.line {
			from = min(startPoint.col, to)
		}
		if line == endPoint.line {
			to = min(endPoint.col, to)
		}
		if from > to {
			parts = append(parts, "")
			continue
		}
		parts = append(parts, string(runes[from:to+1]))
	}
	m.detailYankText(strings.Join(parts, "\n"), false, endPoint.line-startPoint.line+1)
	m.detailVisual = false
	m.detailVisualLine = false
}

// updateDetailVim handles vim keys when the detail pane has focus. Returns
// handled=false for any key it doesn't claim, so callers can fall through to
// the global key handling (q, 1-4, r, etc.).
func (m Model) updateDetailVim(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	key := msg.String()

	if m.detailPending == "g" {
		m.detailPending = ""
		if key == "g" {
			m.detailGotoTop()
			return m, nil, true
		}
		// fall through so other keys still work
	}
	if m.detailPending == "y" {
		m.detailPending = ""
		if key == "y" {
			m.detailYankLine()
			return m, nil, true
		}
		if key == "esc" {
			return m, nil, true
		}
		m.toast = "yank motion not supported"
		return m, nil, true
	}

	switch key {
	case "h", "left":
		m.detailMoveColumn(-1)
		return m, nil, true
	case "l", "right":
		m.detailMoveColumn(1)
		return m, nil, true
	case "j", "down":
		m.detailMoveCursor(1)
		return m, nil, true
	case "k", "up":
		m.detailMoveCursor(-1)
		return m, nil, true
	case "w":
		m.detailMoveWordForward()
		return m, nil, true
	case "b":
		m.detailMoveWordBackward()
		return m, nil, true
	case "e":
		m.detailMoveWordEnd()
		return m, nil, true
	case "0", "home":
		m.detailGotoLineStart()
		return m, nil, true
	case "$", "end":
		m.detailGotoLineEnd()
		return m, nil, true
	case "g":
		m.detailPending = "g"
		return m, nil, true
	case "G":
		m.detailGotoBottom()
		return m, nil, true
	case "ctrl+d":
		step := m.detail.Height / 2
		if step < 1 {
			step = 1
		}
		m.detailMoveCursor(step)
		return m, nil, true
	case "ctrl+u":
		step := m.detail.Height / 2
		if step < 1 {
			step = 1
		}
		m.detailMoveCursor(-step)
		return m, nil, true
	case "ctrl+f", "pgdown":
		step := m.detail.Height
		if step < 1 {
			step = 1
		}
		m.detailMoveCursor(step)
		return m, nil, true
	case "ctrl+b", "pgup":
		step := m.detail.Height
		if step < 1 {
			step = 1
		}
		m.detailMoveCursor(-step)
		return m, nil, true
	case "v":
		if m.detailVisual && !m.detailVisualLine {
			m.detailVisual = false
			m.detailVisualLine = false
			m.toast = ""
		} else {
			m.detailStartVisual(false)
		}
		return m, nil, true
	case "V":
		if m.detailVisual && m.detailVisualLine {
			m.detailVisual = false
			m.detailVisualLine = false
			m.toast = ""
		} else {
			m.detailStartVisual(true)
		}
		return m, nil, true
	case "y":
		if m.detailVisual {
			m.detailYankSelection()
		} else {
			m.detailPending = "y"
		}
		return m, nil, true
	case "Y":
		m.detailYankLine()
		return m, nil, true
	case "esc":
		if m.detailVisual {
			m.detailVisual = false
			m.detailVisualLine = false
			m.toast = ""
			return m, nil, true
		}
		if m.detailPending != "" {
			m.detailPending = ""
			return m, nil, true
		}
		// let global esc return focus to the list pane
		return m, nil, false
	}

	return m, nil, false
}
