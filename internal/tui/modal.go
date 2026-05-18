package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fabhiantomaoludyo/gws-tui/internal/api"
)

type modalKind string

const (
	modalMail   modalKind = "mail"
	modalEvent  modalKind = "event"
	modalSearch modalKind = "search"
)

type modalField struct {
	Name      string `json:"name"`
	Label     string `json:"label"`
	Value     string `json:"value"`
	Multiline bool   `json:"multiline"`
}

type composeModal struct {
	id       string
	kind     modalKind
	title    string
	fields   []modalField
	focus    int
	autosave bool
	savedAt  time.Time
	replyTo  string
}

func (m *composeModal) snapshot() map[string]any {
	fields := map[string]string{}
	for _, field := range m.fields {
		fields[field.Name] = field.Value
	}
	return map[string]any{
		"id":       m.id,
		"kind":     m.kind,
		"reply_to": m.replyTo,
		"saved_at": time.Now().Format(time.RFC3339),
		"fields":   fields,
	}
}

func (m *composeModal) field(name string) string {
	for _, field := range m.fields {
		if field.Name == name {
			return field.Value
		}
	}
	return ""
}

func (m *composeModal) setField(name, value string) {
	for i := range m.fields {
		if m.fields[i].Name == name {
			m.fields[i].Value = value
			return
		}
	}
}

func (m *Model) openMailCompose(thread *api.MailThread, reply bool) {
	subject := ""
	to := ""
	body := ""
	replyTo := ""
	title := "Compose · ^s send · Tab next · ^q cancel"
	if thread != nil && thread.ID != "" {
		replyTo = thread.ID
		to = thread.SenderEmail
		if reply {
			title = "Reply · ^s send · Tab next · ^q cancel"
			if strings.HasPrefix(strings.ToLower(thread.Subject), "re:") {
				subject = thread.Subject
			} else {
				subject = "Re: " + thread.Subject
			}
			body = "\n\n> " + strings.ReplaceAll(thread.Body, "\n", "\n> ")
		} else {
			title = "Forward · ^s send · Tab next · ^q cancel"
			subject = "Fwd: " + thread.Subject
			body = "\n\n---------- Forwarded message ---------\n" + thread.Body
		}
	}
	m.modal = &composeModal{
		id:       fmt.Sprintf("mail-%d", time.Now().UnixNano()),
		kind:     modalMail,
		title:    title,
		autosave: true,
		replyTo:  replyTo,
		fields: []modalField{
			{Name: "to", Label: "To", Value: to},
			{Name: "cc", Label: "Cc"},
			{Name: "subject", Label: "Subject", Value: subject},
			{Name: "body", Label: "Body", Value: body, Multiline: true},
		},
	}
}

func (m *Model) openEventCompose(event *api.CalendarEvent) {
	start := time.Now().Add(24 * time.Hour).Truncate(time.Hour)
	end := start.Add(time.Hour)
	summary, location, attendees, description := "", "Google Meet", "", ""
	if event != nil && event.ID != "" {
		summary = event.Summary
		start = event.Start
		end = event.End
		location = event.Location
		attendees = strings.Join(event.Attendees, ", ")
		description = event.Description
	}
	m.modal = &composeModal{
		id:       fmt.Sprintf("event-%d", time.Now().UnixNano()),
		kind:     modalEvent,
		title:    "New event · ^s save · Tab next · ^q cancel",
		autosave: true,
		fields: []modalField{
			{Name: "summary", Label: "Summary", Value: summary},
			{Name: "start", Label: "Start", Value: start.Format("2006-01-02 15:04")},
			{Name: "end", Label: "End", Value: end.Format("2006-01-02 15:04")},
			{Name: "location", Label: "Where", Value: location},
			{Name: "attendees", Label: "Attendees", Value: attendees},
			{Name: "description", Label: "Description", Value: description, Multiline: true},
		},
	}
}

func (m *Model) openSearchModal() {
	m.modal = &composeModal{
		id:    fmt.Sprintf("search-%d", time.Now().UnixNano()),
		kind:  modalSearch,
		title: "Search · ^s apply · ^q cancel",
		fields: []modalField{
			{Name: "query", Label: "Query", Value: m.search},
		},
	}
}

