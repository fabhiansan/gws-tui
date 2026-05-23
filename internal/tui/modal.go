package tui

import (
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/fabhiansan/gws-tui/internal/api"
)

type modalKind string

const (
	modalMail     modalKind = "mail"
	modalEvent    modalKind = "event"
	modalSearch   modalKind = "search"
	modalLabel    modalKind = "label"
	modalGoToDate modalKind = "gotodate"
)

// mailComposeMode selects how openMailCompose pre-fills the compose modal.
type mailComposeMode int

const (
	mailComposeNew mailComposeMode = iota
	mailComposeReply
	mailComposeReplyAll
	mailComposeForward
)

// modalField is one editable row in a compose modal. Single-line fields are
// backed by a textinput, the multiline body by a textarea — both give cursor
// movement, mid-text editing and a real visible cursor for free.
type modalField struct {
	Name      string
	Label     string
	Multiline bool
	input     textinput.Model
	area      textarea.Model
}

// newModalField builds a field around the right bubbles component. The
// textarea is focused before SetValue so vimGotoTop can park the cursor on the
// first line (reply bodies start with blank space above the quoted original).
func newModalField(name, label, value string, multiline bool) modalField {
	f := modalField{Name: name, Label: label, Multiline: multiline}
	if multiline {
		ta := textarea.New()
		ta.ShowLineNumbers = false
		ta.Prompt = ""
		ta.CharLimit = 0
		ta.MaxHeight = 0
		ta.SetWidth(64)
		ta.SetHeight(8)
		ta.Focus()
		ta.SetValue(value)
		vimGotoTop(&ta)
		f.area = ta
	} else {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 0
		ti.SetValue(value)
		f.input = ti
	}
	return f
}

func (f *modalField) value() string {
	if f.Multiline {
		return f.area.Value()
	}
	return f.input.Value()
}

func (f *modalField) focus() {
	if f.Multiline {
		f.area.Focus()
		return
	}
	f.input.Focus()
}

func (f *modalField) blur() {
	if f.Multiline {
		f.area.Blur()
		return
	}
	f.input.Blur()
}

func (f *modalField) view() string {
	if f.Multiline {
		return f.area.View()
	}
	return f.input.View()
}

// update forwards a message to whichever component backs the field.
func (f *modalField) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	if f.Multiline {
		f.area, cmd = f.area.Update(msg)
	} else {
		f.input, cmd = f.input.Update(msg)
	}
	return cmd
}

type composeModal struct {
	id         string
	kind       modalKind
	title      string
	fields     []modalField
	focus      int
	vimMode    vimMode
	autosave   bool
	savedAt    time.Time
	replyTo    string
	eventID    string
	calendarID string
}

func (m *composeModal) snapshot() map[string]any {
	fields := map[string]string{}
	for i := range m.fields {
		fields[m.fields[i].Name] = m.fields[i].value()
	}
	return map[string]any{
		"id":          m.id,
		"kind":        m.kind,
		"reply_to":    m.replyTo,
		"event_id":    m.eventID,
		"calendar_id": m.calendarID,
		"saved_at":    time.Now().Format(time.RFC3339),
		"fields":      fields,
	}
}

func (m *composeModal) field(name string) string {
	for i := range m.fields {
		if m.fields[i].Name == name {
			return m.fields[i].value()
		}
	}
	return ""
}

// fieldIndex returns the position of a named field, or -1 when absent.
func (m *composeModal) fieldIndex(name string) int {
	for i := range m.fields {
		if m.fields[i].Name == name {
			return i
		}
	}
	return -1
}

func (m *Model) openMailCompose(thread *api.MailThread, mode mailComposeMode) {
	subject := ""
	to := ""
	cc := ""
	body := ""
	replyTo := ""
	title := "Compose mail"
	if thread != nil && thread.ID != "" {
		replyTo = thread.ID
		to = thread.SenderEmail
		switch mode {
		case mailComposeReply, mailComposeReplyAll:
			if strings.HasPrefix(strings.ToLower(thread.Subject), "re:") {
				subject = thread.Subject
			} else {
				subject = "Re: " + thread.Subject
			}
			body = quotedReplyBody(thread)
			if mode == mailComposeReplyAll {
				title = "Reply all"
				cc = replyAllCc(thread, m.selfEmail)
			} else {
				title = "Reply"
			}
		case mailComposeForward:
			title = "Forward"
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
			newModalField("to", "To", to, false),
			newModalField("cc", "Cc", cc, false),
			newModalField("subject", "Subject", subject, false),
			newModalField("body", "Body", body, true),
		},
	}
	// Reply/forward land the user straight in the body; a brand-new mail
	// starts in the empty To field.
	if mode != mailComposeNew {
		m.modal.focus = m.modal.fieldIndex("body")
	}
	m.initModal()
}

