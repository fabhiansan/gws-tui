package daemon

import (
	"context"
	"encoding/json"

	"github.com/fabhiansan/gws-tui/internal/api"
)

func (s *Server) dispatchTaskLists(ctx context.Context) (api.Page[api.TaskList], error) {
	page, err := s.client.TaskLists(ctx)
	if err == nil {
		s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) { snapshot.TaskLists = page.Items })
	}
	return page, err
}

func (s *Server) dispatchTasks(ctx context.Context, params json.RawMessage) (api.Page[api.TaskItem], error) {
	var p api.TasksParams
	if err := decode(params, &p); err != nil {
		return api.Page[api.TaskItem]{}, err
	}
	page, err := s.client.Tasks(ctx, p.Query)
	if err == nil {
		s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
			snapshot.Tasks = page
			snapshot.TaskListID = p.Query.TaskListID
		})
	}
	return page, err
}

func (s *Server) dispatchSetTaskCompleted(ctx context.Context, params json.RawMessage) (api.TaskItem, error) {
	var p api.SetTaskCompletedParams
	if err := decode(params, &p); err != nil {
		return api.TaskItem{}, err
	}
	task, err := s.client.SetTaskCompleted(ctx, p.TaskListID, p.TaskID, p.Completed)
	if err == nil {
		s.applyTask(task)
		s.broadcast("tasks.changed", task)
	}
	return task, err
}

func (s *Server) dispatchDeleteTask(ctx context.Context, params json.RawMessage) (any, error) {
	var p api.TaskIDParams
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	err := s.client.DeleteTask(ctx, p.TaskListID, p.TaskID)
	if err == nil {
		s.removeTask(p.TaskListID, p.TaskID)
		s.broadcast("tasks.changed", map[string]string{"task_list_id": p.TaskListID, "task_id": p.TaskID, "action": "deleted"})
	}
	return nil, err
}
