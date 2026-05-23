package tui

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const maxMessageLogEntries = 200

type messageLogEntry struct {
	at    time.Time
	level string
	text  string
}

func (m *Model) captureTransientMessages(prevErr, prevToast string) {
	if m.err != "" && m.err != prevErr {
		m.recordMessage("error", m.err)
		// Every error the TUI surfaces also lands in tui.log: the on-screen
		// status line is transient and one-shot, the log is the durable trail.
		slog.Error("tui error surfaced", "detail", m.err, "feature", m.feature)
	}
	if m.toast != "" && m.toast != prevToast {
		m.recordMessage("info", m.toast)
	}
}

func (m *Model) recordMessage(level, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	level = strings.TrimSpace(strings.ToLower(level))
	if level == "" {
		level = "info"
	}

	wasAtBottom := m.messageLogScroll >= m.maxMessageLogScroll()
	m.messageLog = append(m.messageLog, messageLogEntry{
		at:    time.Now(),
		level: level,
		text:  text,
	})
	if len(m.messageLog) > maxMessageLogEntries {
		drop := len(m.messageLog) - maxMessageLogEntries
		m.messageLog = append([]messageLogEntry(nil), m.messageLog[drop:]...)
		m.messageLogCursor = max(0, m.messageLogCursor-drop)
		m.messageLogAnchor = max(0, m.messageLogAnchor-drop)
	}
	if !m.messagesVisible || wasAtBottom {
		m.messageLogCursor = max(0, len(m.messageLog)-1)
		m.messageLogCol = 0
		m.messageLogScroll = m.maxMessageLogScroll()
	} else {
		m.clampMessageLogCursor()
	}
}

func (m *Model) openMessageLog() {
	m.helpVisible = false
	m.messagesVisible = true
	m.messageLogVisual = false
	m.messageLogVisualLine = false
	m.messageLogPending = ""
	m.messageLogCursor = max(0, len(m.messageLog)-1)
	m.messageLogCol = 0
	m.messageLogScroll = m.maxMessageLogScroll()
}

func (m Model) updateMessageLogKey(msg tea.KeyMsg) Model {
	if !m.cfg.VimMode {
		return m.updatePlainMessageLogKey(msg)
	}
	return m.updateMessageLogVim(msg)
}

func (m Model) updatePlainMessageLogKey(msg tea.KeyMsg) Model {
	switch msg.String() {
	case "esc", "q", ":":
		m.messagesVisible = false
	case "j", "down":
		m.scrollMessageLog(1)
	case "k", "up":
		m.scrollMessageLog(-1)
	case "ctrl+d", "pgdown":
		m.scrollMessageLog(max(1, m.messageLogVisibleRows()/2))
	case "ctrl+u", "pgup":
		m.scrollMessageLog(-max(1, m.messageLogVisibleRows()/2))
	case "g":
		m.messageLogScroll = 0
	case "G":
		m.messageLogScroll = m.maxMessageLogScroll()
	}
	return m
}

func (m Model) updateMessageLogVim(msg tea.KeyMsg) Model {
	key := msg.String()
	lines := m.messageLogLines()
	m.clampMessageLogCursor()

	if m.messageLogPending == "g" {
		m.messageLogPending = ""
		if key == "g" {
			m.messageLogGotoTop()
			return m
		}
	}
	if m.messageLogPending == "y" {
		m.messageLogPending = ""
		if key == "y" {
			m.messageLogYankLine()
			return m
		}
		if key == "esc" {
			return m
		}
		m.toast = "yank motion not supported"
		return m
	}

	switch key {
	case "esc":
		if m.messageLogVisual {
			m.messageLogVisual = false
			m.messageLogVisualLine = false
			m.toast = ""
			return m
		}
		if m.messageLogPending != "" {
			m.messageLogPending = ""
			return m
		}
		m.messagesVisible = false
	case "q", ":":
		m.messagesVisible = false
	case "h", "left":
		m.messageLogMoveColumn(-1)
	case "l", "right":
		m.messageLogMoveColumn(1)
	case "j", "down":
		m.messageLogMoveCursor(1)
	case "k", "up":
		m.messageLogMoveCursor(-1)
	case "w":
		m.messageLogMoveWordForward(lines)
	case "b":
		m.messageLogMoveWordBackward(lines)
	case "e":
		m.messageLogMoveWordEnd(lines)
	case "0", "home":
		m.messageLogCol = 0
	case "$", "end":
		m.messageLogCol = detailLineLastCol(lines, m.messageLogCursor)
	case "g":
		m.messageLogPending = "g"
	case "G":
		m.messageLogGotoBottom()
	case "ctrl+d":
		m.messageLogMoveCursor(max(1, m.messageLogVisibleRows()/2))
	case "ctrl+u":
		m.messageLogMoveCursor(-max(1, m.messageLogVisibleRows()/2))
	case "ctrl+f", "pgdown":
		m.messageLogMoveCursor(max(1, m.messageLogVisibleRows()))
	case "ctrl+b", "pgup":
		m.messageLogMoveCursor(-max(1, m.messageLogVisibleRows()))
	case "v":
		if m.messageLogVisual && !m.messageLogVisualLine {
			m.messageLogVisual = false
			m.messageLogVisualLine = false
			m.toast = ""
		} else {
			m.messageLogStartVisual(false)
		}
	case "V":
		if m.messageLogVisual && m.messageLogVisualLine {
			m.messageLogVisual = false
			m.messageLogVisualLine = false
			m.toast = ""
		} else {
			m.messageLogStartVisual(true)
		}
	case "y":
		if m.messageLogVisual {
			m.messageLogYankSelection()
		} else {
			m.messageLogPending = "y"
		}
	case "Y":
		m.messageLogYankLine()
	}
	return m
}

