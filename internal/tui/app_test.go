package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fabhiansan/gws-tui/internal/api"
)

func TestModelInitialRenderContainsFeatureTabs(t *testing.T) {
	model := New(Options{
		Client: newTestWorkspaceClient(),
		Config: Config{
			InitialFeature: "chat",
			StatePath:      t.TempDir() + "/state.json",
			DraftDir:       t.TempDir(),
		},
		Version: "test",
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	model = updated.(Model)
	msg := model.loadAllCmd()().(loadedMsg)
	updated, _ = model.Update(msg)
	model = updated.(Model)

	view := model.View()
	for _, want := range []string{"Chat", "Mail", "Calendar", "Meet", "#engineering"} {
		if !strings.Contains(view, want) {
			t.Fatalf("render missing %q:\n%s", want, view)
		}
	}
}

func TestChatSelectionDoesNotBlockOnMessageLoad(t *testing.T) {
	client := &blockingMessagesClient{
		WorkspaceClient: newTestWorkspaceClient(),
		started:         make(chan struct{}),
		release:         make(chan struct{}),
	}
	model := New(Options{
		Client: client,
		Config: Config{
			InitialFeature: "chat",
			StatePath:      t.TempDir() + "/state.json",
			DraftDir:       t.TempDir(),
		},
	})
	model.spaces = []api.Space{
		{Name: "spaces/engineering", DisplayName: "#engineering"},
		{Name: "spaces/design", DisplayName: "#design"},
	}
	model.chatMessages = []api.ChatMessage{{ID: "old", Space: "spaces/engineering", Text: "old"}}
	model.selected[FeatureChat] = 0

	done := make(chan struct {
		model tea.Model
		cmd   tea.Cmd
	}, 1)
	go func() {
		updated, cmd := model.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'j'}}))
		done <- struct {
			model tea.Model
			cmd   tea.Cmd
		}{model: updated, cmd: cmd}
	}()

	select {
	case result := <-done:
		updated := result.model.(Model)
		if updated.selected[FeatureChat] != 1 {
			t.Fatalf("selection did not move: %d", updated.selected[FeatureChat])
		}
		if !updated.chatLoading || updated.chatLoadSpace != "spaces/design" {
			t.Fatalf("expected async chat loading for selected space, got loading=%v space=%q", updated.chatLoading, updated.chatLoadSpace)
		}
		if len(updated.chatMessages) != 0 {
			t.Fatalf("stale messages should be cleared while loading, got %#v", updated.chatMessages)
		}
		if result.cmd == nil {
			t.Fatal("expected async message load command")
		}
	case <-client.started:
		close(client.release)
		<-done
		t.Fatal("Update blocked by calling ChatMessages synchronously")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Update did not return promptly")
	}
}

func TestModelHydratesWorkspaceCacheWithoutInitialLoad(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "cache.json")
	statePath := filepath.Join(dir, "state.json")
	if err := savePersistedState(statePath, persistedState{LastFeature: "chat", LastSpace: "spaces/design", Selections: map[string]int{}}); err != nil {
		t.Fatal(err)
	}
	if err := saveWorkspaceCache(cachePath, workspaceCache{
		Spaces: []api.Space{
			{Name: "spaces/engineering", DisplayName: "#engineering"},
			{Name: "spaces/design", DisplayName: "#design"},
		},
		ChatMessagesBySpace: map[string]api.Page[api.ChatMessage]{
			"spaces/design": {
				Items: []api.ChatMessage{{ID: "cached-design", Space: "spaces/design", Text: "cached"}},
			},
		},
		MailLabels: []api.MailLabel{{Name: "Inbox"}},
		MailThreads: api.Page[api.MailThread]{
			Items: []api.MailThread{{ID: "mail-1", Subject: "Cached mail"}},
		},
		Events: api.Page[api.CalendarEvent]{
			Items: []api.CalendarEvent{{ID: "event-1", Summary: "Cached event"}},
		},
		MeetSpaces: []api.MeetSpace{{Name: "spaces/meet-cached"}},
	}); err != nil {
		t.Fatal(err)
	}

	model := New(Options{
		Client: newTestWorkspaceClient(),
		Config: Config{
			InitialFeature: "chat",
			StatePath:      statePath,
			CachePath:      cachePath,
			DraftDir:       dir,
		},
	})

	if model.loading {
		t.Fatal("cached model should not start in loading state")
	}
	if got := model.selectedSpace().Name; got != "spaces/design" {
		t.Fatalf("expected cached last space, got %q", got)
	}
	if len(model.chatMessages) != 1 || model.chatMessages[0].ID != "cached-design" {
		t.Fatalf("cached chat messages were not hydrated: %#v", model.chatMessages)
	}
	if len(model.mailThreads) != 1 || len(model.events) != 1 || len(model.meetSpaces) != 1 {
		t.Fatalf("cached workspace panes were not hydrated: mail=%d events=%d meet=%d", len(model.mailThreads), len(model.events), len(model.meetSpaces))
	}

	msg := model.Init()()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected startup batch without loadAll, got %T", msg)
	}
	if len(batch) != 2 {
		t.Fatalf("cached startup should only start spinner and autosave, got %d commands", len(batch))
	}
}

