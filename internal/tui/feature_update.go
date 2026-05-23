package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fabhiansan/gws-tui/internal/api"
)

func (m *Model) handleLoaded(msg loadedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.loading = false
	m.chatLoading = false
	m.chatLoadSpace = ""
	if msg.err != nil {
		m.err = msg.err.Error()
	}
	if msg.err == nil {
		m.cacheLoaded = true
		for _, f := range featureOrder {
			m.featureLoading[f] = false
			m.featureLoaded[f] = true
		}
	}
	m.auth = msg.auth
	m.authRequired = m.authRequired || msg.authRequired
	m.spaces = msg.spaces.Items
	m.chatMessages = dedupeChatMessages(msg.messages.Items)
	m.chatOlder = msg.messages.NextPageToken
	m.markSeenChatMessages(m.chatMessages)
	m.mailLabels = msg.labels
	m.mailThreads = msg.threads.Items
	m.mailNext = msg.threads.NextPageToken
	m.events = sortedEvents(msg.events.Items)
	if !msg.calendarMonth.IsZero() {
		m.calendarMonth = msg.calendarMonth
		m.monthEvents = m.events
	}
	m.calendarNext = msg.events.NextPageToken
	if msg.calendars.Items != nil {
		m.calendars = msg.calendars.Items
		m.calendarIndex = indexOfCalendar(m.calendars, msg.calendarID)
	}
	m.meetSpaces = msg.meet.Items
	m.taskLists = msg.taskLists.Items
	m.taskListIndex = indexOfTaskList(m.taskLists, msg.taskListID)
	m.tasks = msg.tasks.Items
	m.taskNext = msg.tasks.NextPageToken
	m.driveFiles = msg.driveFiles.Items
	m.driveNext = msg.driveFiles.NextPageToken
	m.docFiles = msg.docFiles.Items
	m.docNext = msg.docFiles.NextPageToken
	m.doc = msg.doc
	m.docLoadingID = ""
	m.clampSelections()
	m.persistWorkspaceCache()
	cmds = append(cmds, m.subscribeCmd())
	cmds = append(cmds, m.enrichSpacesCmds()...)
	cmds = append(cmds, m.enrichSendersCmds()...)
	cmds = append(cmds, m.imageDownloadCmdsForWorkspace()...)
	cmds = append(cmds, m.precomputeFrameCmdsForCurrentDetail()...)

	return cmds
}

func (m *Model) handleFeatureLoaded(msg featureLoadedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.loading = false
	if msg.err != nil {
		m.err = msg.err.Error()
		return cmds
	}
	switch msg.feature {
	case FeatureChat:
		if messages, ok := msg.items.([]api.ChatMessage); ok {
			m.chatMessages = dedupeChatMessages(append(messages, m.chatMessages...))
			m.chatOlder = msg.next
			m.markSeenChatMessages(m.chatMessages)
			m.toast = "older messages loaded"
			m.persistWorkspaceCache()
			cmds = append(cmds, m.enrichSendersCmds()...)
			cmds = append(cmds, m.imageDownloadCmdsForChat(messages)...)
		}
	case FeatureMail:
		if threads, ok := msg.items.([]api.MailThread); ok {
			m.mailThreads = append(m.mailThreads, threads...)
			m.mailNext = msg.next
			m.toast = "more mail loaded"
			m.persistWorkspaceCache()
			cmds = append(cmds, m.imageDownloadCmdsForMail(threads)...)
		}
	case FeatureCalendar:
		if events, ok := msg.items.([]api.CalendarEvent); ok {
			m.events = sortedEvents(append(m.events, events...))
			m.calendarNext = msg.next
			m.toast = "more events loaded"
			m.persistWorkspaceCache()
		}
	case FeatureTasks:
		if tasks, ok := msg.items.([]api.TaskItem); ok {
			m.tasks = append(m.tasks, tasks...)
			m.taskNext = msg.next
			m.toast = "more tasks loaded"
			m.persistWorkspaceCache()
		}
	case FeatureDrive:
		if files, ok := msg.items.([]api.DriveFile); ok {
			m.driveFiles = append(m.driveFiles, files...)
			m.driveNext = msg.next
			m.toast = "more drive files loaded"
			m.persistWorkspaceCache()
		}
	case FeatureDocs:
		if files, ok := msg.items.([]api.DriveFile); ok {
			m.docFiles = append(m.docFiles, files...)
			m.docNext = msg.next
			m.toast = "more docs loaded"
			m.persistWorkspaceCache()
		}
	}

	return cmds
}

func (m *Model) handleFeatureRefreshed(msg featureRefreshedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.loading = false
	if msg.startup {
		m.featureLoading[msg.feature] = false
	}
	if msg.err != nil {
		m.err = msg.err.Error()
		return cmds
	}
	if msg.startup {
		m.featureLoaded[msg.feature] = true
		m.cacheLoaded = true
	}
	switch msg.feature {
	case FeatureMail:
		m.mailLabels = msg.labels
		m.mailThreads = msg.threads.Items
		m.mailNext = msg.threads.NextPageToken
		if strings.TrimSpace(m.search) != "" {
			m.toast = "search: " + m.search
		} else {
			if !msg.startup {
				m.toast = fallback(m.mailFolder, defaultMailFolder)
			}
			m.rememberMailPage(m.mailFolder, msg.threads)
		}
		m.clampSelections()
		m.persistWorkspaceCache()
		cmds = append(cmds, m.imageDownloadCmdsForMail(m.mailThreads)...)
	case FeatureCalendar:
		if msg.calendars.Items != nil {
			m.calendars = msg.calendars.Items
			m.calendarIndex = indexOfCalendar(m.calendars, msg.calendarID)
		}
		if m.calendarFeedback.eventID != "" {
			m.events = sortedEvents(mergeEvents(m.events, msg.events.Items))
		} else {
			m.events = sortedEvents(msg.events.Items)
		}
		if !msg.calendarMonth.IsZero() {
			m.calendarMonth = msg.calendarMonth
			m.monthEvents = m.events
		}
		m.calendarNext = msg.events.NextPageToken
		if !msg.startup {
			m.toast = "calendar refreshed"
		}
		m.calendarFeedback = calendarActivityFeedback{}
		m.clampSelections()
		m.persistWorkspaceCache()
	case FeatureMeet:
		m.meetSpaces = sortedMeetSpaces(msg.meet.Items)
		if !msg.startup {
			m.toast = "meet refreshed"
		}
		m.clampSelections()
		m.persistWorkspaceCache()
	case FeatureTasks:
		m.taskLists = msg.taskLists.Items
		m.taskListIndex = indexOfTaskList(m.taskLists, msg.taskListID)
		m.tasks = msg.tasks.Items
		m.taskNext = msg.tasks.NextPageToken
		if !msg.startup {
			m.toast = "tasks refreshed"
		}
		m.clampSelections()
		m.persistWorkspaceCache()
	case FeatureDrive:
		m.driveFiles = msg.driveFiles.Items
		m.driveNext = msg.driveFiles.NextPageToken
		if !msg.startup {
			m.toast = "drive refreshed"
		}
		m.clampSelections()
		m.persistWorkspaceCache()
	case FeatureDocs:
		m.docFiles = msg.docFiles.Items
		m.docNext = msg.docFiles.NextPageToken
		m.doc = msg.doc
		m.docLoadingID = ""
		if strings.TrimSpace(m.search) != "" {
			m.toast = "search: " + m.search
		} else if !msg.startup {
			m.toast = "docs refreshed"
		}
		m.clampSelections()
		m.persistWorkspaceCache()
	}

	return cmds
}
