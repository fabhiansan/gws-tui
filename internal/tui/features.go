package tui

import "strings"

type Feature string

const (
	FeatureChat     Feature = "chat"
	FeatureMail     Feature = "mail"
	FeatureCalendar Feature = "calendar"
	FeatureMeet     Feature = "meet"
	FeatureTasks    Feature = "tasks"
	FeatureDrive    Feature = "drive"
	FeatureDocs     Feature = "docs"
)

var featureOrder = []Feature{FeatureChat, FeatureMail, FeatureCalendar, FeatureMeet, FeatureTasks, FeatureDrive, FeatureDocs}

// startupFeatures lists the panes fetched eagerly during a cold start. Drive
// and Docs are excluded; they fetch lazily the first time the user opens them.
var startupFeatures = []Feature{FeatureChat, FeatureMail, FeatureCalendar, FeatureMeet, FeatureTasks}

func (m Model) featureLabel(f Feature) string {
	switch f {
	case FeatureChat:
		return "Chat"
	case FeatureMail:
		return "Mail"
	case FeatureCalendar:
		return "Calendar"
	case FeatureMeet:
		return "Meet"
	case FeatureTasks:
		return "Tasks"
	case FeatureDrive:
		return "Drive"
	case FeatureDocs:
		return "Docs"
	default:
		return string(f)
	}
}

func (m Model) featureIcon(f Feature) string {
	switch f {
	case FeatureChat:
		return m.icon("◉", "c")
	case FeatureMail:
		return m.icon("✉", "m")
	case FeatureCalendar:
		return m.icon("◫", "k")
	case FeatureMeet:
		return m.icon("◎", "v")
	case FeatureTasks:
		return m.icon("☑", "t")
	case FeatureDrive:
		return m.icon("◫", "d")
	case FeatureDocs:
		return m.icon("▤", "o")
	default:
		return m.icon("◦", "-")
	}
}

func normalizeFeature(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "mail", "gmail":
		return string(FeatureMail)
	case "calendar", "cal":
		return string(FeatureCalendar)
	case "meet":
		return string(FeatureMeet)
	case "tasks", "task":
		return string(FeatureTasks)
	case "drive":
		return string(FeatureDrive)
	case "docs", "doc":
		return string(FeatureDocs)
	default:
		return string(FeatureChat)
	}
}
