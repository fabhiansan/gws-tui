package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fabhiansan/gws-tui/internal/api"
)

func (m Model) moveTaskList(delta int) (Model, tea.Cmd) {
	if len(m.taskLists) == 0 {
		m.toast = "no task lists"
		return m, nil
	}
	next := clamp(m.taskListIndex+delta, len(m.taskLists))
	if next == m.taskListIndex {
		return m, nil
	}
	m.taskListIndex = next
	m.selected[FeatureTasks] = 0
	return m.loadSelectedTaskList()
}

func (m Model) loadSelectedTaskList() (Model, tea.Cmd) {
	list := m.selectedTaskList()
	if list.ID == "" {
		m.tasks = nil
		m.taskNext = ""
		return m, nil
	}
	m.loading = true
	m.tasks = nil
	m.taskNext = ""
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
		defer cancel()

		page, err := m.client.Tasks(ctx, api.TaskQuery{TaskListID: list.ID})
		return featureRefreshedMsg{
			feature:    FeatureTasks,
			taskLists:  api.Page[api.TaskList]{Items: m.taskLists},
			tasks:      page,
			taskListID: list.ID,
			err:        err,
		}
	}
}

func (m *Model) applyTask(task api.TaskItem) {
	if task.ID == "" {
		return
	}
	for i := range m.tasks {
		if m.tasks[i].ID == task.ID {
			m.tasks[i] = task
			return
		}
	}
	m.tasks = append(m.tasks, task)
}

func (m *Model) removeTask(taskListID, taskID string) {
	if taskID == "" {
		return
	}
	out := m.tasks[:0]
	for _, task := range m.tasks {
		if task.ID == taskID && (taskListID == "" || task.TaskListID == "" || task.TaskListID == taskListID) {
			continue
		}
		out = append(out, task)
	}
	m.tasks = out
}

func (m Model) toggleSelectedTaskCompleted() (Model, tea.Cmd) {
	list := m.selectedTaskList()
	task := m.selectedTask()
	if list.ID == "" || task.ID == "" {
		return m, nil
	}
	completed := !strings.EqualFold(task.Status, "completed")
	return m, func() tea.Msg {
		updated, err := m.client.SetTaskCompleted(m.ctx, list.ID, task.ID, completed)
		label := "task unchecked"
		if completed {
			label = "task completed"
		}
		return taskActionMsg{task: updated, taskListID: list.ID, taskID: task.ID, err: err, label: label}
	}
}

func (m Model) deleteSelectedTask() (Model, tea.Cmd) {
	list := m.selectedTaskList()
	task := m.selectedTask()
	if list.ID == "" || task.ID == "" {
		return m, nil
	}
	return m, func() tea.Msg {
		err := m.client.DeleteTask(m.ctx, list.ID, task.ID)
		return taskActionMsg{taskListID: list.ID, taskID: task.ID, deleted: true, err: err, label: "task deleted"}
	}
}

func (m *Model) handleTaskAction(msg taskActionMsg) []tea.Cmd {
	var cmds []tea.Cmd
	if msg.err != nil {
		m.err = msg.err.Error()
	} else {
		m.toast = msg.label
		if msg.deleted {
			m.removeTask(msg.taskListID, msg.taskID)
		} else {
			m.applyTask(msg.task)
		}
		m.clampSelections()
		m.persistWorkspaceCache()
	}

	return cmds
}
