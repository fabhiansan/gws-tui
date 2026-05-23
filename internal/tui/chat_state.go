package tui

import (
	"sort"
	"strings"
	"unicode"

	"github.com/fabhiansan/gws-tui/internal/api"
)

func lastSegmentOfName(value string) string {
	if value == "" {
		return ""
	}
	idx := strings.LastIndex(value, "/")
	if idx < 0 {
		return value
	}
	return value[idx+1:]
}

func spaceFromMessageName(value string) string {
	if idx := strings.Index(value, "/messages/"); idx > 0 {
		return value[:idx]
	}
	return ""
}

func normalizeUserKey(value string) string {
	return api.NormalizeUserID(value)
}

func (m *Model) markSelfUser(value string) {
	key := normalizeUserKey(value)
	if key == "" {
		return
	}
	m.selfUserIDs[key] = true
}

func (m Model) isSelfUserID(value string) bool {
	key := normalizeUserKey(value)
	if key == "" {
		return false
	}
	if key == "me" {
		return true
	}
	return m.inferredSelfUserIDs()[key]
}

func (m Model) inferredSelfUserIDs() map[string]bool {
	return api.InferSelfUserIDs(m.spaces, m.membersBySpace, m.selfUserIDs)
}

func (m Model) stripSelfFromSpaceTitle(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parts := splitSpaceTitleParts(value)
	if len(parts) <= 1 {
		if m.isSelfSpaceLabel(value) {
			return ""
		}
		return value
	}
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || m.isSelfSpaceLabel(part) {
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, ", ")
}

func splitSpaceTitleParts(value string) []string {
	raw := strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ',', ';', '|', '\n', '\r', '·', '•':
			return true
		default:
			return false
		}
	})
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func (m Model) isSelfSpaceLabel(value string) bool {
	needle := normalizeSpaceLabel(value)
	if needle == "" {
		return false
	}
	for userID := range m.inferredSelfUserIDs() {
		if normalizeSpaceLabel(userID) == needle {
			return true
		}
		if label := m.userLabels[userID]; normalizeSpaceLabel(label) == needle {
			return true
		}
	}
	return false
}

func normalizeSpaceLabel(value string) string {
	value = strings.TrimSpace(api.UserIDFromName(value))
	value = strings.Join(strings.Fields(value), " ")
	return strings.ToLower(value)
}

func (m *Model) normalizeUserCaches() {
	if m.userLabels != nil {
		normalized := make(map[string]string, len(m.userLabels))
		for userID, label := range m.userLabels {
			key := normalizeUserKey(userID)
			if key == "" {
				continue
			}
			normalized[key] = label
		}
		m.userLabels = normalized
	}
	if m.selfUserIDs != nil {
		normalized := make(map[string]bool, len(m.selfUserIDs))
		for userID, isSelf := range m.selfUserIDs {
			if !isSelf {
				continue
			}
			key := normalizeUserKey(userID)
			if key != "" {
				normalized[key] = true
			}
		}
		m.selfUserIDs = normalized
	}
	m.selfUserIDs = api.InferSelfUserIDs(m.spaces, m.membersBySpace, m.selfUserIDs)
}

func (m Model) visibleSpaces() []api.Space {
	if strings.TrimSpace(m.spaceFilter) == "" {
		return m.spaces
	}
	query := strings.TrimSpace(m.spaceFilter)
	matches := make([]spaceFilterMatch, 0, len(m.spaces))
	for index, space := range m.spaces {
		if score, ok := fuzzySpaceScore(m.spaceSearchText(space), query); ok {
			matches = append(matches, spaceFilterMatch{
				space: space,
				score: score,
				index: index,
			})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score < matches[j].score
		}
		return matches[i].index < matches[j].index
	})
	filtered := make([]api.Space, 0, len(matches))
	for _, match := range matches {
		filtered = append(filtered, match.space)
	}
	return filtered
}

type spaceFilterMatch struct {
	space api.Space
	score int
	index int
}

func (m Model) spaceSearchText(space api.Space) string {
	return strings.Join([]string{
		m.spaceLabel(space),
		space.DisplayName,
		space.FormattedName,
		space.Name,
		lastSegment(space.Name),
	}, " ")
}

func fuzzySpaceScore(candidate, query string) (int, bool) {
	candidate = strings.ToLower(candidate)
	terms := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	if len(terms) == 0 {
		return 0, true
	}
	total := 0
	for _, term := range terms {
		score, ok := fuzzyTermScore(candidate, term)
		if !ok {
			return 0, false
		}
		total += score
	}
	return total, true
}

