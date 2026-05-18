package tui

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fabhiantomaoludyo/gws-tui/internal/api"
)

// imageViewerState holds the fullscreen Kitty render for one attachment.
// `renderKey` lets us re-encode only when the terminal resizes or the file
// is replaced — typing keys while the viewer is open should not pay the
// decode/encode cost.
type imageViewerState struct {
	attachment api.Attachment
	path       string
	render     string
	renderKey  string
}

// openImageViewer enters fullscreen mode for the given attachment. It's a
// no-op (with a toast) when the terminal can't draw Kitty graphics or when
// the image isn't downloaded yet, so the caller can wire it unconditionally
// to a key binding.
func (m *Model) openImageViewer(attachment api.Attachment) {
	if !isKittyTerminal() {
		m.toast = "fullscreen needs kitty"
		return
	}
	if !attachment.IsImage() {
		m.toast = "not an image"
		return
	}
	source := attachment.PreviewSource()
	local, ok := m.previewImagePath(source)
	if !ok {
		m.toast = "image still loading"
		return
	}
	m.imageViewer = &imageViewerState{attachment: attachment, path: local}
	m.imageVersion++
}

// closeImageViewer is split out so the input handler reads cleanly.
func (m *Model) closeImageViewer() {
	if m.imageViewer == nil {
		return
	}
	m.imageViewer = nil
	m.imageVersion++
}

// imageViewerCellBudget converts the terminal width/height into the
// (columns, rows) cell grid we hand to Kitty for the fullscreen frame.
// Leaves a 1-cell margin around the image and a footer row for the hint.
func imageViewerCellBudget(width, height int) (cols, rows int) {
	cols = width - 2
	rows = height - 3
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	return cols, rows
}

func (m Model) renderImageViewer(width, height int) string {
	view := m.imageViewer
	if view == nil {
		return ""
	}

	cols, rows := imageViewerCellBudget(width, height)

	stat, statErr := os.Stat(view.path)
	var sizeTag string
	var modTag string
	if statErr == nil {
		sizeTag = fmt.Sprintf("%d", stat.Size())
		modTag = stat.ModTime().UTC().Format("20060102T150405.000")
	}
	cacheKey := fmt.Sprintf("%d:%d:%s:%s:%s", cols, rows, view.path, sizeTag, modTag)

	frame := view.render
	if frame == "" || view.renderKey != cacheKey || statErr != nil {
		encoded, err := kittyImage(view.path, view.path, cols, rows)
		if err != nil {
			content := m.subtle(fmt.Sprintf("image render failed: %v", err))
			return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content)
		}
		view.render = encoded
		view.renderKey = cacheKey
		frame = encoded
	}

	footer := m.imageViewerFooter(view, width)
	body := lipgloss.JoinVertical(lipgloss.Left, frame, "", footer)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, body)
}

func (m Model) imageViewerFooter(view *imageViewerState, width int) string {
	name := view.attachment.DisplayName()
	if name == "" {
		name = view.path
	}
	hint := "q close · y yank path · O open external"
	left := truncate(name, max(8, width-len(hint)-4))
	footer := left + "  " + m.subtle(hint)
	return footer
}

func (m Model) updateImageViewer(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "enter":
		m.closeImageViewer()
		return m, nil
	case "y":
		if err := copyText(m.imageViewer.path); err != nil {
			m.toast = "yank: " + err.Error()
		} else {
			m.toast = "path yanked"
		}
		return m, nil
	case "O":
		path := m.imageViewer.path
		return m, func() tea.Msg {
			if err := openURL(path); err != nil {
				return imageViewerOpenErrMsg{err: err.Error()}
			}
			return nil
		}
	}
	return m, nil
}

// imageViewerOpenErrMsg carries an O-key failure back to the model so we can
// surface it as a toast on the next render cycle.
type imageViewerOpenErrMsg struct{ err string }
