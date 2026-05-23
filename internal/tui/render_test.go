package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
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

func TestRenderStatusFitsNarrowWidth(t *testing.T) {
	dir := t.TempDir()
	model := New(Options{
		Client: newTestWorkspaceClient(),
		Config: Config{
			InitialFeature: "chat",
			StatePath:      filepath.Join(dir, "state.json"),
			DraftDir:       dir,
			NoColor:        true,
			// NoIcons stays false on purpose: the wide symbol icons are
			// exactly what made the row overflow onto a second line, so the
			// regression has to be exercised with icons enabled.
		},
	})
	model.spaces = []api.Space{
		{Name: "spaces/a", DisplayName: "A", Live: true},
		{Name: "spaces/b", DisplayName: "B", Live: true},
	}

	check := func(t *testing.T, label string, width int) {
		t.Helper()
		model.width = width
		model.height = 30
		model.resize()
		status := model.renderStatus(width)
		// statusWidth is the pessimistic terminal-cell estimate; if it
		// exceeds width the terminal wraps the row onto a second line.
		if got := statusWidth(status); got > width {
			t.Fatalf("%s width=%d: status visual width = %d, want <= %d:\n%q",
				label, width, got, width, ansi.Strip(status))
		}
	}

	for _, width := range []int{16, 20, 24, 30, 40, 60, 80, 100, 120} {
		check(t, "plain", width)
	}

	model.err = "upstream daemon is not responding, retry shortly"
	for _, width := range []int{16, 20, 24, 30, 40, 60, 80, 100, 120} {
		check(t, "with-error", width)
	}
}

func TestStatusBarReportsDaemonFetchActivity(t *testing.T) {
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
	model.width = 120
	model.height = 30
	model.resize()

	// A cold-start model still has its startup panes flagged loading, so the
	// status bar must announce that the daemon is fetching.
	status := ansi.Strip(model.renderStatus(120))
	if !strings.Contains(status, "fetching") {
		t.Fatalf("status bar should report daemon fetch activity at cold start:\n%s", status)
	}

	// Once every pane has its data the indicator clears.
	for _, f := range featureOrder {
		model.featureLoading[f] = false
	}
	model.loading = false
	model.chatLoading = false
	model.docLoadingID = ""
	status = ansi.Strip(model.renderStatus(120))
	if strings.Contains(status, "fetching") {
		t.Fatalf("status bar should be idle once fetches finish:\n%s", status)
	}

	// A single in-flight refresh names the feature being fetched.
	model.feature = FeatureMail
	model.loading = true
	if summary, active := model.fetchActivity(); !active || summary != "Mail" {
		t.Fatalf("refresh should name the feature being fetched, got %q active=%v", summary, active)
	}

	// A chat space load is reported even though m.loading stays false.
	model.feature = FeatureChat
	model.loading = false
	model.chatLoading = true
	if summary, active := model.fetchActivity(); !active || summary != "Chat" {
		t.Fatalf("chat space load should be reported, got %q active=%v", summary, active)
	}

	// Concurrent fetches collapse to a count so the status row never overflows.
	model.featureLoading[FeatureCalendar] = true
	model.featureLoading[FeatureDrive] = true
	if summary, active := model.fetchActivity(); !active || summary != "3 sections" {
		t.Fatalf("concurrent fetches should collapse to a count, got %q active=%v", summary, active)
	}
}

func TestDocsDefaultRenderUsesSingleListPane(t *testing.T) {
	dir := t.TempDir()
	model := New(Options{
		Client: newTestWorkspaceClient(),
		Config: Config{
			InitialFeature: "docs",
			StatePath:      filepath.Join(dir, "state.json"),
			DraftDir:       dir,
			NoColor:        true,
			NoIcons:        true,
		},
	})
	model.feature = FeatureDocs
	model.cacheLoaded = true
	model.loading = false
	model.width = 100
	model.height = 30
	model.resize()
	model.docFiles = []api.DriveFile{{ID: "doc-1", Name: "Launch notes"}}
	model.doc = api.DocDocument{ID: "doc-1", Title: "Launch notes", Body: "Launch plan"}

	view := ansi.Strip(model.View())
	if !strings.Contains(view, "[1]-Docs") {
		t.Fatalf("Docs list pane missing:\n%s", view)
	}
	if strings.Contains(view, "[2]-") || strings.Contains(view, "Launch plan") {
		t.Fatalf("Docs default view should not render detail pane:\n%s", view)
	}

	model.focusedPane = paneDetail
	model.updateDetailContent()
	view = ansi.Strip(model.View())
	if !strings.Contains(view, "[2]-Launch notes") || !strings.Contains(view, "Launch plan") {
		t.Fatalf("Docs detail pane did not replace list:\n%s", view)
	}
}

