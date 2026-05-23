package daemon

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fabhiansan/gws-tui/internal/api"
)

func (s *Server) dispatch(session *Session, method string, params json.RawMessage) (any, error) {
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	switch method {
	case "Ping":
		return nil, nil
	case "DaemonStatus":
		return s.status(), nil
	case "Snapshot":
		return s.getSnapshot(ctx)
	case "SubscribeTopics":
		var p api.SubscribeTopicsParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		s.subscribe(session, p.Topics)
		return nil, nil
	case "ClientHello":
		var p api.ClientHelloParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		session.mu.Lock()
		session.pid = p.PID
		session.tty = p.TTY
		session.mu.Unlock()
		return nil, nil
	case "DraftSave":
		var p api.DraftSaveParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return nil, s.saveDraft(p.Key, p.Payload)
	case "DraftLoad":
		var p api.DraftLoadParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		payload, ok, err := s.loadDraft(p.Key)
		return api.DraftLoadResult{Found: ok, Payload: payload}, err
	case "AuthStatus":
		return s.dispatchAuthStatus(ctx)
	case "ChatSpaces":
		return s.dispatchChatSpaces(ctx)
	case "ChatMessages":
		return s.dispatchChatMessages(ctx, params)
	case "SendChatMessage":
		return s.dispatchSendChatMessage(ctx, params)
	case "EditChatMessage":
		return s.dispatchEditChatMessage(ctx, params)
	case "DeleteChatMessage":
		return s.dispatchDeleteChatMessage(ctx, params)
	case "CreateChatSpace":
		return s.dispatchCreateChatSpace(ctx, params)
	case "SetupChatSpace":
		return s.dispatchSetupChatSpace(ctx, params)
	case "AddChatReaction":
		return s.dispatchAddChatReaction(ctx, params)
	case "DeleteChatReaction":
		return s.dispatchDeleteChatReaction(ctx, params)
	case "ChatMembers":
		return s.dispatchChatMembers(ctx, params)
	case "PeopleGet":
		return s.dispatchPeopleGet(ctx, params)
	case "DownloadAttachment":
		var p api.DownloadAttachmentParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return nil, s.downloadAttachment(ctx, p.Attachment, p.OutputPath)
	case "MailLabels":
		return s.dispatchMailLabels(ctx)
	case "MailThreads":
		return s.dispatchMailThreads(ctx, params)
	case "SendMail":
		return s.dispatchSendMail(ctx, params)
	case "MailDrafts":
		return s.dispatchMailDrafts(ctx, params)
	case "CreateMailDraft":
		return s.dispatchCreateMailDraft(ctx, params)
	case "SendMailDraft":
		return s.dispatchSendMailDraft(ctx, params)
	case "ArchiveMail":
		return s.dispatchArchiveMail(ctx, params)
	case "TrashMail":
		return s.dispatchTrashMail(ctx, params)
	case "ToggleStar":
		return s.dispatchToggleStar(ctx, params)
	case "SetMailUnread":
		return s.dispatchSetMailUnread(ctx, params)
	case "ToggleMailLabel":
		return s.dispatchToggleMailLabel(ctx, params)
	case "CalendarLists":
		return s.dispatchCalendarLists(ctx)
	case "CalendarEvents":
		return s.dispatchCalendarEvents(ctx, params)
	case "QuickAddEvent":
		return s.dispatchQuickAddEvent(ctx, params)
	case "CreateEvent":
		return s.dispatchCreateEvent(ctx, params)
	case "UpdateEvent":
		return s.dispatchUpdateEvent(ctx, params)
	case "MoveEvent":
		return s.dispatchMoveEvent(ctx, params)
	case "RSVPEvent":
		return s.dispatchRSVPEvent(ctx, params)
	case "DeleteEvent":
		return s.dispatchDeleteEvent(ctx, params)
	case "MeetSpaces":
		return s.dispatchMeetSpaces(ctx)
	case "CreateMeetSpace":
		return s.dispatchCreateMeetSpace(ctx, params)
	case "EndMeetSpace":
		return s.dispatchEndMeetSpace(ctx, params)
	case "TaskLists":
		return s.dispatchTaskLists(ctx)
	case "Tasks":
		return s.dispatchTasks(ctx, params)
	case "SetTaskCompleted":
		return s.dispatchSetTaskCompleted(ctx, params)
	case "DeleteTask":
		return s.dispatchDeleteTask(ctx, params)
	case "DriveFiles":
		return s.dispatchDriveFiles(ctx, params)
	case "Docs":
		return s.dispatchDocs(ctx, params)
	case "Doc":
		return s.dispatchDoc(ctx, params)
	case "PinChatSpace":
		var p api.SpaceNameParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return nil, s.pinChatSpace(p.SpaceName)
	case "UnpinChatSpace":
		var p api.SpaceNameParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return nil, s.unpinChatSpace(p.SpaceName)
	case "MarkChatRead":
		var p api.SpaceNameParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return nil, s.markChatRead(ctx, p.SpaceName)
	default:
		return nil, fmt.Errorf("unknown method %q", method)
	}
}

func (s *Server) dispatchAuthStatus(ctx context.Context) (api.AuthStatus, error) {
	status, err := s.client.AuthStatus(ctx)
	if err == nil {
		s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) { snapshot.Auth = status })
		s.broadcast("auth.changed", status)
	}
	return status, err
}
