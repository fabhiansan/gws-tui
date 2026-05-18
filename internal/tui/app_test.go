package tui

import (
	"context"
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
