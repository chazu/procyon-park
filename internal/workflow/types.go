// Package workflow provides the workflow engine types, CUE schema, and loader
// for procyon-park automated workflow definitions.
//
// Workflows are automated sequences of steps that the daemon executes.
// Each workflow file defines a single workflow conforming to #Workflow.
//
// The loader uses a two-phase approach:
//  1. Parse phase: validates structure, extracts params (stubs _input/_ctx)
//  2. Resolve phase: injects concrete params, validates with cue.Concrete(true)
//
// Aspects are cross-cutting concerns applied at load time as a pure
// transformation of the step list (single-pass per-aspect, AND-match semantics).
package workflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

// Workflow is a parsed workflow definition.
type Workflow struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Params      map[string]Param `json:"params"`
	Steps       []Step           `json:"steps"`
	Aspects     []Aspect         `json:"aspects,omitempty"`
	Source      string           `json:"source"`   // "global" or repo path
	FilePath    string           `json:"filePath"`
}

// Param describes a workflow parameter.
type Param struct {
	Type     string      `json:"type"`     // "string", "int", "bool"
	Required bool        `json:"required"`
	Default  interface{} `json:"default,omitempty"`
}

// Step is a single step in a workflow definition.
type Step struct {
	Type    string          `json:"type"`              // spawn, wait, evaluate, dismiss, gate
	Timeout string          `json:"timeout,omitempty"` // optional execution timeout (e.g. "30s", "5m")
	Config  json.RawMessage `json:"config"`
}

// TaskDef defines a task to be created for a spawned agent.
type TaskDef struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	TaskType    string `json:"taskType,omitempty"`
}

// SpawnConfig is the configuration for a spawn step.
type SpawnConfig struct {
	Role   string  `json:"role"`
	Task   TaskDef `json:"task"`
	Repo   string  `json:"repo,omitempty"`
	Branch string  `json:"branch,omitempty"`
}

// WaitConfig is the configuration for a wait step.
type WaitConfig struct {
	Timeout string `json:"timeout"`
}

// EvaluateConfig is the configuration for an evaluate step.
type EvaluateConfig struct {
	Expect json.RawMessage `json:"expect"`
}

// DismissConfig is the configuration for a dismiss step.
type DismissConfig struct{}

// Aspect declares a cross-cutting concern that injects steps before/after
// matching steps. Aspects are applied at load time as a pure transformation
// of the step list.
type Aspect struct {
	Match  AspectMatch `json:"match"`
	Before []Step      `json:"before,omitempty"`
	After  []Step      `json:"after,omitempty"`
}

// AspectMatch defines the criteria for matching steps in an aspect.
// All non-empty fields must match (AND semantics).
type AspectMatch struct {
	Type string `json:"type,omitempty"` // match by step type (e.g. "spawn", "wait")
	Name string `json:"name,omitempty"` // match by step name pattern (glob)
	Role string `json:"role,omitempty"` // match by agent role in spawn steps
}

// GateConfig is the configuration for a gate step.
type GateConfig struct {
	GateType  string   `json:"gateType"`             // "human" or "timer"
	Approvers []string `json:"approvers,omitempty"`   // human gate: list of approvers
	Prompt    string   `json:"prompt,omitempty"`      // human gate: approval prompt
	Duration  string   `json:"duration,omitempty"`    // timer gate: wait duration (e.g. "5m")
	Timeout   string   `json:"timeout,omitempty"`     // human gate: approval timeout
}

// WorkflowContext holds implicit context that steps read and write by convention.
// This enables context passing between steps without brittle index-based references.
type WorkflowContext struct {
	// TaskID is the primary task driving the workflow (from _input if available).
	TaskID string `json:"taskId,omitempty"`
	// ActiveAgent is the most recently spawned agent name.
	ActiveAgent string `json:"activeAgent,omitempty"`
	// ActiveBranch is the most recently created/used branch.
	ActiveBranch string `json:"activeBranch,omitempty"`
	// ActiveRepo is the repository the workflow is running in.
	ActiveRepo string `json:"activeRepo,omitempty"`
	// PreviousOutput holds the output from the last completed step (typically wait).
	PreviousOutput json.RawMessage `json:"previousOutput,omitempty"`
}

// Instance represents a running or completed workflow execution.
type Instance struct {
	ID           string            `json:"id"`
	WorkflowName string            `json:"workflowName"`
	RepoName     string            `json:"repoName"`
	RepoRoot     string            `json:"repoRoot,omitempty"`
	Status       InstanceStatus    `json:"status"`
	CurrentStep  int               `json:"currentStep"`
	Params       map[string]string `json:"params"`
	Context      WorkflowContext   `json:"context"`
	StepResults  []StepResult      `json:"stepResults"`
	Error        string            `json:"error,omitempty"`
	StartedAt    time.Time         `json:"startedAt"`
	CompletedAt  *time.Time        `json:"completedAt,omitempty"`
}

// InstanceStatus represents the status of a workflow instance.
type InstanceStatus string

// Instance status constants.
const (
	StatusPending   InstanceStatus = "pending"
	StatusRunning   InstanceStatus = "running"
	StatusCompleted InstanceStatus = "completed"
	StatusFailed    InstanceStatus = "failed"
	StatusCancelled InstanceStatus = "cancelled"
)

// StepResult captures the outcome of executing a single step.
type StepResult struct {
	StepIndex int             `json:"stepIndex"`
	StepType  string          `json:"stepType"`
	Status    string          `json:"status"`
	Output    json.RawMessage `json:"output,omitempty"`
	Error     string          `json:"error,omitempty"`
	StartedAt time.Time       `json:"startedAt"`
	EndedAt   *time.Time      `json:"endedAt,omitempty"`
}

// StepHandler defines the interface for executing a workflow step.
type StepHandler interface {
	Execute(ctx context.Context, instance *Instance, stepIndex int, config json.RawMessage) (*StepResult, error)
}

// WorkflowSummary is a compact representation of a workflow for listing.
type WorkflowSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	StepCount   int    `json:"stepCount"`
}

// GenerateInstanceID creates a "wf-" prefixed random ID.
func GenerateInstanceID() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return "wf-" + hex.EncodeToString(bytes)
}
