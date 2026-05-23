package daemon

import (
	"context"
	"encoding/json"

	"github.com/fabhiansan/gws-tui/internal/api"
)

func (s *Server) dispatchChatSpaces(ctx context.Context) (api.Page[api.Space], error) {
	page, err := s.client.ChatSpaces(ctx)
	if err == nil {
		s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
			snapshot.Spaces = page.Items
			api.SyncLastReadMarkersFromSpaces(snapshot, page.Items)
		})
		s.stampLiveOnSpaces(page.Items)
		s.prefetchChatMessagesInBackground(page.Items)
	}
	return page, err
}

func (s *Server) dispatchChatMessages(ctx context.Context, params json.RawMessage) (api.Page[api.ChatMessage], error) {
	var p api.ChatMessagesParams
	if err := decode(params, &p); err != nil {
		return api.Page[api.ChatMessage]{}, err
	}
	page, err := s.client.ChatMessages(ctx, p.SpaceName, p.PageToken)
	if err == nil {
		s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
			api.ApplyChatPage(snapshot, p.SpaceName, page)
		})
		s.cacheAttachments(page.Items)
		s.broadcastChatHistoryLoaded(p.SpaceName, page)
		s.broadcast("chat.refreshed", map[string]string{"space": p.SpaceName})
	}
	return page, err
}

func (s *Server) dispatchSendChatMessage(ctx context.Context, params json.RawMessage) (api.ChatMessage, error) {
	var p api.SendChatMessageParams
	if err := decode(params, &p); err != nil {
		return api.ChatMessage{}, err
	}
	msg, err := s.client.SendChatMessage(ctx, p.SpaceName, p.Text, p.ThreadID, p.Attachments)
	if err == nil {
		s.handleChatMessage(msg, false)
	}
	return msg, err
}

func (s *Server) dispatchEditChatMessage(ctx context.Context, params json.RawMessage) (api.ChatMessage, error) {
	var p api.EditChatMessageParams
	if err := decode(params, &p); err != nil {
		return api.ChatMessage{}, err
	}
	msg, err := s.client.EditChatMessage(ctx, p.MessageName, p.Text)
	if err == nil {
		if msg.Space == "" {
			msg.Space = chatSpaceFromMessageName(p.MessageName)
		}
		s.handleChatMessage(msg, false)
	}
	return msg, err
}

func (s *Server) dispatchDeleteChatMessage(ctx context.Context, params json.RawMessage) (any, error) {
	var p api.ChatMessageNameParams
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	err := s.client.DeleteChatMessage(ctx, p.MessageName)
	if err == nil {
		s.removeChatMessage(p.MessageName)
		s.broadcast("chat.message.deleted", map[string]string{"name": p.MessageName})
	}
	return nil, err
}

func (s *Server) dispatchCreateChatSpace(ctx context.Context, params json.RawMessage) (api.Space, error) {
	var p api.CreateChatSpaceParams
	if err := decode(params, &p); err != nil {
		return api.Space{}, err
	}
	space, err := s.client.CreateChatSpace(ctx, p.DisplayName)
	if err == nil {
		s.applyChatSpace(space)
		s.broadcast("chat.space", space)
	}
	return space, err
}

func (s *Server) dispatchSetupChatSpace(ctx context.Context, params json.RawMessage) (api.Space, error) {
	var p api.SetupChatSpaceParams
	if err := decode(params, &p); err != nil {
		return api.Space{}, err
	}
	space, err := s.client.SetupChatSpace(ctx, p.DisplayName, p.Members)
	if err == nil {
		s.applyChatSpace(space)
		s.broadcast("chat.space", space)
	}
	return space, err
}

func (s *Server) dispatchAddChatReaction(ctx context.Context, params json.RawMessage) (string, error) {
	var p api.ChatReactionParams
	if err := decode(params, &p); err != nil {
		return "", err
	}
	name, err := s.client.AddChatReaction(ctx, p.MessageName, p.Emoji)
	if err == nil {
		s.broadcast("chat.reaction", map[string]string{"name": name, "message": p.MessageName, "emoji": p.Emoji, "action": "added"})
	}
	return name, err
}

func (s *Server) dispatchDeleteChatReaction(ctx context.Context, params json.RawMessage) (any, error) {
	var p api.ChatReactionParams
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	err := s.client.DeleteChatReaction(ctx, p.ReactionName)
	if err == nil {
		s.broadcast("chat.reaction", map[string]string{"name": p.ReactionName, "action": "deleted"})
	}
	return nil, err
}

func (s *Server) dispatchChatMembers(ctx context.Context, params json.RawMessage) ([]api.SpaceMember, error) {
	var p api.SpaceNameParams
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	members, err := s.client.ChatMembers(ctx, p.SpaceName)
	if err == nil {
		s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
			snapshot.MembersBySpace[p.SpaceName] = members
		})
	}
	return members, err
}

func (s *Server) dispatchPeopleGet(ctx context.Context, params json.RawMessage) (api.Person, error) {
	var p api.UserIDParams
	if err := decode(params, &p); err != nil {
		return api.Person{}, err
	}
	person, err := s.client.PeopleGet(ctx, p.UserID)
	if err == nil && person.UserID != "" {
		s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
			userID := normalizeUserKey(person.UserID)
			if person.DisplayName != "" {
				snapshot.UserLabels[userID] = person.DisplayName
			}
			if p.UserID == "me" || person.UserID == "me" {
				if userID != "" {
					snapshot.SelfUserIDs[userID] = true
				}
				if email := normalizeUserKey(person.Email); email != "" {
					snapshot.SelfUserIDs[email] = true
				}
			}
		})
	}
	return person, err
}