func TestChatSelectionUsesCachedMessages(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "cache.json")
	if err := saveWorkspaceCache(cachePath, workspaceCache{
		Spaces: []api.Space{
			{Name: "spaces/engineering", DisplayName: "#engineering"},
			{Name: "spaces/design", DisplayName: "#design"},
		},
		ChatMessagesBySpace: map[string]api.Page[api.ChatMessage]{
			"spaces/design": {
				Items:         []api.ChatMessage{{ID: "cached-design", Space: "spaces/design", Text: "cached"}},
				NextPageToken: "older",
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	client := &countingMessagesClient{WorkspaceClient: newTestWorkspaceClient()}
	model := New(Options{
		Client: client,
		Config: Config{
			InitialFeature: "chat",
			StatePath:      filepath.Join(dir, "state.json"),
			CachePath:      cachePath,
			DraftDir:       dir,
		},
	})
	model.selected[FeatureChat] = 0
	model.chatMessages = nil

	updated, cmd := model.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'j'}}))
	model = updated.(Model)
	if cmd != nil {
		t.Fatal("expected cached chat switch to skip async load command")
	}
	if client.calls != 0 {
		t.Fatalf("cached chat switch should not call ChatMessages, got %d calls", client.calls)
	}
	if model.chatLoading {
		t.Fatal("cached chat switch should not enter loading state")
	}
	if len(model.chatMessages) != 1 || model.chatMessages[0].ID != "cached-design" || model.chatOlder != "older" {
		t.Fatalf("cached messages not applied: messages=%#v older=%q", model.chatMessages, model.chatOlder)
	}
}

func TestChatSlashStartsLiveFuzzySpaceFilter(t *testing.T) {
	model := New(Options{
		Client: newTestWorkspaceClient(),
		Config: Config{
			InitialFeature: "chat",
			StatePath:      t.TempDir() + "/state.json",
			DraftDir:       t.TempDir(),
		},
	})
	model.feature = FeatureChat
	model.focusedPane = paneList
	model.spaces = []api.Space{
		{Name: "spaces/engineering", DisplayName: "#engineering"},
		{Name: "spaces/design-reviews", DisplayName: "#Design Reviews"},
		{Name: "spaces/release", DisplayName: "#release"},
	}
	model.chatMessages = []api.ChatMessage{{ID: "eng-1", Space: "spaces/engineering", Text: "current"}}

	updated, cmd := model.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if cmd != nil {
		t.Fatalf("opening filter should not return a command, got %v", cmd)
	}
	if !updated.spaceFilterActive || updated.modal != nil {
		t.Fatalf("slash should start inline space filter, active=%v modal=%#v", updated.spaceFilterActive, updated.modal)
	}

	updated, cmd = updated.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if cmd != nil {
		t.Fatalf("typing filter should stay local, got command %v", cmd)
	}
	updated, cmd = updated.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd != nil {
		t.Fatalf("typing fuzzy filter should stay local, got command %v", cmd)
	}
	matches := updated.visibleSpaces()
	if len(matches) != 1 || matches[0].Name != "spaces/design-reviews" {
		t.Fatalf("expected fuzzy dr to match design reviews, got %#v", matches)
	}
	if got := updated.selectedSpace().Name; got != "spaces/design-reviews" {
		t.Fatalf("expected selected match to follow filter, got %q", got)
	}

	updated, cmd = updated.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should open the filtered selected space")
	}
	if updated.spaceFilterActive {
		t.Fatal("enter should leave filter mode")
	}
	if !updated.chatLoading || updated.chatLoadSpace != "spaces/design-reviews" {
		t.Fatalf("expected selected filtered space to load, loading=%v space=%q", updated.chatLoading, updated.chatLoadSpace)
	}
}

