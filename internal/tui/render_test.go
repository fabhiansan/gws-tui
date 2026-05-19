package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/fabhiansan/gws-tui/internal/api"
)

func newCardTestModel(t *testing.T) *Model {
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
	model.spaces = []api.Space{{Name: "spaces/card", DisplayName: "Card test"}}
	model.selected[FeatureChat] = 0
	return &model
}

func TestDisplayTextStripsInvisibleFormatControls(t *testing.T) {
	got := displayText("Dial\u200b in:\u202a (US) +1 803\u202c \u2066hidden\u2069 \ufeffdone")
	want := "Dial in: (US) +1 803 hidden done"
	if got != want {
		t.Fatalf("displayText() = %q, want %q", got, want)
	}
}

func TestTruncateIsANSIAndFormatControlAware(t *testing.T) {
	styled := "\x1b[7m0123456789\u200b\u202aabcdef\u202c\x1b[0m"
	got := truncate(styled, 8)
	if ansi.StringWidth(got) > 8 {
		t.Fatalf("truncate width = %d, want <= 8; value=%q", ansi.StringWidth(got), got)
	}
	for _, r := range []rune{'\u200b', '\u202a', '\u202c'} {
		if strings.ContainsRune(got, r) {
			t.Fatalf("truncate leaked format control %U in %q", r, got)
		}
	}
}

func TestChatDetailStripsInvisibleFormatControlsBeforeWrapping(t *testing.T) {
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
	model.width = 80
	model.height = 24
	model.resize()
	model.spaces = []api.Space{{Name: "spaces/bidi", DisplayName: "Bidi"}}
	model.selected[FeatureChat] = 0
	model.chatMessages = []api.ChatMessage{{
		ID:         "msg-bidi",
		Space:      "spaces/bidi",
		SenderName: "Alice",
		Text:       "Dial\u200b in:\u202a (US) +1 803-701\u202c\nOther numbers",
		CreateTime: time.Date(2026, 5, 19, 6, 2, 0, 0, time.UTC),
	}}

	got := model.chatDetail()
	for _, r := range []rune{'\u200b', '\u202a', '\u202c'} {
		if strings.ContainsRune(got, r) {
			t.Fatalf("chatDetail leaked format control %U in %q", r, got)
		}
	}
	if !strings.Contains(got, "Dial in: (US) +1 803-701") {
		t.Fatalf("chatDetail removed visible text: %q", got)
	}
}

func TestStripCardHTMLBasicTags(t *testing.T) {
	cases := []struct{ in, want string }{
		{"<b>Logo Aduan </b>", "Logo Aduan "},
		{"Review &#8594; <b>Done</b>", "Review → Done"},
		{"line one<br>line two", "line one\nline two"},
		{`<a href="https://x.com">click here</a>`, "click here"},
		{"<font color=\"#ff0000\">red</font>", "red"},
		{"plain", "plain"},
	}
	for _, tc := range cases {
		got := stripCardHTML(tc.in)
		if got != tc.want {
			t.Errorf("stripCardHTML(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRenderCardsBragaShape(t *testing.T) {
	// Real payload from the PM - lontara space, trimmed to one card.
	card := api.ChatCard{
		ID: "task-created-1",
		Header: &api.CardHeader{
			Title:    "New Task Created",
			Subtitle: "Task baru dibuat di project",
		},
		Widgets: []api.CardWidget{
			{
				Kind: api.CardWidgetDecoratedText,
				DecoratedText: &api.DecoratedTextWidget{
					TopLabel: "Task",
					Text:     "<b>Logo Aduan </b>",
					Icon:     &api.CardIcon{KnownIcon: "TICKET"},
				},
			},
			{
				Kind: api.CardWidgetDecoratedText,
				DecoratedText: &api.DecoratedTextWidget{
					TopLabel: "Created by",
					Text:     "Rheinanda Agista",
					Icon:     &api.CardIcon{KnownIcon: "PERSON"},
				},
			},
			{
				Kind:          api.CardWidgetDecoratedText,
				DecoratedText: &api.DecoratedTextWidget{TopLabel: "Column", Text: "To Do"},
			},
			{
				Kind: api.CardWidgetButtonList,
				ButtonList: &api.ButtonListWidget{Buttons: []api.CardButton{
					{Text: "View in App", URL: "https://ops.braga.co.id/pm/projects/abc"},
				}},
			},
		},
	}

	model := newCardTestModel(t)
	lines := model.renderCards([]api.ChatCard{card}, 80)
	got := strings.Join(lines, "\n")

	for _, want := range []string{
		"New Task Created",
		"Task baru dibuat di project",
		"Task",
		"Logo Aduan",
		"Created by",
		"Rheinanda Agista",
		"Column",
		"To Do",
		"[ View in App ]",
		"https://ops.braga.co.id/pm/projects/abc",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("renderCards missing %q in output:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<b>") {
		t.Errorf("renderCards leaked HTML tags:\n%s", got)
	}
}

func TestRenderCardsHandlesCardOnlyMessage(t *testing.T) {
	model := newCardTestModel(t)
	model.chatMessages = []api.ChatMessage{{
		ID:         "card-only",
		Space:      "spaces/card",
		SenderName: "Braga Bot",
		Text:       "",
		Cards: []api.ChatCard{{
			Header: &api.CardHeader{Title: "Status Changed", Subtitle: "Task dipindahkan"},
			Widgets: []api.CardWidget{
				{
					Kind: api.CardWidgetDecoratedText,
					DecoratedText: &api.DecoratedTextWidget{
						TopLabel: "Status",
						Text:     "Review &#8594; <b>Done</b>",
					},
				},
			},
		}},
		CreateTime: time.Date(2026, 5, 19, 6, 2, 0, 0, time.UTC),
	}}

	got := model.chatDetail()
	if !strings.Contains(got, "Status Changed") {
		t.Fatalf("card-only message dropped header:\n%s", got)
	}
	if !strings.Contains(got, "Review → Done") {
		t.Fatalf("card-only message dropped decoded status:\n%s", got)
	}
}

func TestRenderCardWidgetUnknownFallback(t *testing.T) {
	card := api.ChatCard{
		Widgets: []api.CardWidget{
			{Kind: api.CardWidgetUnknown, UnknownType: "selectionInput"},
		},
	}
	model := newCardTestModel(t)
	got := strings.Join(model.renderCards([]api.ChatCard{card}, 60), "\n")
	if !strings.Contains(got, "[unsupported selectionInput]") {
		t.Fatalf("expected unsupported widget marker, got:\n%s", got)
	}
}
