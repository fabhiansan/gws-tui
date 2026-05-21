package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fabhiansan/gws-tui/internal/api"
)

func TestReplyAllCcExcludesSelfAndSender(t *testing.T) {
	thread := api.MailThread{
		SenderEmail: "alice@example.com",
		To:          "Me <me@example.com>, Bob <bob@example.com>",
		Cc:          "Carol <carol@example.com>, alice@example.com",
	}
	cc := replyAllCc(&thread, "me@example.com")

	for _, banned := range []string{"me@example.com", "alice@example.com"} {
		if strings.Contains(strings.ToLower(cc), banned) {
			t.Fatalf("reply-all Cc should not contain %q: %q", banned, cc)
		}
	}
	for _, want := range []string{"bob@example.com", "carol@example.com"} {
		if !strings.Contains(strings.ToLower(cc), want) {
			t.Fatalf("reply-all Cc missing %q: %q", want, cc)
		}
	}
}

func TestReplyAllCcDeduplicates(t *testing.T) {
	thread := api.MailThread{
		SenderEmail: "alice@example.com",
		To:          "Bob <bob@example.com>",
		Cc:          "bob@example.com",
	}
	cc := replyAllCc(&thread, "me@example.com")
	if got := strings.Count(strings.ToLower(cc), "bob@example.com"); got != 1 {
		t.Fatalf("expected bob to appear once, got %d in %q", got, cc)
	}
}

func TestQuotedReplyBodyPutsReplyOnTop(t *testing.T) {
	thread := api.MailThread{
		Sender:      "Alice",
		SenderEmail: "alice@example.com",
		Date:        time.Date(2026, 5, 20, 9, 30, 0, 0, time.UTC),
		Body:        "first line\nsecond line",
	}
	body := quotedReplyBody(&thread)

	if !strings.HasPrefix(body, "\n\n") {
		t.Fatalf("reply body should open with blank space for the new reply: %q", body)
	}
	if !strings.Contains(body, "Alice <alice@example.com> wrote:") {
		t.Fatalf("reply body missing attribution line: %q", body)
	}
	if !strings.Contains(body, "> first line") || !strings.Contains(body, "> second line") {
		t.Fatalf("reply body should quote every line of the original: %q", body)
	}
	attribIdx := strings.Index(body, "wrote:")
	quoteIdx := strings.Index(body, "> first line")
	if attribIdx == -1 || quoteIdx == -1 || attribIdx > quoteIdx {
		t.Fatalf("attribution line must sit above the quoted original: %q", body)
	}
}

// applyModalKey feeds one key through updateModal and folds the result back, so
// a test can drive the modal the way the event loop does.
func applyModalKey(m *Model, msg tea.KeyMsg) {
	next, _ := m.updateModal(msg)
	*m = next
}

func runes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestModalOpensInInsertMode(t *testing.T) {
	m := &Model{cfg: Config{VimMode: true}, width: 100, height: 32}
	m.openMailCompose(nil, mailComposeNew)
	if m.modal == nil {
		t.Fatal("compose modal should be open")
	}
	if m.modal.vimMode != vimModeInsert {
		t.Fatalf("modal should open in INSERT so typing works immediately, got %v", m.modal.vimMode)
	}
}

func TestModalEscDoesNotCloseInVimMode(t *testing.T) {
	m := &Model{cfg: Config{VimMode: true}, width: 100, height: 32}
	m.openMailCompose(nil, mailComposeNew)

	applyModalKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.modal == nil {
		t.Fatal("esc must not close the modal in vim mode — it should only leave INSERT")
	}
	if m.modal.vimMode != vimModeNormal {
		t.Fatalf("esc should switch INSERT -> NORMAL, got %v", m.modal.vimMode)
	}

	// ^q is the explicit cancel.
	applyModalKey(m, tea.KeyMsg{Type: tea.KeyCtrlQ})
	if m.modal != nil {
		t.Fatal("ctrl+q should close the modal")
	}
}

func TestModalTypingLandsInFocusedField(t *testing.T) {
	m := &Model{cfg: Config{VimMode: true}, width: 100, height: 32}
	m.openMailCompose(nil, mailComposeNew)

	applyModalKey(m, runes("hi@example.com"))
	if got := m.modal.field("to"); got != "hi@example.com" {
		t.Fatalf("typed text should land in the focused To field, got %q", got)
	}
}

func TestModalVimNormalEditsField(t *testing.T) {
	m := &Model{cfg: Config{VimMode: true}, width: 100, height: 32}
	m.openMailCompose(nil, mailComposeNew)

	applyModalKey(m, runes("junk"))
	applyModalKey(m, tea.KeyMsg{Type: tea.KeyEsc}) // INSERT -> NORMAL
	applyModalKey(m, runes("d"))
	applyModalKey(m, runes("d")) // dd clears the single-line field
	if got := m.modal.field("to"); got != "" {
		t.Fatalf("dd in NORMAL should clear the field, got %q", got)
	}
}

func TestModalReplyFocusesBody(t *testing.T) {
	m := &Model{cfg: Config{VimMode: true}, width: 100, height: 32}
	thread := &api.MailThread{ID: "t1", Sender: "Alice", SenderEmail: "alice@example.com"}
	m.openMailCompose(thread, mailComposeReply)
	if m.modal == nil {
		t.Fatal("reply modal should be open")
	}
	if got := m.modal.fields[m.modal.focus].Name; got != "body" {
		t.Fatalf("reply should focus the body field, focused %q", got)
	}
}