// quotedReplyBody builds a mail-client style reply body: a blank space at the
// top for the new reply, an attribution line, then the previous message quoted
// underneath.
func quotedReplyBody(thread *api.MailThread) string {
	attribution := fmt.Sprintf("On %s, %s wrote:",
		thread.Date.Format("Mon, Jan 2, 2006 at 3:04 PM"),
		replyAttributionName(thread))
	quoted := "> " + strings.ReplaceAll(thread.Body, "\n", "\n> ")
	return "\n\n" + attribution + "\n" + quoted
}

func replyAttributionName(thread *api.MailThread) string {
	name := strings.TrimSpace(thread.Sender)
	email := strings.TrimSpace(thread.SenderEmail)
	switch {
	case name != "" && email != "" && !strings.EqualFold(name, email):
		return fmt.Sprintf("%s <%s>", name, email)
	case email != "":
		return email
	case name != "":
		return name
	default:
		return "someone"
	}
}

// replyAllCc collects every recipient of the original thread (its To and Cc
// headers), dropping the account's own address and the original sender (who
// already lands in the To field), de-duplicated.
func replyAllCc(thread *api.MailThread, selfEmail string) string {
	exclude := map[string]bool{}
	if e := strings.ToLower(strings.TrimSpace(selfEmail)); e != "" {
		exclude[e] = true
	}
	if e := strings.ToLower(strings.TrimSpace(thread.SenderEmail)); e != "" {
		exclude[e] = true
	}
	var recipients []string
	seen := map[string]bool{}
	for _, raw := range []string{thread.To, thread.Cc} {
		addrs, err := mail.ParseAddressList(raw)
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			key := strings.ToLower(strings.TrimSpace(addr.Address))
			if key == "" || exclude[key] || seen[key] {
				continue
			}
			seen[key] = true
			recipients = append(recipients, addr.String())
		}
	}
	return strings.Join(recipients, ", ")
}

func (m *Model) openMailLabelModal() {
	thread := m.selectedMail()
	if thread.ID == "" {
		return
	}
	m.modal = &composeModal{
		id:      fmt.Sprintf("label-%d", time.Now().UnixNano()),
		kind:    modalLabel,
		title:   "Toggle label",
		replyTo: thread.ID,
		fields: []modalField{
			newModalField("label", "Label", "", false),
		},
	}
	m.initModal()
}

// resolveMailLabelID maps a user-typed label name (or raw label id) to a
// Gmail label id, ignoring pseudo-labels that only carry a search query.
func (m Model) resolveMailLabelID(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	for _, label := range m.mailLabels {
		if strings.EqualFold(label.Name, name) && len(label.LabelIDs) > 0 {
			return label.LabelIDs[0]
		}
	}
	for _, label := range m.mailLabels {
		for _, id := range label.LabelIDs {
			if strings.EqualFold(id, name) {
				return id
			}
		}
	}
	return ""
}

func (m *Model) openEventCompose(event *api.CalendarEvent) {
	start := time.Now().Add(24 * time.Hour).Truncate(time.Hour)
	// A new event from the month grid defaults to the focused day at 09:00.
	if event == nil && m.feature == FeatureCalendar && m.calendarView == calViewMonth && !m.calendarCursor.IsZero() {
		c := m.calendarCursor
		start = time.Date(c.Year(), c.Month(), c.Day(), 9, 0, 0, 0, time.Local)
	}
	end := start.Add(time.Hour)
	summary, location, attendees, description := "", "Google Meet", "", ""
	eventID := ""
	calendarID := m.selectedCalendar().ID
	title := "New event"
	if event != nil && event.ID != "" {
		eventID = event.ID
		calendarID = event.CalendarID
		title = "Edit event"
		summary = event.Summary
		start = event.Start
		end = event.End
		location = event.Location
		attendees = strings.Join(event.Attendees, ", ")
		description = event.Description
	}
	m.modal = &composeModal{
		id:         fmt.Sprintf("event-%d", time.Now().UnixNano()),
		kind:       modalEvent,
		title:      title,
		autosave:   true,
		eventID:    eventID,
		calendarID: calendarID,
		fields: []modalField{
			newModalField("summary", "Summary", summary, false),
			newModalField("start", "Start", start.Format("2006-01-02 15:04"), false),
			newModalField("end", "End", end.Format("2006-01-02 15:04"), false),
			newModalField("location", "Where", location, false),
			newModalField("attendees", "Attendees", attendees, false),
			newModalField("description", "Description", description, true),
		},
	}
	m.initModal()
}