func fuzzyTermScore(candidate, term string) (int, bool) {
	if term == "" {
		return 0, true
	}
	if idx := strings.Index(candidate, term); idx >= 0 {
		return idx, true
	}
	query := []rune(term)
	haystack := []rune(candidate)
	queryIndex := 0
	first := -1
	last := -1
	gaps := 0
	boundaryBonus := 0
	for index, char := range haystack {
		if char != query[queryIndex] {
			continue
		}
		if first == -1 {
			first = index
		}
		if last >= 0 {
			gaps += index - last - 1
		}
		if index == 0 || isFuzzyBoundary(haystack[index-1]) {
			boundaryBonus++
		}
		last = index
		queryIndex++
		if queryIndex == len(query) {
			return 100 + first + gaps*2 - boundaryBonus*3, true
		}
	}
	return 0, false
}

func isFuzzyBoundary(char rune) bool {
	return unicode.IsSpace(char) || char == '-' || char == '_' || char == '/' || char == '#' || char == '.' || char == ','
}

func (m Model) visibleChatMessages() []api.ChatMessage {
	if strings.TrimSpace(m.search) == "" {
		return m.chatMessages
	}
	needle := strings.ToLower(strings.TrimSpace(m.search))
	filtered := make([]api.ChatMessage, 0, len(m.chatMessages))
	for _, msg := range m.chatMessages {
		if strings.Contains(strings.ToLower(msg.Text), needle) {
			filtered = append(filtered, msg)
			continue
		}
		if strings.Contains(strings.ToLower(m.senderLabel(msg)), needle) {
			filtered = append(filtered, msg)
		}
	}
	return filtered
}

func (m Model) selectedSpace() api.Space {
	visible := m.visibleSpaces()
	if len(visible) == 0 {
		return api.Space{}
	}
	return visible[clamp(m.selected[FeatureChat], len(visible))]
}

func (m *Model) selectSpaceByName(spaceName string) bool {
	if spaceName == "" {
		m.clampSelections()
		return false
	}
	for index, space := range m.visibleSpaces() {
		if space.Name == spaceName {
			m.selected[FeatureChat] = index
			return true
		}
	}
	m.clampSelections()
	return false
}

func (m *Model) promoteSpaceToTop(spaceName string) {
	if spaceName == "" || len(m.spaces) < 2 {
		return
	}
	selectedName := m.selectedSpace().Name
	for index, space := range m.spaces {
		if space.Name != spaceName {
			continue
		}
		if index > 0 {
			copy(m.spaces[1:index+1], m.spaces[:index])
			m.spaces[0] = space
		}
		m.selectSpaceByName(selectedName)
		return
	}
}

func chatMessageKey(msg api.ChatMessage) string {
	return api.ChatMessageKey(msg)
}

func sameChatMessage(a, b api.ChatMessage) bool {
	return api.SameChatMessage(a, b)
}

func upsertChatMessage(items []api.ChatMessage, msg api.ChatMessage) ([]api.ChatMessage, bool) {
	return api.UpsertChatMessage(items, msg)
}

func dedupeChatMessages(items []api.ChatMessage) []api.ChatMessage {
	return api.DedupeChatMessages(items)
}

func (m *Model) markSeenChatMessage(msg api.ChatMessage) {
	if msg.ID != "" {
		m.seenMessages[msg.ID] = true
	}
	if msg.Name != "" {
		m.seenMessages[msg.Name] = true
	}
	if key := chatMessageKey(msg); key != "" {
		m.seenMessages[key] = true
	}
}

func (m *Model) markSeenChatMessages(items []api.ChatMessage) {
	for _, msg := range items {
		m.markSeenChatMessage(msg)
	}
}

func (m Model) hasSeenChatMessage(msg api.ChatMessage) bool {
	if msg.ID != "" && m.seenMessages[msg.ID] {
		return true
	}
	if msg.Name != "" && m.seenMessages[msg.Name] {
		return true
	}
	if key := chatMessageKey(msg); key != "" && m.seenMessages[key] {
		return true
	}
	for _, existing := range m.chatMessages {
		if sameChatMessage(existing, msg) {
			return true
		}
	}
	return false
}

func (m Model) isSelectedSpace(space string) bool {
	return m.selectedSpace().Name == space
}

func (m *Model) setSpaceUnread(spaceName string, unread bool) {
	for i := range m.spaces {
		if m.spaces[i].Name == spaceName {
			m.spaces[i].Unread = unread
			return
		}
	}
}
