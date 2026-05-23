package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type rawMeetConferenceRecord struct {
	Name       string `json:"name"`
	Space      string `json:"space"`
	StartTime  string `json:"startTime"`
	EndTime    string `json:"endTime"`
	ExpireTime string `json:"expireTime"`
}

type rawMeetParticipant struct {
	Name              string `json:"name"`
	EarliestStartTime string `json:"earliestStartTime"`
	LatestEndTime     string `json:"latestEndTime"`
	SignedinUser      struct {
		User        string `json:"user"`
		DisplayName string `json:"displayName"`
	} `json:"signedinUser"`
	AnonymousUser struct {
		DisplayName string `json:"displayName"`
	} `json:"anonymousUser"`
	PhoneUser struct {
		DisplayName string `json:"displayName"`
	} `json:"phoneUser"`
}

type rawMeetArtifact struct {
	Name             string `json:"name"`
	State            string `json:"state"`
	StartTime        string `json:"startTime"`
	EndTime          string `json:"endTime"`
	DriveDestination struct {
		File string `json:"file"`
	} `json:"driveDestination"`
	DocsDestination struct {
		Document string `json:"document"`
	} `json:"docsDestination"`
}

func (c *CommandClient) MeetSpaces(ctx context.Context) (Page[MeetSpace], error) {
	params, _ := json.Marshal(map[string]any{"pageSize": 20})
	var raw struct {
		ConferenceRecords []rawMeetConferenceRecord `json:"conferenceRecords"`
		Items             []rawMeetConferenceRecord `json:"items"`
		NextPageToken     string                    `json:"nextPageToken"`
	}
	if err := c.runJSON(ctx, &raw, "meet", "conferenceRecords", "list", "--params", string(params), "--format", "json"); err != nil {
		return Page[MeetSpace]{}, err
	}
	source := raw.ConferenceRecords
	if len(source) == 0 {
		source = raw.Items
	}
	items := make([]MeetSpace, 0, len(source))
	for _, record := range source {
		space := MeetSpace{
			Name:      record.Name,
			SpaceName: record.Space,
			Created:   parseRFC3339(record.StartTime),
			StartTime: parseRFC3339(record.StartTime),
			EndTime:   parseRFC3339(record.EndTime),
			Type:      "conferenceRecord",
			Active:    record.EndTime == "",
		}
		if details, err := c.meetSpace(ctx, record.Space); err == nil {
			if details.MeetingURI != "" {
				space.MeetingURI = details.MeetingURI
			}
			if details.MeetingCode != "" {
				space.MeetingCode = details.MeetingCode
			}
			if details.Config != nil {
				space.Config = details.Config
			}
			if details.SpaceResourceName() != "" {
				space.SpaceName = details.SpaceResourceName()
			}
			if details.ActiveConference != nil && details.ActiveConference.ConferenceRecord == record.Name {
				space.ActiveConference = details.ActiveConference
			}
		}
		space.Participants, _ = c.meetParticipants(ctx, record.Name)
		space.Recordings, _ = c.meetArtifacts(ctx, record.Name, "recordings")
		space.Transcripts, _ = c.meetArtifacts(ctx, record.Name, "transcripts")
		space.ActiveParticipants = len(space.Participants)
		space.Recording = len(space.Recordings) > 0
		items = append(items, space)
	}
	c.enrichMeetTitles(ctx, items)
	return Page[MeetSpace]{Items: items, NextPageToken: raw.NextPageToken}, nil
}

// enrichMeetTitles cross-references conference records with calendar events
// that share the same Meet link, filling in a human-readable Title and the
// invited attendee emails. It is best-effort: a calendar fetch failure leaves
// the conferences untouched so they simply fall back to the meeting code.
func (c *CommandClient) enrichMeetTitles(ctx context.Context, spaces []MeetSpace) {
	var minTime, maxTime time.Time
	for _, space := range spaces {
		t := space.StartTime
		if t.IsZero() {
			continue
		}
		if minTime.IsZero() || t.Before(minTime) {
			minTime = t
		}
		if maxTime.IsZero() || t.After(maxTime) {
			maxTime = t
		}
	}
	if minTime.IsZero() {
		return
	}
	page, err := c.CalendarEvents(ctx, CalendarQuery{
		TimeMin: minTime.AddDate(0, 0, -1),
		TimeMax: maxTime.AddDate(0, 0, 1),
	})
	if err != nil {
		return
	}
	byLink := make(map[string]CalendarEvent, len(page.Items))
	for _, event := range page.Items {
		if key := normalizeMeetLink(event.HangoutLink); key != "" {
			byLink[key] = event
		}
	}
	for i := range spaces {
		key := normalizeMeetLink(spaces[i].MeetingURI)
		if key == "" {
			continue
		}
		if event, ok := byLink[key]; ok {
			spaces[i].Title = event.Summary
			spaces[i].InvitedEmails = event.Attendees
		}
	}
}