func TestChatSpaceFilterEscClearsAndRestoresSelection(t *testing.T) {
	model := New(Options{
		Client: newTestWorkspaceClient(),
		Config: Config{
			InitialFeature: "chat",
			StatePath:      t.TempDir() + "/state.json",
			DraftDir:       t.TempDir(),
		},
	})
	model.feature = FeatureChat
	model.focusedPane = paneList
	model.spaces = []api.Space{
		{Name: "spaces/engineering", DisplayName: "#engineering"},
		{Name: "spaces/design-reviews", DisplayName: "#Design Reviews"},
	}
	model.selected[FeatureChat] = 0

	updated, _ := model.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	updated, _ = updated.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if got := updated.selectedSpace().Name; got != "spaces/design-reviews" {
		t.Fatalf("filter setup selected wrong space: %q", got)
	}

	updated, cmd := updated.updateKey(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatalf("esc should not load or fetch, got command %v", cmd)
	}
	if updated.spaceFilterActive || updated.spaceFilter != "" {
		t.Fatalf("esc should clear filter state, active=%v query=%q", updated.spaceFilterActive, updated.spaceFilter)
	}
	if got := updated.selectedSpace().Name; got != "spaces/engineering" {
		t.Fatalf("esc should restore original selection, got %q", got)
	}
}

func TestUpdateDetailContentSkipsUnchangedDetailRender(t *testing.T) {
	dir := t.TempDir()
	model := New(Options{
		Client: newTestWorkspaceClient(),
		Config: Config{
			InitialFeature: "chat",
			StatePath:      filepath.Join(dir, "state.json"),
			DraftDir:       dir,
			NoColor:        true,
		},
	})
	model.width = 100
	model.height = 32
	model.resize()
	model.spaces = []api.Space{{Name: "spaces/engineering", DisplayName: "#engineering"}}
	model.selected[FeatureChat] = 0
	model.chatMessages = []api.ChatMessage{{
		ID:         "msg-1",
		Space:      "spaces/engineering",
		SenderID:   "users/alice",
		SenderName: "Alice",
		Text:       "initial message",
		CreateTime: time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC),
	}}

	model.updateDetailContent()
	firstKey := model.detailRenderKey
	model.detail.SetContent("sentinel")
	model.toast = "status changed"
	model.updateDetailContent()
	if model.detailRenderKey != firstKey {
		t.Fatalf("status-only update should keep render key: got %q want %q", model.detailRenderKey, firstKey)
	}
	if !strings.Contains(model.detail.View(), "sentinel") {
		t.Fatalf("unchanged detail should not be rebuilt, view=%q", model.detail.View())
	}

	model.chatMessages[0].Text = "changed message"
	model.updateDetailContent()
	if strings.Contains(model.detail.View(), "sentinel") || !strings.Contains(model.detail.View(), "changed message") {
		t.Fatalf("changed detail data should rebuild viewport, view=%q", model.detail.View())
	}
}

func TestRefreshKeyOnlyReloadsSelectedChatSpace(t *testing.T) {
	client := &countingWorkspaceClient{WorkspaceClient: newTestWorkspaceClient()}
	model := New(Options{
		Client: client,
		Config: Config{
			InitialFeature: "chat",
			StatePath:      t.TempDir() + "/state.json",
			DraftDir:       t.TempDir(),
		},
	})
	model.spaces = []api.Space{
		{Name: "spaces/engineering", DisplayName: "#engineering"},
		{Name: "spaces/design", DisplayName: "#design"},
	}
	model.selected[FeatureChat] = 1
	model.chatMessages = []api.ChatMessage{{ID: "stale", Space: "spaces/engineering", Text: "stale"}}

	updated, cmd := model.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = updated
	if cmd == nil {
		t.Fatal("expected selected chat refresh command")
	}
	if !model.loading || !model.chatLoading || model.chatLoadSpace != "spaces/design" {
		t.Fatalf("expected loading selected space, loading=%v chatLoading=%v space=%q", model.loading, model.chatLoading, model.chatLoadSpace)
	}
	if len(model.chatMessages) != 0 {
		t.Fatalf("stale chat messages should clear during refresh, got %#v", model.chatMessages)
	}

	msg := cmd()
	chatMsg, ok := msg.(chatLoadedMsg)
	if !ok {
		t.Fatalf("expected chatLoadedMsg, got %T", msg)
	}
	if !chatMsg.refresh {
		t.Fatal("selected chat refresh should mark the chatLoadedMsg as refresh")
	}
	updatedModel, _ := model.Update(chatMsg)
	model = updatedModel.(Model)

	if client.chatMessagesCalls != 1 || client.lastChatSpace != "spaces/design" {
		t.Fatalf("expected one ChatMessages call for selected space, calls=%d space=%q", client.chatMessagesCalls, client.lastChatSpace)
	}
	if client.authStatusCalls != 0 || client.chatSpacesCalls != 0 || client.mailLabelsCalls != 0 || client.mailThreadsCalls != 0 || client.calendarEventsCalls != 0 || client.meetSpacesCalls != 0 {
		t.Fatalf("chat refresh should not refetch other panes: auth=%d spaces=%d labels=%d mail=%d calendar=%d meet=%d",
			client.authStatusCalls, client.chatSpacesCalls, client.mailLabelsCalls, client.mailThreadsCalls, client.calendarEventsCalls, client.meetSpacesCalls)
	}
	if model.toast != "chat refreshed" {
		t.Fatalf("expected chat refreshed toast, got %q", model.toast)
	}
}

