package prime

import (
	"strings"
	"testing"
)

func TestAgentPromptAddendum_ContainsAllSections(t *testing.T) {
	result := AgentPromptAddendum("myrepo", "task-123")

	sections := []string{
		"BBS TUPLESPACE PROTOCOL:",
		"BEFORE STARTING:",
		"ATOMIC CLAIMING:",
		"WHILE WORKING:",
		"ON COMPLETION (mandatory",
		"PULSE CADENCE:",
		"NOTIFICATION PIGGYBACKING:",
		"BBS CLI REFERENCE:",
	}

	for _, sec := range sections {
		if !strings.Contains(result, sec) {
			t.Errorf("missing section %q in addendum", sec)
		}
	}
}

func TestAgentPromptAddendum_ParameterizedScope(t *testing.T) {
	result := AgentPromptAddendum("myrepo", "task-123")

	// Scope should appear in scan commands
	if !strings.Contains(result, "pp bbs scan fact myrepo") {
		t.Error("expected parameterized scope in scan fact command")
	}
	if !strings.Contains(result, "pp bbs scan convention myrepo") {
		t.Error("expected parameterized scope in scan convention command")
	}

	// Scope and taskID in atomic claiming
	if !strings.Contains(result, "pp bbs in available myrepo task-123 --timeout 5s") {
		t.Error("expected parameterized scope/taskID in atomic claim consume")
	}
	if !strings.Contains(result, "pp bbs out claim myrepo task-123") {
		t.Error("expected parameterized scope/taskID in claim write")
	}
	if !strings.Contains(result, "bd update task-123 --status=in_progress") {
		t.Error("expected parameterized taskID in bd update")
	}
}

func TestAgentPromptAddendum_ParameterizedTaskID(t *testing.T) {
	result := AgentPromptAddendum("scope-x", "beads-abc")

	// Task ID in trail-leaving
	if !strings.Contains(result, `"task":"beads-abc"`) {
		t.Error("expected taskID in trail-leaving obstacle/need/artifact tuples")
	}

	// Task ID in completion event
	if !strings.Contains(result, "task_done") {
		t.Error("expected task_done event in completion protocol")
	}
	if !strings.Contains(result, `"task":"beads-abc"`) {
		t.Error("expected taskID in completion event")
	}
}

func TestAgentPromptAddendum_CLIReference(t *testing.T) {
	result := AgentPromptAddendum("repo", "task")

	commands := []string{
		"pp bbs out",
		"pp bbs in",
		"pp bbs rd",
		"pp bbs scan",
		"pp bbs pulse",
		"pp bbs seed-available",
	}

	for _, cmd := range commands {
		if !strings.Contains(result, cmd) {
			t.Errorf("CLI reference missing command %q", cmd)
		}
	}

	if !strings.Contains(result, "--agent-id $PP_AGENT_NAME") {
		t.Error("should mention --agent-id for notification piggybacking")
	}
}

func TestAgentPromptAddendum_DifferentParams(t *testing.T) {
	a := AgentPromptAddendum("repo-a", "task-1")
	b := AgentPromptAddendum("repo-b", "task-2")

	if a == b {
		t.Error("different parameters should produce different output")
	}

	if !strings.Contains(a, "repo-a") || strings.Contains(a, "repo-b") {
		t.Error("scope-a addendum should only reference repo-a")
	}
	if !strings.Contains(b, "repo-b") || strings.Contains(b, "repo-a") {
		t.Error("scope-b addendum should only reference repo-b")
	}
}
