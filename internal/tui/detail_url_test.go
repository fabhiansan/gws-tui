package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/fabhiansan/gws-tui/internal/api"
)

func TestDetailURLAtCursorFindsSingleURLOnLine(t *testing.T) {
	got, ok := detailURLAtCursor([]string{"see https://example.com/path?x=1."}, 0, 0)
	if !ok {
		t.Fatal("expected URL on line")
	}
	if got != "https://example.com/path?x=1" {
		t.Fatalf("URL mismatch: got %q", got)
	}
}

func TestDetailURLAtCursorUsesCursorWhenMultipleURLsExist(t *testing.T) {
	line := "one https://a.example/path two https://b.example/path"
	got, ok := detailURLAtCursor([]string{line}, 0, 34)
	if !ok {
		t.Fatal("expected URL under cursor")
	}
	if got != "https://b.example/path" {
		t.Fatalf("URL mismatch: got %q", got)
	}
}

func TestDetailURLAtCursorKeepsBalancedClosingParen(t *testing.T) {
	got, ok := detailURLAtCursor([]string{"docs (https://example.com/a_(b))"}, 0, 0)
	if !ok {
		t.Fatal("expected URL on line")
	}
	if got != "https://example.com/a_(b)" {
		t.Fatalf("URL mismatch: got %q", got)
	}
}

func TestDetailURLAtCursorJoinsWrappedURLSegments(t *testing.T) {
	lines := []string{
		"see https://example.com/verylong",
		"path?x=1&y=2.",
	}
	width := len(lines[0])
	want := "https://example.com/verylongpath?x=1&y=2"

	for _, tc := range []struct {
		name string
		line int
		col  int
	}{
		{name: "first segment", line: 0, col: 6},
		{name: "continuation", line: 1, col: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := detailURLAtCursorWrapped(lines, tc.line, tc.col, width)
			if !ok {
				t.Fatal("expected wrapped URL")
			}
			if got != want {
				t.Fatalf("URL mismatch: got %q want %q", got, want)
			}
		})
	}
}

func TestDetailURLAtCursorDoesNotJoinShortExplicitNextLine(t *testing.T) {
	lines := []string{
		"see https://example.com/path",
		"continued",
	}
	got, ok := detailURLAtCursorWrapped(lines, 0, 0, 80)
	if !ok {
		t.Fatal("expected URL on first line")
	}
	if got != "https://example.com/path" {
		t.Fatalf("URL mismatch: got %q", got)
	}
}

func TestEnterOnDetailURLOpensBrowser(t *testing.T) {
	var opened string
	previousOpenURL := openURL
	openURL = func(url string) error {
		opened = url
		return nil
	}
	t.Cleanup(func() {
		openURL = previousOpenURL
	})

	model := Model{
		detail:      viewport.New(80, 20),
		focusedPane: paneDetail,
		detailLines: []string{
			"spec https://example.com/spec.",
		},
		detailLineCount: 1,
	}

	updated, cmd := model.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected open browser command")
	}
	if updated.toast != "opening browser" {
		t.Fatalf("toast mismatch: got %q", updated.toast)
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("expected nil success message, got %T", msg)
	}
	if opened != "https://example.com/spec" {
		t.Fatalf("opened URL mismatch: got %q", opened)
	}
}

func TestEnterOnWrappedDetailURLOpensFullURL(t *testing.T) {
	var opened string
	previousOpenURL := openURL
	openURL = func(url string) error {
		opened = url
		return nil
	}
	t.Cleanup(func() {
		openURL = previousOpenURL
	})

	lines := []string{
		"spec https://example.com/verylong",
		"path?x=1&y=2.",
	}
	model := Model{
		detail:          viewport.New(len(lines[0]), 20),
		focusedPane:     paneDetail,
		detailLines:     lines,
		detailLineCount: len(lines),
		detailCursor:    0,
		detailCol:       8,
	}

	_, cmd := model.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected open browser command")
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("expected nil success message, got %T", msg)
	}
	if opened != "https://example.com/verylongpath?x=1&y=2" {
		t.Fatalf("opened URL mismatch: got %q", opened)
	}
}

func TestEnterOnDetailURLReportsOpenError(t *testing.T) {
	previousOpenURL := openURL
	openURL = func(string) error {
		return errors.New("no opener")
	}
	t.Cleanup(func() {
		openURL = previousOpenURL
	})

	model := Model{
		detail:          viewport.New(80, 20),
		focusedPane:     paneDetail,
		detailLines:     []string{"https://example.com"},
		detailLineCount: 1,
	}

	updated, cmd := model.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected open browser command")
	}
	next, _ := updated.Update(cmd())
	final := next.(Model)
	if final.toast != "open: no opener" {
		t.Fatalf("toast mismatch: got %q", final.toast)
	}
}

func TestEnterOnAttachmentLineTakesPriorityOverURL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	previousOpenURL := openURL
	openURL = func(string) error {
		t.Fatal("openURL should not be called for mapped attachment lines")
		return nil
	}
	t.Cleanup(func() {
		openURL = previousOpenURL
	})

	att := api.Attachment{
		Name:         "report.pdf",
		URL:          "https://example.com/report.pdf",
		ResourceName: "spaces/AAA/attachments/report",
		ContentType:  "application/pdf",
	}
	client := &recordingAttachmentDownloadClient{}
	model := Model{
		ctx:                context.Background(),
		client:             client,
		detail:             viewport.New(80, 20),
		focusedPane:        paneDetail,
		detailAttachmentAt: map[int]api.Attachment{0: att},
		detailLines:        []string{"[attachment] report.pdf https://example.com/report.pdf"},
		detailLineCount:    1,
	}

	updated, cmd := model.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected attachment download command")
	}
	if !strings.Contains(updated.toast, "downloading report.pdf") {
		t.Fatalf("expected attachment toast, got %q", updated.toast)
	}
	if msg := cmd(); msg == nil {
		t.Fatal("expected attachment download message")
	}
	if client.calls != 1 {
		t.Fatalf("expected one attachment download, got %d", client.calls)
	}
}