func TestChatRefreshUpdatesOriginalSpaceAfterSelectionMoves(t *testing.T) {
	client := &countingChatReaderClient{
		countingWorkspaceClient: &countingWorkspaceClient{WorkspaceClient: newTestWorkspaceClient()},
	}
	model := New(Options{
		Client: client,
		Config: Config{
			InitialFeature: "chat",
			StatePath:      t.TempDir() + "/state.json",
			DraftDir:       t.TempDir(),
			Daemon:         true,
		},
	})
	model.spaces = []api.Space{
		{Name: "spaces/engineering", DisplayName: "#engineering"},
		{Name: "spaces/design", DisplayName: "#design", Unread: true},
	}
	model.selected[FeatureChat] = 1
	model.chatMessages = []api.ChatMessage{{ID: "stale-design", Space: "spaces/design", Text: "stale"}}

	updated, cmd := model.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = updated
	if cmd == nil {
		t.Fatal("expected selected chat refresh command")
	}
	model.selected[FeatureChat] = 0
	model.chatMessages = []api.ChatMessage{{ID: "eng-visible", Space: "spaces/engineering", Text: "visible engineering"}}

	msg := cmd()
	updatedModel, sideEffects := model.Update(msg)
	model = updatedModel.(Model)

	cached := model.cache.ChatMessagesBySpace["spaces/design"]
	if len(cached.Items) != 2 || cached.Items[0].Space != "spaces/design" {
		t.Fatalf("background refresh should update original space cache, got %#v", cached.Items)
	}
	if len(model.chatMessages) != 1 || model.chatMessages[0].ID != "eng-visible" {
		t.Fatalf("background refresh should not overwrite current visible space, got %#v", model.chatMessages)
	}
	if model.spaces[1].Unread {
		t.Fatalf("background refresh should clear original space unread badge: %#v", model.spaces)
	}

	runBatchSideEffects(t, sideEffects)
	if client.readCalls != 1 || client.lastReadSpace != "spaces/design" {
		t.Fatalf("expected daemon read marker for refreshed space, calls=%d space=%q", client.readCalls, client.lastReadSpace)
	}
}

func TestDaemonNotifyEventMarksOtherSpaceUnread(t *testing.T) {
	model := New(Options{
		Client: newTestWorkspaceClient(),
		Config: Config{
			InitialFeature: "chat",
			StatePath:      t.TempDir() + "/state.json",
			DraftDir:       t.TempDir(),
			Daemon:         true,
		},
	})
	model.spaces = []api.Space{
		{Name: "spaces/engineering", DisplayName: "#engineering"},
		{Name: "spaces/design", DisplayName: "#design"},
	}
	model.selected[FeatureChat] = 0
	payload, err := json.Marshal(map[string]string{"space": "spaces/design"})
	if err != nil {
		t.Fatal(err)
	}

	updated, _ := model.Update(daemonEventMsg{
		event: api.DaemonEvent{Topic: "notify", Payload: payload},
	})
	model = updated.(Model)

	if !model.spaces[1].Unread {
		t.Fatalf("notify event should mark other space unread: %#v", model.spaces)
	}
	if model.spaces[0].Unread {
		t.Fatalf("notify event should not mark selected space unread: %#v", model.spaces)
	}
	if model.toast != "new chat message" {
		t.Fatalf("expected chat toast, got %q", model.toast)
	}
}

func TestDaemonChatMessageEventHydratesOtherSpaceCache(t *testing.T) {
	client := &countingMessagesClient{WorkspaceClient: newTestWorkspaceClient()}
	model := New(Options{
		Client: client,
		Config: Config{
			InitialFeature: "chat",
			StatePath:      t.TempDir() + "/state.json",
			DraftDir:       t.TempDir(),
			Daemon:         true,
		},
	})
	model.spaces = []api.Space{
		{Name: "spaces/engineering", DisplayName: "#engineering"},
		{Name: "spaces/design", DisplayName: "#design"},
	}
	model.selected[FeatureChat] = 0
	model.chatMessages = []api.ChatMessage{{ID: "eng-1", Space: "spaces/engineering", Text: "current space"}}
	model.rememberChatPage("spaces/design", api.Page[api.ChatMessage]{
		Items:         []api.ChatMessage{{ID: "design-old", Space: "spaces/design", Text: "old cached"}},
		NextPageToken: "older",
	})
	incoming := api.ChatMessage{
		ID:         "design-new",
		Name:       "spaces/design/messages/design-new",
		Space:      "spaces/design",
		SenderID:   "users/alice",
		SenderName: "Alice",
		Text:       "new from daemon",
		CreateTime: time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC),
	}
	payload, err := json.Marshal(incoming)
	if err != nil {
		t.Fatal(err)
	}

	updated, _ := model.Update(daemonEventMsg{
		event: api.DaemonEvent{Topic: "chat.message", Payload: payload},
	})
	model = updated.(Model)

	if !model.spaces[1].Unread {
		t.Fatalf("chat.message event should mark other space unread: %#v", model.spaces)
	}
	cached := model.cache.ChatMessagesBySpace["spaces/design"]
	if len(cached.Items) != 2 || cached.Items[1].ID != "design-new" {
		t.Fatalf("incoming daemon message was not cached for unopened space: %#v", cached.Items)
	}

	updated, _ = model.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'j'}}))
	model = updated.(Model)
	if client.calls != 0 {
		t.Fatalf("cached daemon message should avoid ChatMessages fetch, got %d calls", client.calls)
	}
	if len(model.chatMessages) != 2 || model.chatMessages[1].Text != "new from daemon" {
		t.Fatalf("cached daemon message should render after opening space, got %#v", model.chatMessages)
	}
}

