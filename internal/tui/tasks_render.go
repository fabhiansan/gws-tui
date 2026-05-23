package tui

import (
	"fmt"
	"strings"
)

func (m Model) taskListRows(width int) (string, []string, int, int) {
	list := m.selectedTaskList()
	title := fmt.Sprintf(" [1]-Tasks: %s (%d) ", truncate(fallback(list.Title, "Tasks"), 18), len(m.tasks))
	lines := []string{}
	selStart, selEnd := -1, -1
	for i, task := range m.tasks {
		prefix := " "
		if i == m.selected[FeatureTasks] {
			selStart = len(lines)
		}
		marker := m.icon("☐", "-")
		if strings.EqualFold(task.Status, "completed") {
			marker = m.live(m.icon("☑", "x"))
		}
		due := ""
		if !task.Due.IsZero() {
			due = " " + m.subtle(task.Due.Format("Jan 02"))
		}
		lines = append(lines, prefix+marker+" "+truncate(task.Title, width-14)+due)
		if i == m.selected[FeatureTasks] {
			selEnd = len(lines) - 1
		}
	}
	if len(m.taskLists) > 1 {
		lines = append(lines, "", m.subtle("[/[ previous list  ] next list]"))
	}
	return title, lines, selStart, selEnd
}

func (m Model) taskDetail() string {
	list := m.selectedTaskList()
	if list.ID == "" {
		return centerText("No task lists found.", m.detail.Width)
	}
	task := m.selectedTask()
	if task.ID == "" {
		return centerText("No tasks in "+list.Title+".", m.detail.Width)
	}
	lines := []string{
		"List:      " + fallback(list.Title, list.ID),
		"Status:    " + fallback(task.Status, "needsAction"),
		"Due:       " + formatOptionalTime(task.Due, "Mon, 02 Jan 2006"),
		"Completed: " + formatOptionalTime(task.Completed, "Mon, 02 Jan 2006 15:04"),
		"Updated:   " + formatOptionalTime(task.Updated, "Mon, 02 Jan 2006 15:04"),
		"Resource:  " + task.ID,
		"",
		"─── Notes ───",
		"",
		fallback(task.Notes, "(no notes)"),
		"",
		m.subtle("[Space] complete/uncomplete · d delete · [/] switch task list · m more"),
	}
	return displayText(strings.Join(wrapDetailLines(lines, m.detailTextWidth()), "\n"))
}
