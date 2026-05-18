package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type paneRect struct {
	x, y, w, h int
}

func (r paneRect) contains(x, y int) bool {
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

// paneRects returns the outer bounding boxes (including border) of the three
// main panes, using the same geometry the render path uses. Keeping this in
// one place means hit-testing always matches what the user sees.
func (m Model) paneRects() (list, detail, action paneRect) {
	w, h := m.width, m.height
	if w <= 0 {
		w = 100
	}
	if h <= 0 {
		h = 32
	}
	leftHBorder := m.theme.Pane.GetHorizontalBorderSize()
	leftVBorder := m.theme.Pane.GetVerticalBorderSize()
	detailHBorder := m.theme.Active.GetHorizontalBorderSize()
	detailVBorder := m.theme.Active.GetVerticalBorderSize()
	actionVBorder := m.theme.Input.GetVerticalBorderSize()
	statusH := 1

	leftW := max(20, int(float64(w)*0.30)-leftHBorder)
	rightW := max(20, w-leftW-leftHBorder-detailHBorder)
	actionContentH := max(3, min(8, strings.Count(m.input.Value(), "\n")+3))
	detailContentH := max(5, h-statusH-detailVBorder-actionContentH-actionVBorder)
	leftContentH := max(5, h-statusH-leftVBorder)

	leftTotalW := leftW + leftHBorder
	rightTotalW := rightW + detailHBorder
	leftTotalH := leftContentH + leftVBorder
	detailTotalH := detailContentH + detailVBorder
	actionTotalH := actionContentH + actionVBorder

	list = paneRect{x: 0, y: 0, w: leftTotalW, h: leftTotalH}
	detail = paneRect{x: leftTotalW, y: 0, w: rightTotalW, h: detailTotalH}
	action = paneRect{x: leftTotalW, y: detailTotalH, w: rightTotalW, h: actionTotalH}
	return
}

func (m Model) paneAt(x, y int) (pane, paneRect, bool) {
	list, detail, action := m.paneRects()
	if list.contains(x, y) {
		return paneList, list, true
	}
	if detail.contains(x, y) {
		return paneDetail, detail, true
	}
	if action.contains(x, y) {
		return paneAction, action, true
	}
	return 0, paneRect{}, false
}

func (m Model) updateMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	if m.helpVisible || m.modal != nil {
		return m, nil
	}

	target, rect, ok := m.paneAt(msg.X, msg.Y)
	if !ok {
		return m, nil
	}

	switch msg.Button {
	case tea.MouseButtonWheelUp:
		return m.mouseScroll(target, -mouseScrollLines(msg))
	case tea.MouseButtonWheelDown:
		return m.mouseScroll(target, +mouseScrollLines(msg))
	}

	if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
		return m.mouseClick(target, rect, msg.X, msg.Y)
	}
	return m, nil
}

func mouseScrollLines(msg tea.MouseMsg) int {
	if msg.Shift {
		return 5
	}
	return 1
}

func (m Model) mouseScroll(target pane, delta int) (Model, tea.Cmd) {
	if delta == 0 {
		return m, nil
	}
	switch target {
	case paneList:
		return m.moveSelection(delta)
	case paneDetail:
		if delta > 0 {
			m.detail.LineDown(delta)
		} else {
			m.detail.LineUp(-delta)
		}
		return m, nil
	case paneAction:
		// Forward wheel to the textarea so cursor moves naturally.
		key := tea.KeyMsg{Type: tea.KeyDown}
		if delta < 0 {
			key.Type = tea.KeyUp
		}
		count := delta
		if count < 0 {
			count = -count
		}
		var cmd tea.Cmd
		for i := 0; i < count; i++ {
			var c tea.Cmd
			m.input, c = m.input.Update(key)
			if c != nil {
				cmd = c
			}
		}
		return m, cmd
	}
	return m, nil
}

func (m Model) mouseClick(target pane, rect paneRect, x, y int) (Model, tea.Cmd) {
	// Always focus the pane the user clicked on.
	prev := m.focusedPane
	m.focusedPane = target

	if target == paneAction {
		if prev != paneAction {
			m.input.Focus()
			if m.cfg.VimMode {
				m.vimComposer = vimModeInsert
			}
		}
		return m, nil
	}

	// Clicking off the composer should blur it so vim/keys behave normally.
	if prev == paneAction {
		m.input.Blur()
		if m.cfg.VimMode {
			m.vimComposer = vimModeNormal
		}
	}

	if target == paneList {
		idx, ok := m.listRowAt(rect, y)
		if ok {
			length := m.listLen()
			if length > 0 && idx >= 0 && idx < length {
				if idx != m.selected[m.feature] {
					m.selected[m.feature] = idx
					return m.loadSelectedChat()
				}
			}
		}
	}
	return m, nil
}

// listRowAt maps an absolute Y coordinate inside the list pane to a logical
// item index, accounting for the pane border and title row, and for features
// whose rows span multiple terminal lines.
func (m Model) listRowAt(rect paneRect, y int) (int, bool) {
	leftVBorder := m.theme.Pane.GetVerticalBorderSize()
	topPad := leftVBorder / 2
	titleLines := 1
	innerStart := rect.y + topPad + titleLines
	row := y - innerStart
	if row < 0 {
		return 0, false
	}
	switch m.feature {
	case FeatureChat, FeatureMeet:
		return row, true
	case FeatureMail:
		// Each thread renders as three lines (sender, subject, separator).
		return row / 3, true
	case FeatureCalendar:
		// Day headers are interleaved with events, so walk the rendered
		// sequence to find which event sits at this row.
		lastDay := ""
		visualRow := 0
		for i, event := range sortedEvents(m.events) {
			day := event.Start.Format("Mon 02 Jan")
			if day != lastDay {
				if visualRow == row {
					return i, true
				}
				visualRow++
				lastDay = day
			}
			if visualRow == row {
				return i, true
			}
			visualRow++
		}
		return 0, false
	}
	return 0, false
}