func TestCachedChatSelectionMarksSpaceRead(t *testing.T) {
	client := &countingChatReaderClient{
		countingWorkspaceClient: &countingWorkspaceClient{WorkspaceClient: newTestWorkspaceClient()},
	}
	model := New(Options{
		Client: client,
		Config: Config{
			InitialFeature: "chat",
			StatePath:      t.TempDir() + "/state.json",
			DraftDir:       t.TempDir(),
			Daemon:         true,
		},
	})
	model.spaces = []api.Space{
		{Name: "spaces/engineering", DisplayName: "#engineering"},
		{Name: "spaces/design", DisplayName: "#design", Unread: true},
	}
	model.selected[FeatureChat] = 0
	model.chatMessages = []api.ChatMessage{{ID: "eng-1", Space: "spaces/engineering", Text: "current"}}
	model.rememberChatPage("spaces/design", api.Page[api.ChatMessage]{
		Items: []api.ChatMessage{{ID: "design-cached", Space: "spaces/design", Text: "cached unread"}},
	})

	updated, cmd := model.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'j'}}))
	model = updated.(Model)

	if model.spaces[1].Unread {
		t.Fatalf("cached space open should clear unread badge: %#v", model.spaces)
	}
	if len(model.chatMessages) != 1 || model.chatMessages[0].ID != "design-cached" {
		t.Fatalf("cached space open should render cached messages, got %#v", model.chatMessages)
	}
	runBatchSideEffects(t, cmd)
	if client.readCalls != 1 || client.lastReadSpace != "spaces/design" {
		t.Fatalf("expected daemon read marker for cached-open space, calls=%d space=%q", client.readCalls, client.lastReadSpace)
	}
}

func TestDaemonEventCommandReusesStreamForBufferedEvents(t *testing.T) {
	eventCh := make(chan api.DaemonEvent, 2)
	unusedCh := make(chan api.DaemonEvent, 1)
	client := &rotatingEventsClient{
		WorkspaceClient: newTestWorkspaceClient(),
		channels:        []chan api.DaemonEvent{eventCh, unusedCh},
	}
	model := New(Options{
		Client: client,
		Config: Config{
			InitialFeature: "chat",
			StatePath:      t.TempDir() + "/state.json",
			DraftDir:       t.TempDir(),
			Daemon:         true,
		},
	})
	model.spaces = []api.Space{
		{Name: "spaces/engineering", DisplayName: "#engineering"},
		{Name: "spaces/design", DisplayName: "#design"},
	}
	model.selected[FeatureChat] = 0

	notifyPayload, err := json.Marshal(map[string]string{"space": "spaces/design"})
	if err != nil {
		t.Fatal(err)
	}
	incoming := api.ChatMessage{
		ID:         "design-new",
		Name:       "spaces/design/messages/design-new",
		Space:      "spaces/design",
		SenderID:   "users/alice",
		SenderName: "Alice",
		Text:       "new from daemon",
		CreateTime: time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC),
	}
	messagePayload, err := json.Marshal(incoming)
	if err != nil {
		t.Fatal(err)
	}
	eventCh <- api.DaemonEvent{Topic: "notify", Payload: notifyPayload}
	eventCh <- api.DaemonEvent{Topic: "chat.message", Payload: messagePayload}

	first := runTestCmd(t, model.daemonEventCmd()).(daemonEventMsg)
	if first.event.Topic != "notify" {
		t.Fatalf("expected first buffered event to be notify, got %#v", first.event)
	}
	updated, _ := model.Update(first)
	model = updated.(Model)

	second := runTestCmd(t, model.daemonEventCmd()).(daemonEventMsg)
	if second.event.Topic != "chat.message" {
		t.Fatalf("expected second buffered event to reuse existing stream, got %#v", second.event)
	}
	if client.calls != 1 {
		t.Fatalf("daemon event command should reuse event stream, SubscribeEvents calls=%d", client.calls)
	}
	updated, _ = model.Update(second)
	model = updated.(Model)

	cached := model.cache.ChatMessagesBySpace["spaces/design"]
	if len(cached.Items) != 1 || cached.Items[0].ID != "design-new" {
		t.Fatalf("chat.message from reused stream was not cached: %#v", cached.Items)
	}
}

