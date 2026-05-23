package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fabhiansan/gws-tui/internal/api"
)

func (m Model) loadTasksSectionCmd() tea.Cmd {
	taskListIndex := m.taskListIndex
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		taskLists, taskListsErr := m.client.TaskLists(ctx)
		tasks := api.Page[api.TaskItem]{}
		taskListID := ""
		var tasksErr error
		if len(taskLists.Items) > 0 {
			taskListID = taskLists.Items[clamp(taskListIndex, len(taskLists.Items))].ID
			tasks, tasksErr = m.client.Tasks(ctx, api.TaskQuery{TaskListID: taskListID})
		}
		return featureRefreshedMsg{feature: FeatureTasks, taskLists: taskLists, tasks: tasks, taskListID: taskListID, startup: true, err: firstErr(taskListsErr, tasksErr)}
	}
}
