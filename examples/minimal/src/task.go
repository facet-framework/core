package tasks

import (
	"errors"
	"strings"
)

// CreateTask validates and trims the title of an in-memory example task.
//
// @facet: demo/tasks/create-task
// @facet-role: primary
func CreateTask(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", errors.New("a task title is required")
	}
	return title, nil
}