func TestRealtimeChatMessageDedupesDuplicateTriggers(t *testing.T) {
	model := New(Options{
		Client: newTestWorkspaceClient(),
		Config: Config{
			InitialFeature: "chat",
			StatePath:      t.TempDir() + "/state.json",
			DraftDir:       t.TempDir(),
		},
	})
	model.spaces = []api.Space{{Name: "spaces/engineering", DisplayName: "#engineering", Live: true}}
	model.selected[FeatureChat] = 0
	msg := api.ChatMessage{
		ID:         "live-1",
		Name:       "spaces/engineering/messages/live-1",
		Space:      "spaces/engineering",
		SenderID:   "users/alice",
		SenderName: "Alice",
		Text:       "masukk ke notiff",
		CreateTime: time.Date(2026, 5, 19, 6, 51, 20, 153455000, time.UTC),
	}

	updated, _ := model.Update(realtimeMsg{message: msg})
	model = updated.(Model)
	updated, _ = model.Update(realtimeMsg{message: msg})
	model = updated.(Model)

	if len(model.chatMessages) != 1 {
		t.Fatalf("duplicate live trigger should render one message, got %#v", model.chatMessages)
	}
	if model.chatMessages[0].Text != "masukk ke notiff" {
		t.Fatalf("unexpected message kept: %#v", model.chatMessages[0])
	}
}

func TestDaemonChatReadEventClearsSpaceUnread(t *testing.T) {
	model := New(Options{
		Client: newTestWorkspaceClient(),
		Config: Config{
			InitialFeature: "chat",
			StatePath:      t.TempDir() + "/state.json",
			DraftDir:       t.TempDir(),
			Daemon:         true,
		},
	})
	model.spaces = []api.Space{
		{Name: "spaces/design", DisplayName: "#design", Unread: true},
	}
	payload, err := json.Marshal(map[string]string{"space": "spaces/design"})
	if err != nil {
		t.Fatal(err)
	}

	updated, _ := model.Update(daemonEventMsg{
		event: api.DaemonEvent{Topic: "chat.read", Payload: payload},
	})
	model = updated.(Model)

	if model.spaces[0].Unread {
		t.Fatalf("chat.read event should clear unread badge: %#v", model.spaces)
	}
}

func TestRefreshKeyOnlyReloadsMailFeature(t *testing.T) {
	client := &countingWorkspaceClient{WorkspaceClient: newTestWorkspaceClient()}
	model := New(Options{
		Client: client,
		Config: Config{
			InitialFeature: "mail",
			StatePath:      t.TempDir() + "/state.json",
			DraftDir:       t.TempDir(),
		},
	})
	model.feature = FeatureMail
	model.search = "deck"

	updated, cmd := model.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = updated
	if cmd == nil {
		t.Fatal("expected mail refresh command")
	}
	msg := cmd()
	refreshMsg, ok := msg.(featureRefreshedMsg)
	if !ok {
		t.Fatalf("expected featureRefreshedMsg, got %T", msg)
	}
	updatedModel, _ := model.Update(refreshMsg)
	model = updatedModel.(Model)

	if client.mailLabelsCalls != 1 || client.mailThreadsCalls != 1 {
		t.Fatalf("expected mail labels and threads refresh, labels=%d threads=%d", client.mailLabelsCalls, client.mailThreadsCalls)
	}
	if client.lastMailQuery.Label != "All Mail" || client.lastMailQuery.Search != "deck" {
		t.Fatalf("expected current mail search to refresh, got query=%#v", client.lastMailQuery)
	}
	if client.authStatusCalls != 0 || client.chatSpacesCalls != 0 || client.chatMessagesCalls != 0 || client.calendarEventsCalls != 0 || client.meetSpacesCalls != 0 {
		t.Fatalf("mail refresh should not refetch other panes: auth=%d spaces=%d chat=%d calendar=%d meet=%d",
			client.authStatusCalls, client.chatSpacesCalls, client.chatMessagesCalls, client.calendarEventsCalls, client.meetSpacesCalls)
	}
	if model.toast != "mail refreshed" {
		t.Fatalf("expected mail refreshed toast, got %q", model.toast)
	}
}

