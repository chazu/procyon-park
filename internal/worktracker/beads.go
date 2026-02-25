package worktracker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// BeadsTracker implements WorkTracker by shelling out to the bd CLI.
type BeadsTracker struct {
	// WorkDir is the directory in which bd commands are run.
	// If empty, uses the current working directory.
	WorkDir string
}

// NewBeadsTracker creates a BeadsTracker rooted at the given directory.
func NewBeadsTracker(workDir string) *BeadsTracker {
	return &BeadsTracker{WorkDir: workDir}
}

// bdJSON is the intermediate format returned by "bd show --json".
type bdJSON struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Type        string   `json:"type"`
	Priority    int      `json:"priority"`
	Labels      []string `json:"labels"`
	Parent      string   `json:"parent"`
	Assignee    string   `json:"assignee"`
	Notes       string   `json:"notes"`
	BlockedBy   []string `json:"blocked_by"`
	Blocks      []string `json:"blocks"`
}

func (b *bdJSON) toTask() *Task {
	return &Task{
		ID:          b.ID,
		Title:       b.Title,
		Description: b.Description,
		Status:      b.Status,
		Type:        b.Type,
		Priority:    b.Priority,
		Labels:      b.Labels,
		Parent:      b.Parent,
		Assignee:    b.Assignee,
		Notes:       b.Notes,
		BlockedBy:   b.BlockedBy,
		Blocks:      b.Blocks,
	}
}

// run executes a bd command and returns its stdout.
func (bt *BeadsTracker) run(args ...string) ([]byte, error) {
	cmd := exec.Command("bd", args...)
	if bt.WorkDir != "" {
		cmd.Dir = bt.WorkDir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("bd %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(stderr.String()), err)
	}
	return stdout.Bytes(), nil
}

func (bt *BeadsTracker) GetTask(id string) (*Task, error) {
	out, err := bt.run("show", id, "--json")
	if err != nil {
		return nil, err
	}
	var t bdJSON
	if err := json.Unmarshal(out, &t); err != nil {
		return nil, fmt.Errorf("parse bd show output: %w", err)
	}
	return t.toTask(), nil
}

func (bt *BeadsTracker) CreateTask(opts CreateTaskOpts) (*Task, error) {
	args := []string{"create", "--title", opts.Title, "--json"}
	if opts.Description != "" {
		args = append(args, "--description", opts.Description)
	}
	if opts.TaskType != "" {
		args = append(args, "--type", opts.TaskType)
	}
	if opts.Priority >= 0 {
		args = append(args, "--priority", fmt.Sprintf("%d", opts.Priority))
	}
	for _, label := range opts.Labels {
		args = append(args, "--labels", label)
	}
	if opts.Parent != "" {
		args = append(args, "--parent", opts.Parent)
	}
	out, err := bt.run(args...)
	if err != nil {
		return nil, err
	}
	var t bdJSON
	if err := json.Unmarshal(out, &t); err != nil {
		return nil, fmt.Errorf("parse bd create output: %w", err)
	}
	return t.toTask(), nil
}

func (bt *BeadsTracker) CloseTask(id string) error {
	_, err := bt.run("close", id)
	return err
}

func (bt *BeadsTracker) UpdateTask(id string, opts UpdateTaskOpts) error {
	args := []string{"update", id}
	if opts.Status != nil {
		args = append(args, "--status", *opts.Status)
	}
	if opts.Assignee != nil {
		args = append(args, "--assignee", *opts.Assignee)
	}
	if opts.Notes != nil {
		args = append(args, "--notes", *opts.Notes)
	}
	if opts.Title != nil {
		args = append(args, "--title", *opts.Title)
	}
	if opts.Description != nil {
		args = append(args, "--description", *opts.Description)
	}
	_, err := bt.run(args...)
	return err
}

func (bt *BeadsTracker) ListReady() ([]Task, error) {
	return bt.listJSON("ready", "--json")
}

func (bt *BeadsTracker) ListByStatus(status string) ([]Task, error) {
	return bt.listJSON("list", "--status="+status, "--json")
}

func (bt *BeadsTracker) ListByParent(epicID string) ([]Task, error) {
	return bt.listJSON("list", "--parent="+epicID, "--json")
}

func (bt *BeadsTracker) AddDependency(taskID, dependsOnID string) error {
	_, err := bt.run("dep", "add", taskID, dependsOnID)
	return err
}

// listJSON runs a bd list-style command with --json and parses the result.
func (bt *BeadsTracker) listJSON(args ...string) ([]Task, error) {
	out, err := bt.run(args...)
	if err != nil {
		return nil, err
	}
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return nil, nil
	}
	var items []bdJSON
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, fmt.Errorf("parse bd list output: %w", err)
	}
	tasks := make([]Task, len(items))
	for i, item := range items {
		tasks[i] = *item.toTask()
	}
	return tasks, nil
}

// HasBeadsDir checks if a .beads directory exists in the given path.
func HasBeadsDir(dir string) bool {
	cmd := exec.Command("test", "-d", dir+"/.beads")
	return cmd.Run() == nil
}
