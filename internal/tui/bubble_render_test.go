package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/fabhiansan/gws-tui/internal/api"
)

func newBubbleTestModel(t *testing.T) *Model {
	t.Helper()
	dir := t.TempDir()
	model := New(Options{
		Client: newTestWorkspaceClient(),
		Config: Config{
			InitialFeature: "chat",
			StatePath:      filepath.Join(dir, "state.json"),
			DraftDir:       dir,
			NoColor:        true,
			NoIcons:        true,
		},
	})
	model.width = 100
	model.height = 30
	model.resize()
	model.spaces = []api.Space{{Name: "spaces/b", DisplayName: "Bubble"}}
	model.selected[FeatureChat] = 0
	model.selfUserIDs = map[string]bool{"me": true}
	return &model
}

func TestGroupBurstsCoalescesSameSenderWithinWindow(t *testing.T) {
	model := newBubbleTestModel(t)
	base := time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC)
	bursts := model.groupBursts([]api.ChatMessage{
		{SenderID: "users/alice", SenderName: "Alice", Text: "hi", CreateTime: base},
		{SenderID: "users/alice", SenderName: "Alice", Text: "halo", CreateTime: base.Add(2 * time.Minute)},
		{SenderID: "users/bob", SenderName: "Bob", Text: "yo", CreateTime: base.Add(3 * time.Minute)},
		{SenderID: "users/alice", SenderName: "Alice", Text: "kembali", CreateTime: base.Add(4 * time.Minute)},
	})
	if len(bursts) != 3 {
		t.Fatalf("expected 3 bursts (Alice×2, Bob, Alice), got %d", len(bursts))
	}
	if len(bursts[0].messages) != 2 {
		t.Errorf("first Alice burst should have 2 messages, got %d", len(bursts[0].messages))
	}
	if bursts[1].senderID != "users/bob" {
		t.Errorf("second burst should be Bob, got %q", bursts[1].senderID)
	}
}

func TestGroupBurstsSplitsAtWindowGap(t *testing.T) {
	model := newBubbleTestModel(t)
	base := time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC)
	bursts := model.groupBursts([]api.ChatMessage{
		{SenderID: "users/alice", SenderName: "Alice", Text: "hi", CreateTime: base},
		// 6 minutes later — outside the 5-minute window, should split.
		{SenderID: "users/alice", SenderName: "Alice", Text: "halo lagi", CreateTime: base.Add(6 * time.Minute)},
	})
	if len(bursts) != 2 {
		t.Fatalf("expected 2 bursts after window gap, got %d", len(bursts))
	}
}

func TestRenderBubbleHasSenderInTopBorder(t *testing.T) {
	model := newBubbleTestModel(t)
	burst := messageBurst{
		senderID:   "users/alice",
		senderName: "Alice",
		isSelf:     false,
		messages: []api.ChatMessage{
			{SenderID: "users/alice", SenderName: "Alice", Text: "halo bro", CreateTime: time.Date(2026, 5, 19, 10, 23, 0, 0, time.UTC)},
		},
	}
	lines := model.renderBubble(burst, 60)
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines (top, body, bottom), got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	top := lines[0]
	if !strings.Contains(top, "Alice") {
		t.Errorf("top border missing sender name: %q", top)
	}
	if !strings.Contains(top, "10:23") {
		t.Errorf("top border missing timestamp: %q", top)
	}
	body := strings.Join(lines, "\n")
	if !strings.Contains(body, "halo bro") {
		t.Errorf("body missing message text:\n%s", body)
	}
}

func TestRenderBubbleSelfMessageRightAligned(t *testing.T) {
	model := newBubbleTestModel(t)
	burst := messageBurst{
		senderID:   "users/me",
		senderName: "You",
		isSelf:     true,
		messages: []api.ChatMessage{
			{SenderID: "users/me", SenderName: "You", Text: "tes", CreateTime: time.Date(2026, 5, 19, 10, 23, 0, 0, time.UTC)},
		},
	}
	textWidth := 60
	lines := model.renderBubble(burst, textWidth)
	if len(lines) == 0 {
		t.Fatalf("self bubble produced no output")
	}
	for _, line := range lines {
		w := lipgloss.Width(line)
		if w != textWidth {
			t.Errorf("self-aligned line should fill textWidth=%d, got width %d: %q", textWidth, w, line)
		}
		// Right alignment puts leading spaces, so the first non-space
		// char shouldn't be the rounded corner at column 0.
		if !strings.HasPrefix(line, " ") {
			t.Errorf("self bubble line should start with padding, got %q", line)
		}
	}
}