func TestChatCtrlXClearsPendingAttachments(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.png")
	pathB := filepath.Join(dir, "b.png")
	for _, p := range []string{pathA, pathB} {
		if err := os.WriteFile(p, []byte("png"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	model := New(Options{
		Client: newTestWorkspaceClient(),
		Config: Config{
			InitialFeature: "chat",
			StatePath:      t.TempDir() + "/state.json",
			DraftDir:       t.TempDir(),
		},
	})
	model.feature = FeatureChat
	model.pendingChatAttachments = []pendingAttachment{
		{path: pathA, contentType: "image/png", name: "a.png"},
		{path: pathB, contentType: "image/png", name: "b.png"},
	}

	updated, cmd := model.updateKey(tea.KeyMsg{Type: tea.KeyCtrlX})
	if cmd != nil {
		t.Fatalf("clear should not return a command, got %v", cmd)
	}
	if len(updated.pendingChatAttachments) != 0 {
		t.Fatalf("expected pending list cleared, got %d", len(updated.pendingChatAttachments))
	}
	if !strings.Contains(updated.toast, "cleared") {
		t.Fatalf("expected clear toast, got %q", updated.toast)
	}
	for _, p := range []string{pathA, pathB} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("expected %s removed, stat err=%v", p, err)
		}
	}
}

func TestChatSentFailureRestoresPendingAttachments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "paste.png")
	if err := os.WriteFile(path, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	model := New(Options{
		Client: newTestWorkspaceClient(),
		Config: Config{
			InitialFeature: "chat",
			StatePath:      t.TempDir() + "/state.json",
			DraftDir:       t.TempDir(),
		},
	})
	model.feature = FeatureChat
	model.chatMessages = []api.ChatMessage{{ID: "pending-1", Space: "spaces/engineering", Text: "hi", Pending: true}}
	model.seenMessages = map[string]bool{}

	failure := chatSentMsg{
		pendingID:   "pending-1",
		err:         errors.New("network down"),
		attachments: []pendingAttachment{{path: path, contentType: "image/png", name: "paste.png"}},
	}
	updated, _ := model.Update(failure)
	m := updated.(Model)

	if len(m.pendingChatAttachments) != 1 || m.pendingChatAttachments[0].path != path {
		t.Fatalf("expected pending attachments restored, got %#v", m.pendingChatAttachments)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("attachment file should still exist after failure, got %v", err)
	}
	if !strings.Contains(m.err, "network down") {
		t.Fatalf("expected send error surfaced, got %q", m.err)
	}
}

func TestChatSentReplacePendingDedupesRealtimeRace(t *testing.T) {
	model := New(Options{
		Client: newTestWorkspaceClient(),
		Config: Config{
			InitialFeature: "chat",
			StatePath:      t.TempDir() + "/state.json",
			DraftDir:       t.TempDir(),
		},
	})
	model.feature = FeatureChat
	model.seenMessages = map[string]bool{}
	pendingID := "pending-1"
	real := api.ChatMessage{
		ID:         "real-1",
		Space:      "spaces/engineering",
		SenderID:   "users/me-real",
		SenderName: "Me",
		Text:       "hello",
		CreateTime: time.Now(),
	}
	// Simulate the race: realtime push lands first and appends the real
	// message under its real ID, then the pending placeholder is still in
	// the list waiting for the API response.
	model.chatMessages = []api.ChatMessage{
		{ID: pendingID, Space: real.Space, SenderID: "users/me", Text: "hello", Pending: true, CreateTime: real.CreateTime.Add(-time.Second)},
		real,
	}

	model.replacePending(pendingID, real, nil)

	if got := len(model.chatMessages); got != 1 {
		t.Fatalf("expected dedupe to leave 1 message, got %d: %#v", got, model.chatMessages)
	}
	if model.chatMessages[0].ID != real.ID {
		t.Fatalf("expected real message after dedupe, got %#v", model.chatMessages[0])
	}
}

func TestChatShiftRRefreshesAllWorkspaceData(t *testing.T) {
	client := &countingWorkspaceClient{WorkspaceClient: newTestWorkspaceClient()}
	model := New(Options{
		Client: client,
		Config: Config{
			InitialFeature: "chat",
			StatePath:      t.TempDir() + "/state.json",
			DraftDir:       t.TempDir(),
		},
	})
	model.feature = FeatureChat

	updated, cmd := model.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	model = updated
	if cmd == nil {
		t.Fatal("expected full refresh command")
	}
	if !model.loading {
		t.Fatal("full refresh should enter loading state")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("expected loadedMsg from full refresh command")
	}

	if client.authStatusCalls != 1 || client.chatSpacesCalls != 1 || client.chatMessagesCalls != 1 || client.mailLabelsCalls != 1 || client.mailThreadsCalls != 1 || client.calendarEventsCalls != 1 || client.meetSpacesCalls != 1 {
		t.Fatalf("expected full workspace refresh, auth=%d spaces=%d chat=%d labels=%d mail=%d calendar=%d meet=%d",
			client.authStatusCalls, client.chatSpacesCalls, client.chatMessagesCalls, client.mailLabelsCalls, client.mailThreadsCalls, client.calendarEventsCalls, client.meetSpacesCalls)
	}
}

