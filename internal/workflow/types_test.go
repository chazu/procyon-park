package workflow

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestWorkflowJSONRoundTrip(t *testing.T) {
	w := Workflow{
		Name:        "deploy",
		Description: "Deploy to production",
		Params: map[string]Param{
			"env": {Type: "string", Required: true},
		},
		Steps: []Step{
			{Type: "spawn", Config: json.RawMessage(`{"role":"cub","task":{"title":"build"}}`)},
			{Type: "wait", Config: json.RawMessage(`{"timeout":"5m"}`)},
		},
		Source:   "global",
		FilePath: "/workflows/deploy.cue",
	}

	data, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Workflow
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Name != w.Name {
		t.Errorf("Name = %q, want %q", got.Name, w.Name)
	}
	if got.Description != w.Description {
		t.Errorf("Description = %q, want %q", got.Description, w.Description)
	}
	if got.Source != w.Source {
		t.Errorf("Source = %q, want %q", got.Source, w.Source)
	}
	if len(got.Steps) != len(w.Steps) {
		t.Errorf("len(Steps) = %d, want %d", len(got.Steps), len(w.Steps))
	}
	if len(got.Params) != len(w.Params) {
		t.Errorf("len(Params) = %d, want %d", len(got.Params), len(w.Params))
	}
	p, ok := got.Params["env"]
	if !ok {
		t.Fatal("missing param 'env'")
	}
	if p.Type != "string" || !p.Required {
		t.Errorf("param env = %+v, want type=string required=true", p)
	}
}

func TestInstanceJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	completed := now.Add(5 * time.Minute)

	inst := Instance{
		ID:           "wf-abc123",
		WorkflowName: "deploy",
		RepoName:     "myrepo",
		Status:       StatusCompleted,
		CurrentStep:  2,
		Params:       map[string]string{"env": "prod"},
		StepResults: []StepResult{
			{
				StepIndex: 0,
				StepType:  "spawn",
				Status:    "completed",
				Output:    json.RawMessage(`{"agentId":"a1"}`),
				StartedAt: now,
				EndedAt:   &completed,
			},
		},
		StartedAt:   now,
		CompletedAt: &completed,
	}

	data, err := json.Marshal(inst)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Instance
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.ID != inst.ID {
		t.Errorf("ID = %q, want %q", got.ID, inst.ID)
	}
	if got.Status != inst.Status {
		t.Errorf("Status = %q, want %q", got.Status, inst.Status)
	}
	if got.WorkflowName != inst.WorkflowName {
		t.Errorf("WorkflowName = %q, want %q", got.WorkflowName, inst.WorkflowName)
	}
	if len(got.StepResults) != 1 {
		t.Fatalf("len(StepResults) = %d, want 1", len(got.StepResults))
	}
	if got.StepResults[0].StepType != "spawn" {
		t.Errorf("StepResults[0].StepType = %q, want %q", got.StepResults[0].StepType, "spawn")
	}
	if got.CompletedAt == nil {
		t.Fatal("CompletedAt is nil, want non-nil")
	}
}

func TestInstanceStatusConstants(t *testing.T) {
	tests := []struct {
		status InstanceStatus
		want   string
	}{
		{StatusPending, "pending"},
		{StatusRunning, "running"},
		{StatusCompleted, "completed"},
		{StatusFailed, "failed"},
		{StatusCancelled, "cancelled"},
	}
	for _, tt := range tests {
		if string(tt.status) != tt.want {
			t.Errorf("status = %q, want %q", tt.status, tt.want)
		}
	}
}

func TestGenerateInstanceID(t *testing.T) {
	id := GenerateInstanceID()

	if !strings.HasPrefix(id, "wf-") {
		t.Errorf("ID %q does not have 'wf-' prefix", id)
	}

	// "wf-" (3 chars) + 16 hex chars = 19 total
	if len(id) != 19 {
		t.Errorf("ID length = %d, want 19", len(id))
	}

	hexPart := id[3:]
	for _, c := range hexPart {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("ID %q contains non-hex char %q", id, string(c))
		}
	}

	// Uniqueness: two IDs should differ.
	id2 := GenerateInstanceID()
	if id == id2 {
		t.Errorf("two generated IDs are identical: %q", id)
	}
}

func TestSpawnConfigMarshal(t *testing.T) {
	cfg := SpawnConfig{
		Role:   "cub",
		Task:   TaskDef{Title: "build the project", Description: "Full build", TaskType: "feature"},
		Repo:   "myrepo",
		Branch: "feature-x",
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got SpawnConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Role != cfg.Role {
		t.Errorf("Role = %q, want %q", got.Role, cfg.Role)
	}
	if got.Task.Title != cfg.Task.Title {
		t.Errorf("Task.Title = %q, want %q", got.Task.Title, cfg.Task.Title)
	}
	if got.Task.Description != cfg.Task.Description {
		t.Errorf("Task.Description = %q, want %q", got.Task.Description, cfg.Task.Description)
	}
	if got.Task.TaskType != cfg.Task.TaskType {
		t.Errorf("Task.TaskType = %q, want %q", got.Task.TaskType, cfg.Task.TaskType)
	}
	if got.Repo != cfg.Repo {
		t.Errorf("Repo = %q, want %q", got.Repo, cfg.Repo)
	}
	if got.Branch != cfg.Branch {
		t.Errorf("Branch = %q, want %q", got.Branch, cfg.Branch)
	}
}

func TestSpawnConfigOmitEmpty(t *testing.T) {
	cfg := SpawnConfig{Role: "cub", Task: TaskDef{Title: "test"}}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	s := string(data)
	if strings.Contains(s, `"repo"`) {
		t.Errorf("expected repo to be omitted, got %s", s)
	}
	if strings.Contains(s, `"branch"`) {
		t.Errorf("expected branch to be omitted, got %s", s)
	}
}

func TestWaitConfigMarshal(t *testing.T) {
	cfg := WaitConfig{Timeout: "10m"}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got WaitConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got != cfg {
		t.Errorf("got %+v, want %+v", got, cfg)
	}
}

func TestEvaluateConfigMarshal(t *testing.T) {
	cfg := EvaluateConfig{
		Expect: json.RawMessage(`{"status":"ok","exitCode":0}`),
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got EvaluateConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if string(got.Expect) != string(cfg.Expect) {
		t.Errorf("Expect = %s, want %s", got.Expect, cfg.Expect)
	}
}

func TestStepJSONRoundTrip(t *testing.T) {
	step := Step{
		Type:   "spawn",
		Config: json.RawMessage(`{"role":"cub","task":{"title":"deploy"}}`),
	}

	data, err := json.Marshal(step)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Step
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Type != step.Type {
		t.Errorf("Type = %q, want %q", got.Type, step.Type)
	}
	if string(got.Config) != string(step.Config) {
		t.Errorf("Config = %s, want %s", got.Config, step.Config)
	}
}