func TestGmailSelfBubbleAlignmentPaddingDoesNotPaintBackground(t *testing.T) {
	dir := t.TempDir()
	model := New(Options{
		Client: newTestWorkspaceClient(),
		Config: Config{
			InitialFeature: "chat",
			Theme:          "gmail",
			StatePath:      filepath.Join(dir, "state.json"),
			DraftDir:       dir,
			NoIcons:        true,
		},
	})

	got := model.rightAlignBubbleLine("box", 8)
	if plain := stripANSI(got); plain != "     box" {
		t.Fatalf("plain aligned line = %q, want %q", plain, "     box")
	}
	prefix, _, ok := strings.Cut(got, "box")
	if !ok {
		t.Fatalf("aligned line missing content: %q", got)
	}
	prefixWithoutResets := strings.ReplaceAll(prefix, "\x1b[0m", "")
	prefixWithoutResets = strings.ReplaceAll(prefixWithoutResets, "\x1b[m", "")
	if strings.Contains(prefixWithoutResets, "\x1b[") {
		t.Fatalf("gmail alignment padding should be plain spaces, got ANSI-painted prefix %q in %q", prefix, got)
	}
}

func TestRenderBubbleCoalescesShowsInlineTimestamp(t *testing.T) {
	model := newBubbleTestModel(t)
	base := time.Date(2026, 5, 19, 10, 23, 0, 0, time.UTC)
	burst := messageBurst{
		senderID:   "users/alice",
		senderName: "Alice",
		messages: []api.ChatMessage{
			{SenderID: "users/alice", SenderName: "Alice", Text: "halo", CreateTime: base},
			{SenderID: "users/alice", SenderName: "Alice", Text: "apa kabar", CreateTime: base.Add(2 * time.Minute)},
		},
	}
	body := strings.Join(model.renderBubble(burst, 60), "\n")
	if !strings.Contains(body, "halo") || !strings.Contains(body, "apa kabar") {
		t.Errorf("multi-message burst missing one of the texts:\n%s", body)
	}
	if !strings.Contains(body, "10:25") {
		t.Errorf("inline timestamp for second message not shown:\n%s", body)
	}
	// Top border carries the first timestamp.
	if !strings.Contains(body, "10:23") {
		t.Errorf("first-message timestamp not in top border:\n%s", body)
	}
}

func TestRenderBubbleSkipsCardOnlyMessage(t *testing.T) {
	model := newBubbleTestModel(t)
	burst := messageBurst{
		senderID:   "users/bot",
		senderName: "Braga Bot",
		messages: []api.ChatMessage{
			{SenderID: "users/bot", SenderName: "Braga Bot", Text: "", Cards: []api.ChatCard{{Header: &api.CardHeader{Title: "X"}}}, CreateTime: time.Date(2026, 5, 19, 10, 23, 0, 0, time.UTC)},
		},
	}
	if got := model.renderBubble(burst, 60); got != nil {
		t.Fatalf("card-only burst should return nil, got:\n%s", strings.Join(got, "\n"))
	}
}

func TestChatDetailBubblesAndCardsRenderTogether(t *testing.T) {
	model := newBubbleTestModel(t)
	base := time.Date(2026, 5, 19, 10, 23, 0, 0, time.UTC)
	model.chatMessages = []api.ChatMessage{
		{ID: "1", Space: "spaces/b", SenderID: "users/alice", SenderName: "Alice", Text: "halo bro", CreateTime: base},
		{ID: "2", Space: "spaces/b", SenderID: "users/bot", SenderName: "Bot", Text: "", Cards: []api.ChatCard{{
			Header: &api.CardHeader{Title: "Task Updated", Subtitle: "via Bot"},
			Widgets: []api.CardWidget{{
				Kind:          api.CardWidgetDecoratedText,
				DecoratedText: &api.DecoratedTextWidget{TopLabel: "Task", Text: "Demo"},
			}},
		}}, CreateTime: base.Add(time.Minute)},
	}
	got := model.chatDetail()
	if !strings.Contains(got, "Alice") || !strings.Contains(got, "halo bro") {
		t.Errorf("bubble missing from chatDetail:\n%s", got)
	}
	if !strings.Contains(got, "Task Updated") || !strings.Contains(got, "Demo") {
		t.Errorf("card missing from chatDetail:\n%s", got)
	}
}

func TestRenderBubbleNarrowFallsBackToPlain(t *testing.T) {
	model := newBubbleTestModel(t)
	burst := messageBurst{
		senderID:   "users/alice",
		senderName: "Alice",
		messages: []api.ChatMessage{
			{SenderID: "users/alice", SenderName: "Alice", Text: "hi", CreateTime: time.Date(2026, 5, 19, 10, 23, 0, 0, time.UTC)},
		},
	}
	lines := model.renderBubble(burst, 10) // below minBubbleWidth
	if len(lines) == 0 {
		t.Fatalf("narrow-mode rendering produced no output")
	}
	body := strings.Join(lines, "\n")
	// Plain fallback should not emit the rounded corners.
	if strings.ContainsAny(body, "╭╮╰╯") {
		t.Errorf("narrow fallback should not draw a box, got:\n%s", body)
	}
	if !strings.Contains(body, "hi") {
		t.Errorf("narrow fallback dropped text:\n%s", body)
	}
}