type blockingMessagesClient struct {
	api.WorkspaceClient
	started chan struct{}
	release chan struct{}
}

func (c *blockingMessagesClient) ChatMessages(ctx context.Context, spaceName, pageToken string) (api.Page[api.ChatMessage], error) {
	close(c.started)
	select {
	case <-ctx.Done():
		return api.Page[api.ChatMessage]{}, ctx.Err()
	case <-c.release:
		return c.WorkspaceClient.ChatMessages(ctx, spaceName, pageToken)
	}
}

type countingMessagesClient struct {
	api.WorkspaceClient
	calls int
}

func (c *countingMessagesClient) ChatMessages(ctx context.Context, spaceName, pageToken string) (api.Page[api.ChatMessage], error) {
	c.calls++
	return c.WorkspaceClient.ChatMessages(ctx, spaceName, pageToken)
}

type rotatingEventsClient struct {
	api.WorkspaceClient
	channels []chan api.DaemonEvent
	calls    int
}

func (c *rotatingEventsClient) SubscribeEvents(context.Context, []string) (<-chan api.DaemonEvent, error) {
	if c.calls >= len(c.channels) {
		c.channels = append(c.channels, make(chan api.DaemonEvent))
	}
	ch := c.channels[c.calls]
	c.calls++
	return ch, nil
}

func runTestCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected command")
	}
	done := make(chan tea.Msg, 1)
	go func() {
		done <- cmd()
	}()
	select {
	case msg := <-done:
		return msg
	case <-time.After(100 * time.Millisecond):
		t.Fatal("command timed out")
		return nil
	}
}

func runBatchSideEffects(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	runBatchSideEffectsDepth(t, cmd, 0)
}

func runBatchSideEffectsDepth(t *testing.T, cmd tea.Cmd, depth int) {
	t.Helper()
	if cmd == nil {
		return
	}
	if depth > 4 {
		t.Fatal("command batch nesting too deep")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return
	}
	for _, inner := range batch {
		runBatchSideEffectsDepth(t, inner, depth+1)
	}
}

type countingWorkspaceClient struct {
	api.WorkspaceClient

	authStatusCalls     int
	chatSpacesCalls     int
	chatMessagesCalls   int
	mailLabelsCalls     int
	mailThreadsCalls    int
	calendarEventsCalls int
	meetSpacesCalls     int

	lastChatSpace string
	lastMailQuery api.MailQuery
}

func (c *countingWorkspaceClient) AuthStatus(ctx context.Context) (api.AuthStatus, error) {
	c.authStatusCalls++
	return c.WorkspaceClient.AuthStatus(ctx)
}

func (c *countingWorkspaceClient) ChatSpaces(ctx context.Context) (api.Page[api.Space], error) {
	c.chatSpacesCalls++
	return c.WorkspaceClient.ChatSpaces(ctx)
}

func (c *countingWorkspaceClient) ChatMessages(ctx context.Context, spaceName, pageToken string) (api.Page[api.ChatMessage], error) {
	c.chatMessagesCalls++
	c.lastChatSpace = spaceName
	return c.WorkspaceClient.ChatMessages(ctx, spaceName, pageToken)
}

func (c *countingWorkspaceClient) MailLabels(ctx context.Context) ([]api.MailLabel, error) {
	c.mailLabelsCalls++
	return c.WorkspaceClient.MailLabels(ctx)
}

func (c *countingWorkspaceClient) MailThreads(ctx context.Context, query api.MailQuery) (api.Page[api.MailThread], error) {
	c.mailThreadsCalls++
	c.lastMailQuery = query
	return c.WorkspaceClient.MailThreads(ctx, query)
}

func (c *countingWorkspaceClient) CalendarEvents(ctx context.Context, query api.CalendarQuery) (api.Page[api.CalendarEvent], error) {
	c.calendarEventsCalls++
	return c.WorkspaceClient.CalendarEvents(ctx, query)
}

func (c *countingWorkspaceClient) MeetSpaces(ctx context.Context) (api.Page[api.MeetSpace], error) {
	c.meetSpacesCalls++
	return c.WorkspaceClient.MeetSpaces(ctx)
}

type countingChatReaderClient struct {
	*countingWorkspaceClient

	readCalls     int
	lastReadSpace string
}

func (c *countingChatReaderClient) MarkChatRead(_ context.Context, spaceName string) error {
	c.readCalls++
	c.lastReadSpace = spaceName
	return nil
}