// openGoToDateModal prompts for a calendar date to jump the month grid to.
func (m *Model) openGoToDateModal() {
	cursor := m.calendarCursorOrToday()
	m.modal = &composeModal{
		id:    fmt.Sprintf("gotodate-%d", time.Now().UnixNano()),
		kind:  modalGoToDate,
		title: "Go to date",
		fields: []modalField{
			newModalField("date", "Date (YYYY-MM-DD)", cursor.Format("2006-01-02"), false),
		},
	}
	m.initModal()
}

func (m *Model) openSearchModal() {
	m.modal = &composeModal{
		id:    fmt.Sprintf("search-%d", time.Now().UnixNano()),
		kind:  modalSearch,
		title: "Search",
		fields: []modalField{
			newModalField("query", "Query", m.search, false),
		},
	}
	m.initModal()
}

// initModal sizes the freshly built modal's fields to the terminal and focuses
// the chosen field; every other field is blurred so only one cursor shows.
func (m *Model) initModal() {
	if m.modal == nil || len(m.modal.fields) == 0 {
		return
	}
	if m.modal.focus < 0 || m.modal.focus >= len(m.modal.fields) {
		m.modal.focus = 0
	}
	m.resizeModalFields()
	for i := range m.modal.fields {
		if i == m.modal.focus {
			m.modal.fields[i].focus()
		} else {
			m.modal.fields[i].blur()
		}
	}
}

// resizeModalFields fits the modal's editors to the current terminal size. It
// mirrors the geometry renderModal uses so the cursor and text never overflow
// the box.
func (m *Model) resizeModalFields() {
	if m.modal == nil {
		return
	}
	w, h := m.width, m.height
	if w <= 0 {
		w = 100
	}
	if h <= 0 {
		h = 32
	}
	modalWidth := min(max(40, w-14), 88)
	singles := 0
	for i := range m.modal.fields {
		if !m.modal.fields[i].Multiline {
			singles++
		}
	}
	bodyHeight := max(5, min(22, h-10-singles))
	for i := range m.modal.fields {
		f := &m.modal.fields[i]
		if f.Multiline {
			f.area.SetWidth(max(20, modalWidth-8))
			f.area.SetHeight(bodyHeight)
		} else {
			f.input.Width = max(10, modalWidth-18)
		}
	}
}

// modalSetFocus moves field focus to an absolute index, keeping exactly one
// component focused so only one cursor blinks.
func (m *Model) modalSetFocus(idx int) {
	if m.modal == nil || idx < 0 || idx >= len(m.modal.fields) || idx == m.modal.focus {
		return
	}
	m.modal.fields[m.modal.focus].blur()
	m.modal.focus = idx
	m.modal.fields[idx].focus()
}

// modalFocusField cycles field focus by delta, wrapping around.
func (m *Model) modalFocusField(delta int) {
	if m.modal == nil || len(m.modal.fields) == 0 {
		return
	}
	n := len(m.modal.fields)
	m.modalSetFocus(((m.modal.focus+delta)%n + n) % n)
}

func (m Model) updateModal(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.modal == nil {
		return m, nil
	}
	key := msg.String()

	// Controls that work in every mode and on every field.
	switch key {
	case "ctrl+q":
		m.modal = nil
		return m, nil
	case "ctrl+s":
		return m.submitModal()
	case "ctrl+d":
		if m.modal.kind == modalMail {
			return m.submitMailDraftModal()
		}
		return m, nil
	case "tab":
		m.modalFocusField(1)
		return m, nil
	case "shift+tab":
		m.modalFocusField(-1)
		return m, nil
	}

	field := &m.modal.fields[m.modal.focus]

	// Vim NORMAL mode: keys are motions/edits, never raw text. Esc never
	// closes the modal here — only ^q does — so a reflex Esc while editing
	// can't discard the draft.
	if m.cfg.VimMode && m.modal.vimMode == vimModeNormal {
		if field.Multiline {
			m.vimTextareaKey(msg, &field.area, &m.modal.vimMode)
		} else {
			m.modalVimSingleLine(msg, field)
		}
		return m, nil
	}

	// INSERT mode (vim on) or plain editing (vim off).
	switch key {
	case "esc":
		if m.cfg.VimMode {
			m.modal.vimMode = vimModeNormal
		} else {
			m.modal = nil
		}
		return m, nil
	case "enter":
		if field.Multiline {
			return m, field.update(msg)
		}
		if len(m.modal.fields) == 1 {
			return m.submitModal()
		}
		m.modalFocusField(1)
		return m, nil
	}
	return m, field.update(msg)
}

