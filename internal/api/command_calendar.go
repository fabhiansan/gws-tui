package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type rawCalendarDateTime struct {
	DateTime string `json:"dateTime"`
	Date     string `json:"date"`
}

type rawCalendarAttendee struct {
	Email          string `json:"email"`
	ResponseStatus string `json:"responseStatus,omitempty"`
}

type rawCalendarEvent struct {
	ID               string                `json:"id"`
	Summary          string                `json:"summary"`
	Location         string                `json:"location"`
	HangoutLink      string                `json:"hangoutLink"`
	Description      string                `json:"description"`
	Start            rawCalendarDateTime   `json:"start"`
	End              rawCalendarDateTime   `json:"end"`
	Attendees        []rawCalendarAttendee `json:"attendees"`
	RecurringEventID string                `json:"recurringEventId"`
}

type rawCalendarListItem struct {
	ID          string `json:"id"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Primary     bool   `json:"primary"`
	AccessRole  string `json:"accessRole"`
}

func (c *CommandClient) CalendarLists(ctx context.Context) (Page[CalendarListItem], error) {
	params, _ := json.Marshal(map[string]any{"maxResults": 100})
	var raw struct {
		Items         []rawCalendarListItem `json:"items"`
		CalendarItems []rawCalendarListItem `json:"calendarItems"`
		NextPageToken string                `json:"nextPageToken"`
	}
	if err := c.runJSON(ctx, &raw, "calendar", "calendarList", "list", "--params", string(params), "--format", "json"); err != nil {
		return Page[CalendarListItem]{}, err
	}
	source := raw.Items
	if len(source) == 0 {
		source = raw.CalendarItems
	}
	items := make([]CalendarListItem, 0, len(source))
	for _, item := range source {
		items = append(items, CalendarListItem{
			ID:          item.ID,
			Summary:     fallback(item.Summary, item.ID),
			Description: item.Description,
			Primary:     item.Primary,
			AccessRole:  item.AccessRole,
		})
	}
	return Page[CalendarListItem]{Items: items, NextPageToken: raw.NextPageToken}, nil
}

func (c *CommandClient) CalendarEvents(ctx context.Context, q CalendarQuery) (Page[CalendarEvent], error) {
	calendarID := fallback(q.CalendarID, "primary")
	maxResults := 20
	params := map[string]any{"calendarId": calendarID, "singleEvents": true, "orderBy": "startTime"}
	switch {
	case q.Search != "":
		params["q"] = q.Search
	case !q.TimeMin.IsZero():
		params["timeMin"] = q.TimeMin.Format(time.RFC3339)
	default:
		params["timeMin"] = time.Now().Format(time.RFC3339)
	}
	if !q.TimeMax.IsZero() {
		params["timeMax"] = q.TimeMax.Format(time.RFC3339)
		// A whole month can hold far more events than the agenda's one page.
		maxResults = 250
	}
	params["maxResults"] = maxResults
	if q.PageToken != "" {
		params["pageToken"] = q.PageToken
	}
	payload, _ := json.Marshal(params)
	var raw struct {
		Items         []rawCalendarEvent `json:"items"`
		NextPageToken string             `json:"nextPageToken"`
	}
	err := c.runJSON(ctx, &raw, "calendar", "events", "list", "--params", string(payload), "--format", "json")
	if err != nil {
		return Page[CalendarEvent]{}, err
	}
	items := make([]CalendarEvent, 0, len(raw.Items))
	for _, item := range raw.Items {
		items = append(items, calendarEventFromRaw(item, "", calendarID))
	}
	return Page[CalendarEvent]{Items: items, NextPageToken: raw.NextPageToken}, nil
}

func (c *CommandClient) QuickAddEvent(ctx context.Context, text string) (CalendarEvent, error) {
	var raw rawCalendarEvent
	params, _ := json.Marshal(map[string]string{"calendarId": "primary", "text": text})
	err := c.runJSON(ctx, &raw, "calendar", "events", "quickAdd", "--params", string(params), "--format", "json")
	if err != nil {
		return CalendarEvent{}, err
	}
	return calendarEventFromRaw(raw, "", "primary"), nil
}

func (c *CommandClient) CreateEvent(ctx context.Context, draft EventDraft) (CalendarEvent, error) {
	calendarID := fallback(draft.CalendarID, "primary")
	payload, _ := json.Marshal(eventDraftBody(draft))
	var raw rawCalendarEvent
	err := c.runJSON(ctx, &raw, "calendar", "events", "insert", "--params", fmt.Sprintf(`{"calendarId":%q,"sendUpdates":"none"}`, calendarID), "--json", string(payload), "--format", "json")
	return calendarEventFromRaw(raw, "", calendarID), err
}

func (c *CommandClient) UpdateEvent(ctx context.Context, eventID string, draft EventDraft) (CalendarEvent, error) {
	if strings.TrimSpace(eventID) == "" {
		return CalendarEvent{}, errors.New("event id is required")
	}
	calendarID := fallback(draft.CalendarID, "primary")
	payload, _ := json.Marshal(eventDraftBody(draft))
	params, _ := json.Marshal(map[string]string{"calendarId": calendarID, "eventId": eventID, "sendUpdates": "none"})
	var raw rawCalendarEvent
	if err := c.runJSON(ctx, &raw, "calendar", "events", "patch", "--params", string(params), "--json", string(payload), "--format", "json"); err != nil {
		return CalendarEvent{}, err
	}
	return calendarEventFromRaw(raw, "", calendarID), nil
}

func (c *CommandClient) MoveEvent(ctx context.Context, eventID, sourceCalendarID, destinationCalendarID string) (CalendarEvent, error) {
	if strings.TrimSpace(eventID) == "" {
		return CalendarEvent{}, errors.New("event id is required")
	}
	sourceCalendarID = fallback(sourceCalendarID, "primary")
	if strings.TrimSpace(destinationCalendarID) == "" {
		return CalendarEvent{}, errors.New("destination calendar id is required")
	}
	params, _ := json.Marshal(map[string]string{"calendarId": sourceCalendarID, "eventId": eventID, "destination": destinationCalendarID, "sendUpdates": "none"})
	var raw rawCalendarEvent
	if err := c.runJSON(ctx, &raw, "calendar", "events", "move", "--params", string(params), "--format", "json"); err != nil {
		return CalendarEvent{}, err
	}
	return calendarEventFromRaw(raw, "", destinationCalendarID), nil
}

func eventDraftBody(draft EventDraft) map[string]any {
	body := map[string]any{
		"summary": draft.Summary,
		"start":   map[string]string{"dateTime": draft.Start.Format(time.RFC3339)},
		"end":     map[string]string{"dateTime": draft.End.Format(time.RFC3339)},
	}
	if draft.Location != "" {
		body["location"] = draft.Location
	}
	if draft.Description != "" {
		body["description"] = draft.Description
	}
	if len(draft.Attendees) > 0 {
		attendees := make([]map[string]string, 0, len(draft.Attendees))
		for _, email := range draft.Attendees {
			attendees = append(attendees, map[string]string{"email": email})
		}
		body["attendees"] = attendees
	}
	return body
}

func (c *CommandClient) RSVPEvent(ctx context.Context, eventID, response string) (CalendarEvent, error) {
	if strings.TrimSpace(eventID) == "" {
		return CalendarEvent{}, errors.New("event id is required")
	}
	switch response {
	case "accepted", "declined", "tentative":
	default:
		return CalendarEvent{}, fmt.Errorf("unsupported RSVP response %q", response)
	}
	selfEmail, err := c.primaryCalendarEmail(ctx)
	if err != nil {
		return CalendarEvent{}, err
	}
	getParams, _ := json.Marshal(map[string]string{"calendarId": "primary", "eventId": eventID})
	var raw rawCalendarEvent
	if err := c.runJSON(ctx, &raw, "calendar", "events", "get", "--params", string(getParams), "--format", "json"); err != nil {
		return CalendarEvent{}, err
	}
	attendees := make([]map[string]string, 0, len(raw.Attendees))
	matched := false
	for _, attendee := range raw.Attendees {
		if attendee.Email == "" {
			continue
		}
		next := map[string]string{"email": attendee.Email}
		if attendee.ResponseStatus != "" {
			next["responseStatus"] = attendee.ResponseStatus
		}
		if strings.EqualFold(attendee.Email, selfEmail) {
			next["responseStatus"] = response
			matched = true
		}
		attendees = append(attendees, next)
	}
	if !matched {
		return CalendarEvent{}, fmt.Errorf("self attendee %q not found on event", selfEmail)
	}
	patchBody, _ := json.Marshal(map[string]any{"attendees": attendees})
	patchParams, _ := json.Marshal(map[string]string{"calendarId": "primary", "eventId": eventID, "sendUpdates": "none"})
	var updated rawCalendarEvent
	if err := c.runJSON(ctx, &updated, "calendar", "events", "patch", "--params", string(patchParams), "--json", string(patchBody), "--format", "json"); err != nil {
		return CalendarEvent{}, err
	}
	return calendarEventFromRaw(updated, selfEmail, "primary"), nil
}

func (c *CommandClient) DeleteEvent(ctx context.Context, eventID string) error {
	if strings.TrimSpace(eventID) == "" {
		return errors.New("event id is required")
	}
	params, _ := json.Marshal(map[string]string{"calendarId": "primary", "eventId": eventID})
	return c.runVoid(ctx, "calendar", "events", "delete", "--params", string(params), "--format", "json")
}

func (c *CommandClient) primaryCalendarEmail(ctx context.Context) (string, error) {
	params, _ := json.Marshal(map[string]string{"calendarId": "primary"})
	var raw struct {
		ID string `json:"id"`
	}
	if err := c.runJSON(ctx, &raw, "calendar", "calendarList", "get", "--params", string(params), "--format", "json"); err != nil {
		return "", err
	}
	if strings.TrimSpace(raw.ID) == "" {
		return "", errors.New("primary calendar email not found")
	}
	return raw.ID, nil
}

func calendarEventFromRaw(raw rawCalendarEvent, selfEmail, calendarID string) CalendarEvent {
	attendees := make([]string, 0, len(raw.Attendees))
	rsvp := ""
	for _, attendee := range raw.Attendees {
		if attendee.Email != "" {
			attendees = append(attendees, attendee.Email)
		}
		if selfEmail != "" && strings.EqualFold(attendee.Email, selfEmail) {
			rsvp = attendee.ResponseStatus
		}
	}
	return CalendarEvent{
		ID:          raw.ID,
		CalendarID:  calendarID,
		Summary:     raw.Summary,
		Start:       parseGoogleTime(raw.Start.DateTime, raw.Start.Date),
		End:         parseGoogleTime(raw.End.DateTime, raw.End.Date),
		AllDay:      raw.Start.DateTime == "" && raw.Start.Date != "",
		Recurring:   raw.RecurringEventID != "",
		Location:    raw.Location,
		HangoutLink: raw.HangoutLink,
		Attendees:   attendees,
		Description: raw.Description,
		RSVP:        rsvp,
	}
}

func parseGoogleTime(dateTime, date string) time.Time {
	if dateTime != "" {
		if parsed, err := time.Parse(time.RFC3339, dateTime); err == nil {
			return parsed
		}
	}
	if date != "" {
		// All-day events carry a date with no clock time; parse it in the
		// local zone so day grouping in the TUI lands on the right date.
		if parsed, err := time.ParseInLocation("2006-01-02", date, time.Local); err == nil {
			return parsed
		}
	}
	return time.Now()
}
