package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fabhiantomaoludyo/gws-tui/internal/api"
)

func TestModelInitialRenderContainsFeatureTabs(t *testing.T) {
	model := New(Options{
		Client: api.NewFixtureClient(),
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
		WorkspaceClient: api.NewFixtureClient(),
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
		Client: api.NewFixtureClient(),
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
	client := &countingMessagesClient{WorkspaceClient: api.NewFixtureClient()}
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

func TestRefreshKeyOnlyReloadsSelectedChatSpace(t *testing.T) {
	client := &countingWorkspaceClient{WorkspaceClient: api.NewFixtureClient()}
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

func TestRefreshKeyOnlyReloadsMailFeature(t *testing.T) {
	client := &countingWorkspaceClient{WorkspaceClient: api.NewFixtureClient()}
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

func TestChatShiftRRefreshesAllWorkspaceData(t *testing.T) {
	client := &countingWorkspaceClient{WorkspaceClient: api.NewFixtureClient()}
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