// modalVimSingleLine applies vim NORMAL-mode keys to a single-line field.
// Line-oriented motions degrade sensibly: j/k hop between fields, dd clears
// the field, gg/G jump to the ends.
func (m *Model) modalVimSingleLine(msg tea.KeyMsg, field *modalField) {
	key := msg.String()
	if m.vimPending != "" {
		pending := m.vimPending
		m.vimPending = ""
		switch pending + key {
		case "dd":
			field.input.SetValue("")
		case "cc":
			field.input.SetValue("")
			m.modal.vimMode = vimModeInsert
		case "yy":
			m.vimRegister = field.value()
			m.vimRegisterLine = false
			m.toast = "yanked"
		case "gg":
			field.update(tea.KeyMsg{Type: tea.KeyHome})
		}
		return
	}
	switch key {
	case "h", "left":
		field.update(tea.KeyMsg{Type: tea.KeyLeft})
	case "l", "right":
		field.update(tea.KeyMsg{Type: tea.KeyRight})
	case "w", "e":
		field.update(tea.KeyMsg{Type: tea.KeyRight, Alt: true})
	case "b":
		field.update(tea.KeyMsg{Type: tea.KeyLeft, Alt: true})
	case "0", "home":
		field.update(tea.KeyMsg{Type: tea.KeyHome})
	case "$", "end", "G":
		field.update(tea.KeyMsg{Type: tea.KeyEnd})
	case "g":
		m.vimPending = "g"
	case "j", "down":
		m.modalFocusField(1)
	case "k", "up":
		m.modalFocusField(-1)
	case "i":
		m.modal.vimMode = vimModeInsert
	case "I":
		field.update(tea.KeyMsg{Type: tea.KeyHome})
		m.modal.vimMode = vimModeInsert
	case "a":
		field.update(tea.KeyMsg{Type: tea.KeyRight})
		m.modal.vimMode = vimModeInsert
	case "A":
		field.update(tea.KeyMsg{Type: tea.KeyEnd})
		m.modal.vimMode = vimModeInsert
	case "x":
		field.update(tea.KeyMsg{Type: tea.KeyDelete})
	case "X":
		field.update(tea.KeyMsg{Type: tea.KeyBackspace})
	case "d":
		m.vimPending = "d"
	case "c":
		m.vimPending = "c"
	case "y":
		m.vimPending = "y"
	case "p", "P":
		for _, r := range m.vimRegister {
			if r == '\n' {
				continue
			}
			field.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	default:
		// swallow stray keys so normal mode never types raw text.
	}
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
				return loadedMsg{threads: page, labels: m.mailLabels, spaces: api.Page[api.Space]{Items: m.spaces}, messages: api.Page[api.ChatMessage]{Items: m.chatMessages, NextPageToken: m.chatOlder}, events: api.Page[api.CalendarEvent]{Items: m.events, NextPageToken: m.calendarNext}, meet: api.Page[api.MeetSpace]{Items: m.meetSpaces}, taskLists: api.Page[api.TaskList]{Items: m.taskLists}, tasks: api.Page[api.TaskItem]{Items: m.tasks, NextPageToken: m.taskNext}, taskListID: m.selectedTaskList().ID, driveFiles: api.Page[api.DriveFile]{Items: m.driveFiles, NextPageToken: m.driveNext}, docFiles: api.Page[api.DriveFile]{Items: m.docFiles, NextPageToken: m.docNext}, doc: m.doc, auth: m.auth, err: err}
			}
		case FeatureCalendar:
			// Search results populate the agenda; show that view, not the grid.
			m.calendarView = calViewAgenda
			m.loading = true
			return m, func() tea.Msg {
				calendarID := m.selectedCalendar().ID
				page, err := m.client.CalendarEvents(m.ctx, api.CalendarQuery{CalendarID: calendarID, Search: query})
				return loadedMsg{events: page, calendars: api.Page[api.CalendarListItem]{Items: m.calendars}, calendarID: calendarID, labels: m.mailLabels, spaces: api.Page[api.Space]{Items: m.spaces}, messages: api.Page[api.ChatMessage]{Items: m.chatMessages, NextPageToken: m.chatOlder}, threads: api.Page[api.MailThread]{Items: m.mailThreads, NextPageToken: m.mailNext}, meet: api.Page[api.MeetSpace]{Items: m.meetSpaces}, taskLists: api.Page[api.TaskList]{Items: m.taskLists}, tasks: api.Page[api.TaskItem]{Items: m.tasks, NextPageToken: m.taskNext}, taskListID: m.selectedTaskList().ID, driveFiles: api.Page[api.DriveFile]{Items: m.driveFiles, NextPageToken: m.driveNext}, docFiles: api.Page[api.DriveFile]{Items: m.docFiles, NextPageToken: m.docNext}, doc: m.doc, auth: m.auth, err: err}
			}
		case FeatureDrive:
			m.loading = true
			return m, func() tea.Msg {
				page, err := m.client.DriveFiles(m.ctx, api.DriveQuery{Search: query})
				return featureRefreshedMsg{feature: FeatureDrive, driveFiles: page, err: err}
			}
		case FeatureDocs:
			m.loading = true
			m.focusedPane = paneList
			m.selected[FeatureDocs] = 0
			m.docFiles = nil
			m.docNext = ""
			m.doc = api.DocDocument{}
			m.docLoadingID = ""
			return m, func() tea.Msg {
				files, filesErr := m.client.Docs(m.ctx, api.DriveQuery{Search: query})
				doc := api.DocDocument{}
				var docErr error
				if len(files.Items) > 0 {
					doc, docErr = m.client.Doc(m.ctx, files.Items[clamp(m.selected[FeatureDocs], len(files.Items))].ID)
				}
				return featureRefreshedMsg{feature: FeatureDocs, docFiles: files, doc: doc, err: firstErr(filesErr, docErr)}
			}
		case FeatureChat:
			m.clampSelections()
			return m.loadSelectedChat()
		default:
			m.clampSelections()
			return m, nil
		}
	case modalGoToDate:
		raw := strings.TrimSpace(modal.field("date"))
		m.modal = nil
		day, err := parseModalTime(raw)
		if err != nil {
			m.err = "date must use YYYY-MM-DD"
			return m, nil
		}
		m.calendarCursor = startOfDay(day)
		m.calendarDayEventCursor = 0
		m.calendarView = calViewMonth
		m.toast = day.Format("Mon, 02 Jan 2006")
		return m.loadCalendarMonth(m.calendarCursor)
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
			CalendarID:  modal.calendarID,
			Summary:     strings.TrimSpace(modal.field("summary")),
			Start:       start,
			End:         end,
			Location:    strings.TrimSpace(modal.field("location")),
			Attendees:   splitCSV(modal.field("attendees")),
			Description: modal.field("description"),
		}
		m.modal = nil
		return m, func() tea.Msg {
			var event api.CalendarEvent
			var err error
			if modal.eventID != "" {
				event, err = m.client.UpdateEvent(m.ctx, modal.eventID, draft)
			} else {
				event, err = m.client.CreateEvent(m.ctx, draft)
			}
			return eventActionMsg{event: event, err: err, label: "event saved"}
		}
	case modalLabel:
		name := strings.TrimSpace(modal.field("label"))
		threadID := modal.replyTo
		m.modal = nil
		if name == "" || threadID == "" {
			return m, nil
		}
		labelID := m.resolveMailLabelID(name)
		if labelID == "" {
			m.err = fmt.Sprintf("unknown label: %s", name)
			return m, nil
		}
		return m, func() tea.Msg {
			thread, err := m.client.ToggleMailLabel(m.ctx, threadID, labelID)
			return mailActionMsg{thread: thread, err: err, label: "label toggled"}
		}
	}
	return m, nil
}

func (m Model) submitMailDraftModal() (Model, tea.Cmd) {
	if m.modal == nil || m.modal.kind != modalMail {
		return m, nil
	}
	modal := m.modal
	draft := api.MailDraft{
		To:       strings.TrimSpace(modal.field("to")),
		Cc:       strings.TrimSpace(modal.field("cc")),
		Subject:  strings.TrimSpace(modal.field("subject")),
		Body:     modal.field("body"),
		ThreadID: modal.replyTo,
	}
	m.modal = nil
	return m, func() tea.Msg {
		item, err := m.client.CreateMailDraft(m.ctx, draft)
		return mailDraftActionMsg{draft: item, err: err}
	}
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
