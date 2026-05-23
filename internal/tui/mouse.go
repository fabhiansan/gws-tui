package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type paneRect struct {
	x, y, w, h int
}

func (r paneRect) contains(x, y int) bool {
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

// paneRects returns the outer bounding boxes (including border) of the panes,
// using the same geometry the render path uses. Keeping this in one place
// means hit-testing always matches what the user sees. The sidebar rect is
// only populated for the Mail feature; it stays zero elsewhere.
func (m Model) paneRects() (list, detail, action, sidebar paneRect) {
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

	// Mail uses the Gmail-style layout: a sidebar on the left, then the inbox
	// list and the reading pane sharing one slot on the right. Only whichever
	// of the two is currently visible is reported as a hit-testable pane.
	if m.feature == FeatureMail {
		sidebarW := mailSidebarWidth(w)
		mainW := max(30, w-sidebarW-leftHBorder-detailHBorder)
		sidebarTotalW := sidebarW + leftHBorder
		mainTotalW := mainW + detailHBorder

		// The composer pane is only on screen (and hit-testable) while it is
		// the focused pane — otherwise the main pane fills the right column.
		mainContentH := max(5, h-statusH-detailVBorder)
		if m.focusedPane == paneAction {
			actionContentH := max(3, min(8, strings.Count(m.input.Value(), "\n")+3))
			mainContentH = max(5, h-statusH-detailVBorder-actionContentH-actionVBorder)
			action = paneRect{x: sidebarTotalW, y: mainContentH + detailVBorder, w: mainTotalW, h: actionContentH + actionVBorder}
		}
		sidebar = paneRect{x: 0, y: 0, w: sidebarTotalW, h: max(1, h-statusH)}
		mainRect := paneRect{x: sidebarTotalW, y: 0, w: mainTotalW, h: mainContentH + detailVBorder}
		if m.mailListVisible() {
			list = mainRect
		} else {
			detail = mainRect
		}
		return
	}

	if m.feature == FeatureCalendar {
		mainRect := paneRect{x: 0, y: 0, w: w, h: max(1, h-statusH)}
		if m.focusedPane == paneDetail {
			detail = mainRect
			return
		}
		if m.focusedPane == paneAction {
			actionContentH := max(3, min(8, strings.Count(m.input.Value(), "\n")+3))
			calendarH := max(8, h-statusH-detailVBorder-actionContentH-actionVBorder)
			list = paneRect{x: 0, y: 0, w: w, h: calendarH + detailVBorder}
			action = paneRect{x: 0, y: list.h, w: w, h: actionContentH + actionVBorder}
			return
		}
		list = mainRect
		return
	}

	if m.feature == FeatureDocs {
		mainRect := paneRect{x: 0, y: 0, w: w, h: max(1, h-statusH)}
		if m.singlePaneDetailVisible() {
			detail = mainRect
		} else {
			list = mainRect
		}
		return
	}

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
	list, detail, action, sidebar := m.paneRects()
	if list.contains(x, y) {
		return paneList, list, true
	}
	if detail.contains(x, y) {
		return paneDetail, detail, true
	}
	if action.contains(x, y) {
		return paneAction, action, true
	}
	if sidebar.contains(x, y) {
		return paneMailSidebar, sidebar, true
	}
	return 0, paneRect{}, false
}

func (m Model) updateMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	if m.helpVisible || m.messagesVisible {
		return m, nil
	}
	if m.modal != nil {
		return m.updateModalMouse(msg)
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
	case paneMailSidebar:
		m.moveMailFolderCursor(delta)
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

	if target == paneMailSidebar {
		if idx, ok := m.mailFolderRowAt(rect, y); ok {
			if folders := m.mailFolderList(); idx >= 0 && idx < len(folders) {
				m.mailFolderCursor = idx
				return m.selectMailFolder()
			}
		}
		return m, nil
	}

	if target == paneList {
		idx, ok := m.listRowAt(rect, y)
		if ok {
			length := m.listLen()
			if length > 0 && idx >= 0 && idx < length {
				if idx != m.selected[m.feature] {
					m.selected[m.feature] = idx
					return m.loadSelectedItem()
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
	case FeatureChat, FeatureTasks, FeatureDrive, FeatureDocs:
		return row, true
	case FeatureMeet:
		// Date headers and the inline-expanded selection make Meet rows
		// variable-height, so a click only focuses the pane; j/k drive the
		// conference cursor.
		return 0, false
	case FeatureMail:
		// Gmail-style inbox: one terminal line per thread.
		return row, true
	case FeatureCalendar:
		if m.calendarView == calViewMonth {
			// The grid is addressed by row and column; a click only focuses
			// the pane (keys drive day selection).
			return 0, false
		}
		// Day headers are interleaved with events, so walk the rendered
		// sequence (matching renderList) to find the event at this row.
		today := startOfDay(time.Now())
		lastLabel := ""
		visualRow := 0
		for i, event := range m.events {
			label := relativeDayLabel(startOfDay(event.Start), today)
			if label != lastLabel {
				if visualRow == row {
					return i, true
				}
				visualRow++
				lastLabel = label
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

// mailFolderRowAt maps an absolute Y coordinate inside the Mail folder rail to
// a folder index, accounting for the pane border, title row, and the blank +
// "Labels" header that separate the system folders from the user labels.
func (m Model) mailFolderRowAt(rect paneRect, y int) (int, bool) {
	vBorder := m.theme.Pane.GetVerticalBorderSize()
	innerStart := rect.y + vBorder/2 + 1 // +1 for the title row
	row := y - innerStart
	if row < 0 {
		return 0, false
	}
	systemCount := len(mailSystemFolderDefs)
	if row < systemCount {
		return row, true
	}
	// Rows systemCount and systemCount+1 are the blank line and "Labels"
	// header; user labels begin two rows below the last system folder.
	custom := row - systemCount - 2
	if custom < 0 {
		return 0, false
	}
	if idx := systemCount + custom; idx < len(m.mailFolderList()) {
		return idx, true
	}
	return 0, false
}

// updateModalMouse handles mouse input while a compose modal is open: the
// wheel scrolls the focused body textarea, and a left click focuses the field
// under the pointer (entering INSERT mode so the user can type right away).
func (m Model) updateModalMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	if m.modal == nil || len(m.modal.fields) == 0 {
		return m, nil
	}
	field := &m.modal.fields[m.modal.focus]
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if field.Multiline {
			for range mouseScrollLines(msg) {
				field.update(tea.KeyMsg{Type: tea.KeyUp})
			}
		}
		return m, nil
	case tea.MouseButtonWheelDown:
		if field.Multiline {
			for range mouseScrollLines(msg) {
				field.update(tea.KeyMsg{Type: tea.KeyDown})
			}
		}
		return m, nil
	}
	if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
		if idx, ok := m.modalFieldAt(msg.X, msg.Y); ok {
			m.modalSetFocus(idx)
			if m.cfg.VimMode {
				m.modal.vimMode = vimModeInsert
			}
		}
	}
	return m, nil
}

// modalFieldAt maps an absolute screen coordinate to the modal field drawn
// there. It re-renders the modal to measure its real placement so the result
// always matches what the user sees, then offsets past the top border and the
// modal's top padding to reach the content rows.
func (m Model) modalFieldAt(x, y int) (int, bool) {
	if m.modal == nil {
		return 0, false
	}
	rendered := m.renderModal(max(40, m.width-14))
	boxW := lipgloss.Width(rendered)
	boxH := lipgloss.Height(rendered)
	boxX := (m.width - boxW) / 2
	boxY := (m.height - boxH) / 2
	if x < boxX || x >= boxX+boxW || y < boxY || y >= boxY+boxH {
		return 0, false
	}
	row := y - boxY - 2 // skip the top border row and the top padding row
	_, fieldOf := m.modalContentLines()
	if row < 0 || row >= len(fieldOf) {
		return 0, false
	}
	if idx := fieldOf[row]; idx >= 0 {
		return idx, true
	}
	return 0, false
}