func (m *Model) scrollMessageLog(delta int) {
	m.messageLogScroll += delta
	m.clampMessageLogScroll()
}

func (m *Model) clampMessageLogScroll() {
	m.messageLogScroll = clamp(m.messageLogScroll, m.maxMessageLogScroll()+1)
}

func (m Model) maxMessageLogScroll() int {
	return max(0, len(m.messageLog)-m.messageLogVisibleRows())
}

func (m Model) messageLogVisibleRows() int {
	height := m.height
	if height <= 0 {
		height = 32
	}
	return max(1, height-m.theme.Modal.GetVerticalFrameSize()-5)
}

func (m Model) messageLogModalWidth(width int) int {
	if width <= 0 {
		width = 100
	}
	modalWidth := min(width-4, 112)
	if modalWidth < 40 {
		modalWidth = max(20, width-2)
	}
	return modalWidth
}

func (m Model) messageLogContentWidth() int {
	width := m.width
	if width <= 0 {
		width = 100
	}
	return max(1, m.messageLogModalWidth(width)-m.theme.Modal.GetHorizontalFrameSize())
}

func (m Model) messageLogLineText(entry messageLogEntry) string {
	return fmt.Sprintf("%s %-5s %s", entry.at.Format("15:04:05"), strings.ToUpper(entry.level), entry.text)
}

func (m Model) messageLogLines() []string {
	width := m.messageLogContentWidth()
	lines := make([]string, 0, len(m.messageLog))
	for _, entry := range m.messageLog {
		lines = append(lines, truncate(m.messageLogLineText(entry), width))
	}
	return lines
}

func (m Model) renderMessageLog(width, height int) string {
	modalWidth := m.messageLogModalWidth(width)
	contentWidth := max(1, modalWidth-m.theme.Modal.GetHorizontalFrameSize())
	rows := m.messageLogVisibleRows()
	if rows > height-m.theme.Modal.GetVerticalFrameSize()-3 {
		rows = max(1, height-m.theme.Modal.GetVerticalFrameSize()-3)
	}

	bufferLines := m.messageLogLines()
	start := clamp(m.messageLogScroll, max(1, len(bufferLines)))
	end := min(len(bufferLines), start+rows)
	renderedLines := make([]string, 0, rows)
	if len(bufferLines) == 0 {
		renderedLines = append(renderedLines, centerText("No messages yet", contentWidth))
	} else {
		cursor := detailClampPoint(bufferLines, detailPoint{line: m.messageLogCursor, col: m.messageLogCol})
		selStart, selEnd := m.messageLogVisualBoundsFor(bufferLines)
		for line := start; line < end; line++ {
			renderedLines = append(renderedLines, m.renderMessageLogBufferLine(bufferLines, line, cursor, selStart, selEnd, contentWidth))
		}
	}
	for len(renderedLines) < rows {
		renderedLines = append(renderedLines, "")
	}

	footer := m.messageLogFooter(end)
	content := strings.Join(append(renderedLines, "", footer), "\n")
	box := m.theme.Modal.Width(modalWidth).Render(content)
	return paneWithTitle(m.theme.Modal, m.title(" gws - messages "), content, modalWidth, lipgloss.Height(box))
}

func (m Model) messageLogFooter(end int) string {
	if m.cfg.VimMode {
		return m.subtle(fmt.Sprintf("hjkl/wbe move | v/V visual | y/yy yank | :/Esc/q close | %d/%d", end, len(m.messageLog)))
	}
	return m.subtle(fmt.Sprintf("j/k scroll | g/G top/bottom | :/Esc/q close | %d/%d", end, len(m.messageLog)))
}

