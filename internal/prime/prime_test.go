package prime

import (
	"sort"
	"strings"
	"testing"
)

func TestListRoles(t *testing.T) {
	roles, err := ListRoles()
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}

	expected := []string{"cub", "king", "merge-handler", "reviewer"}
	if len(roles) != len(expected) {
		t.Fatalf("expected %d roles, got %d: %v", len(expected), len(roles), roles)
	}

	if !sort.StringsAreSorted(roles) {
		t.Errorf("roles should be sorted, got %v", roles)
	}

	for i, want := range expected {
		if roles[i] != want {
			t.Errorf("roles[%d] = %q, want %q", i, roles[i], want)
		}
	}
}

func TestLoadTemplate_KnownRoles(t *testing.T) {
	roles := []string{"cub", "king", "reviewer", "merge-handler"}
	for _, role := range roles {
		tmpl, err := LoadTemplate(role)
		if err != nil {
			t.Errorf("LoadTemplate(%q): %v", role, err)
			continue
		}
		if tmpl == nil {
			t.Errorf("LoadTemplate(%q) returned nil template", role)
		}
	}
}

func TestLoadTemplate_UnknownRoleFallsBackToImp(t *testing.T) {
	tmpl, err := LoadTemplate("nonexistent-role")
	if err != nil {
		t.Fatalf("LoadTemplate(nonexistent-role): %v", err)
	}
	if tmpl.Name() != "cub.txt" {
		t.Errorf("expected fallback to cub.txt, got %q", tmpl.Name())
	}
}

func TestRenderTemplate_AllFields(t *testing.T) {
	data := TemplateData{
		Role:      "cub",
		AgentName: "Sprocket",
		Repo:      "test-repo",
		TaskID:    "test-123",
		Branch:    "agent/Sprocket/test-123",
		Worktree:  "/tmp/worktrees/test-repo/Sprocket",
		EnvPrefix: "PP",
	}

	result, err := RenderTemplate("cub", data)
	if err != nil {
		t.Fatalf("RenderTemplate: %v", err)
	}

	// Verify all template data fields appear in the output.
	checks := map[string]string{
		"AgentName": data.AgentName,
		"Repo":      data.Repo,
		"TaskID":    data.TaskID,
		"Branch":    data.Branch,
		"Worktree":  data.Worktree,
		"EnvPrefix": data.EnvPrefix,
	}

	for field, value := range checks {
		if !strings.Contains(result, value) {
			t.Errorf("rendered output missing %s value %q", field, value)
		}
	}
}

func TestRenderTemplate_EachRole(t *testing.T) {
	data := TemplateData{
		Role:      "test-role",
		AgentName: "Widget",
		Repo:      "my-repo",
		TaskID:    "task-42",
		Branch:    "agent/Widget/task-42",
		Worktree:  "/worktrees/Widget",
		EnvPrefix: "PP",
	}

	roles := []string{"cub", "king", "reviewer", "merge-handler"}
	for _, role := range roles {
		data.Role = role
		result, err := RenderTemplate(role, data)
		if err != nil {
			t.Errorf("RenderTemplate(%q): %v", role, err)
			continue
		}
		if result == "" {
			t.Errorf("RenderTemplate(%q) returned empty string", role)
			continue
		}
		// Each role template should contain the agent name and repo.
		if !strings.Contains(result, "Widget") {
			t.Errorf("RenderTemplate(%q) missing agent name", role)
		}
		if !strings.Contains(result, "my-repo") {
			t.Errorf("RenderTemplate(%q) missing repo name", role)
		}
	}
}

func TestRenderTemplate_UnknownRoleFallback(t *testing.T) {
	data := TemplateData{
		Role:      "unknown",
		AgentName: "Ghost",
		Repo:      "repo",
		TaskID:    "task-0",
		Branch:    "agent/Ghost/task-0",
		Worktree:  "/tmp/ghost",
		EnvPrefix: "PP",
	}

	result, err := RenderTemplate("unknown", data)
	if err != nil {
		t.Fatalf("RenderTemplate(unknown): %v", err)
	}

	// Should fall back to cub template — contains cub-specific content.
	if !strings.Contains(result, "autonomous contributor") {
		t.Error("fallback should render cub template content")
	}
	// Should still contain the agent-specific data.
	if !strings.Contains(result, "Ghost") {
		t.Error("fallback should still render agent-specific data")
	}
}

func TestRenderTemplate_EnvPrefixInOutput(t *testing.T) {
	data := TemplateData{
		Role:      "cub",
		AgentName: "Bolt",
		Repo:      "repo",
		TaskID:    "task-1",
		Branch:    "agent/Bolt/task-1",
		Worktree:  "/tmp/bolt",
		EnvPrefix: "PP",
	}

	result, err := RenderTemplate("cub", data)
	if err != nil {
		t.Fatalf("RenderTemplate: %v", err)
	}

	// EnvPrefix should be used to construct env var names.
	if !strings.Contains(result, "PP_AGENT_NAME") {
		t.Error("expected PP_AGENT_NAME in rendered output")
	}
	if !strings.Contains(result, "PP_REPO") {
		t.Error("expected PP_REPO in rendered output")
	}
}