// normalizeMeetLink reduces a Meet URL to a comparable key so a conference's
// MeetingURI and a calendar event's HangoutLink match regardless of scheme or
// trailing slash. It returns "" for anything that is not a Meet URL.
func normalizeMeetLink(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "https://")
	value = strings.TrimPrefix(value, "http://")
	value = strings.TrimSuffix(value, "/")
	if !strings.HasPrefix(value, "meet.google.com/") {
		return ""
	}
	return value
}

func (c *CommandClient) CreateMeetSpace(ctx context.Context, _ string) (MeetSpace, error) {
	var raw MeetSpace
	err := c.runJSON(ctx, &raw, "meet", "spaces", "create", "--json", "{}", "--format", "json")
	if raw.SpaceName == "" {
		raw.SpaceName = raw.SpaceResourceName()
	}
	return raw, err
}

func (c *CommandClient) meetSpace(ctx context.Context, name string) (MeetSpace, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return MeetSpace{}, errors.New("meet space name is required")
	}
	params, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return MeetSpace{}, err
	}
	var raw MeetSpace
	if err := c.runJSON(ctx, &raw, "meet", "spaces", "get", "--params", string(params), "--format", "json"); err != nil {
		return MeetSpace{}, err
	}
	if raw.SpaceName == "" {
		raw.SpaceName = raw.SpaceResourceName()
	}
	return raw, nil
}

func (c *CommandClient) EndMeetSpace(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("meet space name is required")
	}
	params, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return err
	}
	return c.runVoid(ctx, "meet", "spaces", "endActiveConference", "--params", string(params), "--format", "json")
}

func (c *CommandClient) meetParticipants(ctx context.Context, recordName string) ([]MeetParticipant, error) {
	if recordName == "" {
		return nil, nil
	}
	params, _ := json.Marshal(map[string]any{"parent": recordName, "pageSize": 50})
	var raw struct {
		Participants []rawMeetParticipant `json:"participants"`
		Items        []rawMeetParticipant `json:"items"`
	}
	if err := c.runJSON(ctx, &raw, "meet", "conferenceRecords", "participants", "list", "--params", string(params), "--format", "json"); err != nil {
		return nil, err
	}
	source := raw.Participants
	if len(source) == 0 {
		source = raw.Items
	}
	out := make([]MeetParticipant, 0, len(source))
	for _, item := range source {
		display := fallback(item.SignedinUser.DisplayName, fallback(item.AnonymousUser.DisplayName, item.PhoneUser.DisplayName))
		out = append(out, MeetParticipant{
			Name:        item.Name,
			DisplayName: display,
			User:        item.SignedinUser.User,
			JoinTime:    parseRFC3339(item.EarliestStartTime),
			LeaveTime:   parseRFC3339(item.LatestEndTime),
		})
	}
	return out, nil
}

func (c *CommandClient) meetArtifacts(ctx context.Context, recordName, kind string) ([]MeetArtifact, error) {
	if recordName == "" {
		return nil, nil
	}
	params, _ := json.Marshal(map[string]any{"parent": recordName, "pageSize": 20})
	var raw struct {
		Recordings  []rawMeetArtifact `json:"recordings"`
		Transcripts []rawMeetArtifact `json:"transcripts"`
		Items       []rawMeetArtifact `json:"items"`
	}
	if err := c.runJSON(ctx, &raw, "meet", "conferenceRecords", kind, "list", "--params", string(params), "--format", "json"); err != nil {
		return nil, err
	}
	source := raw.Items
	if kind == "recordings" && len(raw.Recordings) > 0 {
		source = raw.Recordings
	}
	if kind == "transcripts" && len(raw.Transcripts) > 0 {
		source = raw.Transcripts
	}
	out := make([]MeetArtifact, 0, len(source))
	for _, item := range source {
		file := item.DriveDestination.File
		if file == "" {
			file = item.DocsDestination.Document
		}
		out = append(out, MeetArtifact{
			Name:      item.Name,
			State:     item.State,
			File:      file,
			StartTime: parseRFC3339(item.StartTime),
			EndTime:   parseRFC3339(item.EndTime),
		})
	}
	return out, nil
}