func (m Model) updateModal(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.modal == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc", "ctrl+q":
		m.modal = nil
		return m, nil
	case "tab":
		m.modal.focus = (m.modal.focus + 1) % len(m.modal.fields)
		return m, nil
	case "shift+tab":
		m.modal.focus = (m.modal.focus - 1 + len(m.modal.fields)) % len(m.modal.fields)
		return m, nil
	case "ctrl+s":
		return m.submitModal()
	case "enter":
		if m.modal.fields[m.modal.focus].Multiline {
			m.modal.fields[m.modal.focus].Value += "\n"
			return m, nil
		}
		if len(m.modal.fields) == 1 {
			return m.submitModal()
		}
		m.modal.focus = (m.modal.focus + 1) % len(m.modal.fields)
		return m, nil
	case "backspace", "ctrl+h":
		value := []rune(m.modal.fields[m.modal.focus].Value)
		if len(value) > 0 {
			m.modal.fields[m.modal.focus].Value = string(value[:len(value)-1])
		}
		return m, nil
	default:
		if len(msg.Runes) > 0 {
			m.modal.fields[m.modal.focus].Value += string(msg.Runes)
		}
	}
	return m, nil
}

func (m Model) submitModal() (Model, tea.Cmd) {
	if m.modal == nil {
		return m, nil
	}
	modal := m.modal
	switch modal.kind {
	case modalSearch:
		query := strings.TrimSpace(modal.field("query"))
		m.search = query
		m.modal = nil
		switch m.feature {
		case FeatureMail:
			m.loading = true
			return m, func() tea.Msg {
				page, err := m.client.MailThreads(m.ctx, api.MailQuery{Label: "All Mail", Search: query})
				return loadedMsg{threads: page, labels: m.mailLabels, spaces: api.Page[api.Space]{Items: m.spaces}, messages: api.Page[api.ChatMessage]{Items: m.chatMessages, NextPageToken: m.chatOlder}, events: api.Page[api.CalendarEvent]{Items: m.events, NextPageToken: m.calendarNext}, meet: api.Page[api.MeetSpace]{Items: m.meetSpaces}, auth: m.auth, err: err}
			}
		case FeatureCalendar:
			m.loading = true
			return m, func() tea.Msg {
				page, err := m.client.CalendarEvents(m.ctx, api.CalendarQuery{Search: query})
				return loadedMsg{events: page, labels: m.mailLabels, spaces: api.Page[api.Space]{Items: m.spaces}, messages: api.Page[api.ChatMessage]{Items: m.chatMessages, NextPageToken: m.chatOlder}, threads: api.Page[api.MailThread]{Items: m.mailThreads, NextPageToken: m.mailNext}, meet: api.Page[api.MeetSpace]{Items: m.meetSpaces}, auth: m.auth, err: err}
			}
		case FeatureChat:
			m.clampSelections()
			return m.loadSelectedChat()
		default:
			m.clampSelections()
			return m, nil
		}
	case modalMail:
		draft := api.MailDraft{
			To:       strings.TrimSpace(modal.field("to")),
			Cc:       strings.TrimSpace(modal.field("cc")),
			Subject:  strings.TrimSpace(modal.field("subject")),
			Body:     modal.field("body"),
			ThreadID: modal.replyTo,
		}
		m.modal = nil
		return m, func() tea.Msg {
			thread, err := m.client.SendMail(m.ctx, draft)
			return mailActionMsg{thread: thread, err: err, label: "mail sent"}
		}
	case modalEvent:
		start, startErr := parseModalTime(modal.field("start"))
		end, endErr := parseModalTime(modal.field("end"))
		if startErr != nil || endErr != nil {
			m.err = "event start/end must use YYYY-MM-DD HH:MM"
			return m, nil
		}
		draft := api.EventDraft{
			Summary:     strings.TrimSpace(modal.field("summary")),
			Start:       start,
			End:         end,
			Location:    strings.TrimSpace(modal.field("location")),
			Attendees:   splitCSV(modal.field("attendees")),
			Description: modal.field("description"),
		}
		m.modal = nil
		return m, func() tea.Msg {
			event, err := m.client.CreateEvent(m.ctx, draft)
			return eventActionMsg{event: event, err: err, label: "event saved"}
		}
	}
	return m, nil
}

func parseModalTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"2006-01-02 15:04", time.RFC3339, "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time: %s", value)
}