func (m Model) renderMessageLogBufferLine(lines []string, line int, cursor, selStart, selEnd detailPoint, width int) string {
	text := lines[line]
	selectedStart, selectedEnd, selected := m.messageLogSelectionColumnsFor(lines, line, selStart, selEnd)
	cursorCol := -1
	if line == cursor.line {
		cursorCol = cursor.col
	}
	if cursorCol >= 0 && !m.messageLogVisual {
		return m.renderMessageLogCursorLine(text, cursorCol, width)
	}
	if cursorCol >= 0 || selected {
		return m.renderDetailTextLine(text, cursorCol, selectedStart, selectedEnd, selected)
	}
	entry := m.messageLog[line]
	switch entry.level {
	case "error":
		if m.cfg.NoColor {
			return text
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Error)).Render(text)
	case "event":
		return m.subtle(text)
	default:
		return text
	}
}

func (m Model) renderMessageLogCursorLine(line string, cursorCol, width int) string {
	runes := []rune(line)
	if strings.TrimSpace(line) == "" {
		return m.detailCursorStyle().Render(" ") + strings.Repeat(" ", max(0, width-1))
	}
	bg := m.theme.Selected
	cursor := m.detailCursorStyle()
	var out strings.Builder
	used := 0
	for idx := 0; used < width; idx++ {
		cell, cw := " ", 1
		if idx < len(runes) {
			r := runes[idx]
			rw := wideCond.RuneWidth(r)
			if rw < 1 {
				rw = 1
			}
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

func (m *Model) clampMessageLogCursor() {
	lines := m.messageLogLines()
	if len(lines) == 0 {
		m.messageLogCursor = 0
		m.messageLogCol = 0
		m.messageLogAnchor = 0
		m.messageLogAnchorCol = 0
		m.messageLogVisual = false
		m.messageLogVisualLine = false
		m.messageLogPending = ""
		m.messageLogScroll = 0
		return
	}
	if m.messageLogCursor < 0 {
		m.messageLogCursor = 0
	} else if m.messageLogCursor >= len(lines) {
		m.messageLogCursor = len(lines) - 1
	}
	m.messageLogCol = detailClampPoint(lines, detailPoint{line: m.messageLogCursor, col: m.messageLogCol}).col
	if m.messageLogAnchor < 0 {
		m.messageLogAnchor = 0
	} else if m.messageLogAnchor >= len(lines) {
		m.messageLogAnchor = len(lines) - 1
	}
	m.messageLogAnchorCol = detailClampPoint(lines, detailPoint{line: m.messageLogAnchor, col: m.messageLogAnchorCol}).col
	m.messageLogEnsureCursorVisible()
}

func (m *Model) messageLogEnsureCursorVisible() {
	rows := m.messageLogVisibleRows()
	if m.messageLogCursor < m.messageLogScroll {
		m.messageLogScroll = m.messageLogCursor
	}
	if m.messageLogCursor >= m.messageLogScroll+rows {
		m.messageLogScroll = m.messageLogCursor - rows + 1
	}
	m.clampMessageLogScroll()
}

func (m *Model) messageLogMoveCursor(delta int) {
	lines := m.messageLogLines()
	if len(lines) == 0 {
		return
	}
	m.messageLogCursor = clamp(m.messageLogCursor+delta, len(lines))
	m.messageLogCol = detailClampPoint(lines, detailPoint{line: m.messageLogCursor, col: m.messageLogCol}).col
	m.messageLogEnsureCursorVisible()
}

func (m *Model) messageLogMoveColumn(delta int) {
	lines := m.messageLogLines()
	if len(lines) == 0 {
		return
	}
	last := detailLineLastCol(lines, m.messageLogCursor)
	m.messageLogCol = min(max(0, m.messageLogCol+delta), last)
}

func (m *Model) messageLogGotoTop() {
	if len(m.messageLogLines()) == 0 {
		return
	}
	m.messageLogCursor = 0
	m.messageLogCol = 0
	m.messageLogScroll = 0
}

func (m *Model) messageLogGotoBottom() {
	lines := m.messageLogLines()
	if len(lines) == 0 {
		return
	}
	m.messageLogCursor = len(lines) - 1
	m.messageLogCol = detailClampPoint(lines, detailPoint{line: m.messageLogCursor, col: m.messageLogCol}).col
	m.messageLogEnsureCursorVisible()
}

func (m *Model) messageLogSetPoint(lines []string, p detailPoint) {
	p = detailClampPoint(lines, p)
	m.messageLogCursor = p.line
	m.messageLogCol = p.col
	m.messageLogEnsureCursorVisible()
}

func (m *Model) messageLogMoveWordForward(lines []string) {
	text := []rune(strings.Join(lines, "\n"))
	if len(text) == 0 {
		return
	}
	pos := detailAbsPos(lines, detailPoint{line: m.messageLogCursor, col: m.messageLogCol})
	if pos >= len(text)-1 {
		m.messageLogSetPoint(lines, detailPointForAbs(lines, len(text)-1))
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
	m.messageLogSetPoint(lines, detailPointForAbs(lines, pos))
}

func (m *Model) messageLogMoveWordBackward(lines []string) {
	text := []rune(strings.Join(lines, "\n"))
	if len(text) == 0 {
		return
	}
	pos := detailAbsPos(lines, detailPoint{line: m.messageLogCursor, col: m.messageLogCol})
	if pos <= 0 {
		m.messageLogSetPoint(lines, detailPointForAbs(lines, 0))
		return
	}
	pos--
	for pos > 0 && !detailTokenRune(text[pos]) {
		pos--
	}
	for pos > 0 && detailTokenRune(text[pos-1]) {
		pos--
	}
	m.messageLogSetPoint(lines, detailPointForAbs(lines, pos))
}

func (m *Model) messageLogMoveWordEnd(lines []string) {
	text := []rune(strings.Join(lines, "\n"))
	if len(text) == 0 {
		return
	}
	pos := detailAbsPos(lines, detailPoint{line: m.messageLogCursor, col: m.messageLogCol})
	if pos >= len(text)-1 {
		m.messageLogSetPoint(lines, detailPointForAbs(lines, len(text)-1))
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
	m.messageLogSetPoint(lines, detailPointForAbs(lines, pos))
}

func (m *Model) messageLogVisualBoundsFor(lines []string) (start, end detailPoint) {
	cursor := detailClampPoint(lines, detailPoint{line: m.messageLogCursor, col: m.messageLogCol})
	anchor := detailClampPoint(lines, detailPoint{line: m.messageLogAnchor, col: m.messageLogAnchorCol})
	if !m.messageLogVisual {
		return cursor, cursor
	}
	if detailPointLess(cursor, anchor) {
		return cursor, anchor
	}
	return anchor, cursor
}

func (m *Model) messageLogSelectionColumnsFor(lines []string, line int, start, end detailPoint) (int, int, bool) {
	if !m.messageLogVisual || line < start.line || line > end.line {
		return 0, 0, false
	}
	last := detailLineLastCol(lines, line)
	if m.messageLogVisualLine {
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

func (m *Model) messageLogStartVisual(linewise bool) {
	m.messageLogVisual = true
	m.messageLogVisualLine = linewise
	m.messageLogAnchor = m.messageLogCursor
	m.messageLogAnchorCol = m.messageLogCol
	if linewise {
		m.toast = "-- VISUAL LINE --"
	} else {
		m.toast = "-- VISUAL --"
	}
}

func (m *Model) messageLogYankLine() {
	lines := m.messageLogLines()
	if len(lines) == 0 || m.messageLogCursor < 0 || m.messageLogCursor >= len(lines) {
		m.toast = "nothing to yank"
		return
	}
	m.messageLogYankText(lines[m.messageLogCursor], true, 1)
}

func (m *Model) messageLogYankText(text string, linewise bool, lines int) {
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
		m.toast = "line yanked"
		if !linewise {
			m.toast = "selection yanked"
		}
		return
	}
	m.toast = fmt.Sprintf("%d lines yanked", lines)
}

func (m *Model) messageLogYankSelection() {
	lines := m.messageLogLines()
	if len(lines) == 0 {
		m.toast = "nothing to yank"
		return
	}
	startPoint, endPoint := m.messageLogVisualBoundsFor(lines)
	if m.messageLogVisualLine {
		text := strings.Join(lines[startPoint.line:endPoint.line+1], "\n")
		m.messageLogYankText(text, true, endPoint.line-startPoint.line+1)
		m.messageLogVisual = false
		m.messageLogVisualLine = false
		return
	}

	parts := make([]string, 0, endPoint.line-startPoint.line+1)
	for line := startPoint.line; line <= endPoint.line; line++ {
		runes := []rune(lines[line])
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
	m.messageLogYankText(strings.Join(parts, "\n"), false, endPoint.line-startPoint.line+1)
	m.messageLogVisual = false
	m.messageLogVisualLine = false
}
