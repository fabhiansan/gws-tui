package tui

import "github.com/fabhiansan/gws-tui/internal/api"

func (m Model) selectedTaskList() api.TaskList {
	if len(m.taskLists) == 0 {
		return api.TaskList{}
	}
	return m.taskLists[clamp(m.taskListIndex, len(m.taskLists))]
}

func (m Model) selectedTask() api.TaskItem {
	if len(m.tasks) == 0 {
		return api.TaskItem{}
	}
	return m.tasks[clamp(m.selected[FeatureTasks], len(m.tasks))]
}

func indexOfTaskList(lists []api.TaskList, id string) int {
	if id == "" {
		return 0
	}
	for i, list := range lists {
		if list.ID == id {
			return i
		}
	}
	return 0
}
