package tui

import (
	"sort"

	"github.com/fabhiansan/gws-tui/internal/api"
)

func (m Model) selectedMeet() api.MeetSpace {
	if len(m.meetSpaces) == 0 {
		return api.MeetSpace{}
	}
	return m.meetSpaces[clamp(m.selected[FeatureMeet], len(m.meetSpaces))]
}

// sortedMeetSpaces orders conferences newest first so the single-pane list and
// the selection index stay in the same order. Conferences with no start time
// sink to the bottom.
func sortedMeetSpaces(spaces []api.MeetSpace) []api.MeetSpace {
	out := append([]api.MeetSpace(nil), spaces...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].StartTime.After(out[j].StartTime)
	})
	return out
}