func TestDocsDetailRendersStructuredBlocks(t *testing.T) {
	dir := t.TempDir()
	model := New(Options{
		Client: newTestWorkspaceClient(),
		Config: Config{
			InitialFeature: "docs",
			StatePath:      filepath.Join(dir, "state.json"),
			DraftDir:       dir,
			NoColor:        true,
			NoIcons:        true,
		},
	})
	model.feature = FeatureDocs
	model.focusedPane = paneDetail
	model.cacheLoaded = true
	model.loading = false
	model.width = 100
	model.height = 30
	model.resize()
	model.docFiles = []api.DriveFile{{ID: "doc-1", Name: "Launch notes"}}
	image := api.Attachment{Name: "Architecture", ContentType: "image/png", URL: "https://example.com/architecture.png"}
	model.doc = api.DocDocument{
		ID:    "doc-1",
		Title: "Launch notes",
		Body:  "Launch notes\n- Review risks\nRequirement | Approved\n[image: Architecture]",
		Blocks: []api.DocBlock{
			{Kind: api.DocBlockTitle, Text: "Launch notes", Inlines: []api.DocInline{{Text: "Launch notes", Bold: true}}},
			{Kind: api.DocBlockListItem, Text: "Review risks", Inlines: []api.DocInline{{Text: "Review risks"}}},
			{Kind: api.DocBlockTable, Rows: [][]string{{"Requirement", "Status"}, {"Review", "Approved"}}},
			{Kind: api.DocBlockImage, Text: "Architecture", Attachment: &image},
		},
		Attachments: []api.Attachment{image},
	}

	model.updateDetailContent()
	view := ansi.Strip(model.View())
	for _, want := range []string{"Launch notes", "- Review risks", "Requirement", "Status", "[image] Architecture"} {
		if !strings.Contains(view, want) {
			t.Fatalf("Docs rich detail missing %q:\n%s", want, view)
		}
	}
}

func TestRenderMailSidebarShowsFoldersAndActiveMarker(t *testing.T) {
	dir := t.TempDir()
	model := New(Options{
		Client: newTestWorkspaceClient(),
		Config: Config{
			InitialFeature: "mail",
			StatePath:      filepath.Join(dir, "state.json"),
			DraftDir:       dir,
			NoColor:        true,
			NoIcons:        true,
		},
	})
	model.feature = FeatureMail
	model.width = 100
	model.height = 30
	model.resize()
	model.mailFolder = "Starred"
	model.mailLabels = []api.MailLabel{{Name: "Receipts", LabelIDs: []string{"Label_9"}}}

	sidebar := ansi.Strip(model.renderMailSidebar(20, 24))
	for _, folder := range []string{"Inbox", "Starred", "Important", "Sent", "Drafts", "Spam", "Trash", "All Mail"} {
		if !strings.Contains(sidebar, folder) {
			t.Errorf("sidebar missing system folder %q:\n%s", folder, sidebar)
		}
	}
	if !strings.Contains(sidebar, "Receipts") {
		t.Errorf("sidebar missing custom label:\n%s", sidebar)
	}
	if !strings.Contains(sidebar, "Labels") {
		t.Errorf("sidebar missing Labels header:\n%s", sidebar)
	}
	// The active folder carries the '>' marker (ASCII icon mode).
	if !strings.Contains(sidebar, "> Starred") {
		t.Errorf("active folder Starred not marked:\n%s", sidebar)
	}
}

func TestMailDetailSanitizesHTMLEntityArtifacts(t *testing.T) {
	dir := t.TempDir()
	model := New(Options{
		Client: newTestWorkspaceClient(),
		Config: Config{
			InitialFeature: "mail",
			StatePath:      filepath.Join(dir, "state.json"),
			DraftDir:       dir,
			NoColor:        true,
			NoIcons:        true,
		},
	})
	model.feature = FeatureMail
	model.width = 100
	model.height = 30
	model.resize()
	model.mailThreads = []api.MailThread{{
		ID:          "thread-html",
		Sender:      "Shutterstock",
		SenderEmail: "emktng.shutterstock.com",
		Subject:     "Konten terbaru",
		Date:        time.Date(2026, 5, 21, 10, 14, 0, 0, time.UTC),
		Labels:      []string{"CATEGORY_PROMOTIONS", "UNREAD", "INBOX"},
		Body:        "Shutterstock<br>&zwnj; &zwnj; &zwnj;<div><b>Offer</b> ready</div>",
	}}

	got := ansi.Strip(model.mailDetail())
	for _, leaked := range []string{"&zwnj;", "<br", "<div", "<b>"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("mail detail leaked %q:\n%s", leaked, got)
		}
	}
	if !strings.Contains(got, "Shutterstock") || !strings.Contains(got, "Offer ready") {
		t.Fatalf("mail detail dropped visible body text:\n%s", got)
	}
}

func TestMailBodyDisplayTextPreservesPlainAngleText(t *testing.T) {
	got := mailBodyDisplayText("2 < 3 and keep <important> marker &amp; text")
	want := "2 < 3 and keep <important> marker & text"
	if got != want {
		t.Fatalf("mail body plain text changed:\ngot  %q\nwant %q", got, want)
	}
}

func TestWrapDetailLinesAccountsForAmbiguousWidth(t *testing.T) {
	// The mail action hint is separated by · (U+00B7), an East Asian
	// "ambiguous" glyph some terminals draw two cells wide. ansi.Wrap
	// measures it as one cell, so without an ambiguous-width budget the
	// wrapped line still overflows the viewport and gets clipped.
	hint := "R reply · f forward · e archive · # trash · s star · u read/unread"
	width := lipgloss.Width(hint) // fits exactly by the one-cell measurement

	out := wrapDetailLines([]string{hint}, width)
	if len(out) < 2 {
		t.Fatalf("hint with ambiguous · should wrap on a wide-ambiguous budget, got %d line(s): %q", len(out), out)
	}
	for _, line := range out {
		if w := lipgloss.Width(line) + ambiguousDrift(line); w > width {
			t.Fatalf("wrapped line still overflows a wide-ambiguous terminal: %d > %d\n%q", w, width, line)
		}
	}

	// A pure-ASCII line carries no ambiguous drift, so it must not wrap
	// early — the budget only kicks in for glyphs that actually drift.
	ascii := strings.Repeat("a", width)
	if got := wrapDetailLines([]string{ascii}, width); len(got) != 1 {
		t.Fatalf("ASCII line at exact width should stay on one line, got %d", len(got))
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
