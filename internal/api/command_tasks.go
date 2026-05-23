package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

type rawTaskList struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Updated string `json:"updated"`
}

type rawTaskItem struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Notes     string `json:"notes"`
	Status    string `json:"status"`
	Due       string `json:"due"`
	Completed string `json:"completed"`
	Updated   string `json:"updated"`
	Parent    string `json:"parent"`
	Position  string `json:"position"`
}

func (c *CommandClient) TaskLists(ctx context.Context) (Page[TaskList], error) {
	params, _ := json.Marshal(map[string]any{"maxResults": 100})
	var raw struct {
		Items         []rawTaskList `json:"items"`
		TaskLists     []rawTaskList `json:"taskLists"`
		NextPageToken string        `json:"nextPageToken"`
	}
	if err := c.runJSON(ctx, &raw, "tasks", "tasklists", "list", "--params", string(params), "--format", "json"); err != nil {
		return Page[TaskList]{}, err
	}
	source := raw.Items
	if len(source) == 0 {
		source = raw.TaskLists
	}
	items := make([]TaskList, 0, len(source))
	for _, item := range source {
		items = append(items, TaskList{
			ID:      item.ID,
			Title:   fallback(item.Title, "(untitled)"),
			Updated: parseRFC3339(item.Updated),
		})
	}
	return Page[TaskList]{Items: items, NextPageToken: raw.NextPageToken}, nil
}

func (c *CommandClient) Tasks(ctx context.Context, q TaskQuery) (Page[TaskItem], error) {
	if strings.TrimSpace(q.TaskListID) == "" {
		return Page[TaskItem]{}, errors.New("task list id is required")
	}
	params := map[string]any{
		"tasklist":      q.TaskListID,
		"maxResults":    100,
		"showCompleted": true,
		"showDeleted":   false,
	}
	if q.PageToken != "" {
		params["pageToken"] = q.PageToken
	}
	payload, _ := json.Marshal(params)
	var raw struct {
		Items         []rawTaskItem `json:"items"`
		Tasks         []rawTaskItem `json:"tasks"`
		NextPageToken string        `json:"nextPageToken"`
	}
	if err := c.runJSON(ctx, &raw, "tasks", "tasks", "list", "--params", string(payload), "--format", "json"); err != nil {
		return Page[TaskItem]{}, err
	}
	source := raw.Items
	if len(source) == 0 {
		source = raw.Tasks
	}
	items := make([]TaskItem, 0, len(source))
	for _, item := range source {
		items = append(items, taskItemFromRaw(q.TaskListID, item))
	}
	return Page[TaskItem]{Items: items, NextPageToken: raw.NextPageToken}, nil
}

func (c *CommandClient) SetTaskCompleted(ctx context.Context, taskListID, taskID string, completed bool) (TaskItem, error) {
	if strings.TrimSpace(taskListID) == "" {
		return TaskItem{}, errors.New("task list id is required")
	}
	if strings.TrimSpace(taskID) == "" {
		return TaskItem{}, errors.New("task id is required")
	}
	status := "needsAction"
	if completed {
		status = "completed"
	}
	params, _ := json.Marshal(map[string]any{"tasklist": taskListID, "task": taskID})
	body, _ := json.Marshal(map[string]any{"status": status})
	var raw rawTaskItem
	if err := c.runJSON(ctx, &raw, "tasks", "tasks", "patch", "--params", string(params), "--json", string(body), "--format", "json"); err != nil {
		return TaskItem{}, err
	}
	return taskItemFromRaw(taskListID, raw), nil
}

func (c *CommandClient) DeleteTask(ctx context.Context, taskListID, taskID string) error {
	if strings.TrimSpace(taskListID) == "" {
		return errors.New("task list id is required")
	}
	if strings.TrimSpace(taskID) == "" {
		return errors.New("task id is required")
	}
	params, _ := json.Marshal(map[string]any{"tasklist": taskListID, "task": taskID})
	return c.runVoid(ctx, "tasks", "tasks", "delete", "--params", string(params), "--format", "json")
}

func taskItemFromRaw(taskListID string, item rawTaskItem) TaskItem {
	return TaskItem{
		ID:         item.ID,
		TaskListID: taskListID,
		Title:      fallback(item.Title, "(untitled task)"),
		Notes:      item.Notes,
		Status:     item.Status,
		Due:        parseRFC3339(item.Due),
		Completed:  parseRFC3339(item.Completed),
		Updated:    parseRFC3339(item.Updated),
		Parent:     item.Parent,
		Position:   item.Position,
	}
}
