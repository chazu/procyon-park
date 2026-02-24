package prime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPipeline_FullPipeline(t *testing.T) {
	store := newTestStore(t)
	insertTuple(t, store, "fact", "myrepo", "go-version", `{"ver":"1.21"}`, "furniture")
	insertTuple(t, store, "convention", "myrepo", "test-first", `{"rule":"always"}`, "furniture")

	cfg := PipelineConfig{
		Role: "cub",
		Data: TemplateData{
			Role:      "cub",
			AgentName: "Moss",
			Repo:      "myrepo",
			TaskID:    "task-42",
			Branch:    "agent/Moss/task-42",
			Worktree:  "/tmp/worktrees/Moss",
			EnvPrefix: "PP",
		},
		Scope:  "myrepo",
		TaskID: "task-42",
		Store:  store,
	}

	result, err := RunPipeline(cfg)
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}

	// Should contain rendered template data.
	if !strings.Contains(result, "Moss") {
		t.Error("expected agent name in output")
	}
	if !strings.Contains(result, "myrepo") {
		t.Error("expected repo in output")
	}

	// Should contain BBS addendum.
	if !strings.Contains(result, "BBS TUPLESPACE PROTOCOL:") {
		t.Error("expected BBS addendum section")
	}

	// Should contain tuplespace context.
	if !strings.Contains(result, "BBS TUPLESPACE CONTEXT") {
		t.Error("expected tuplespace context section")
	}
	if !strings.Contains(result, "go-version") {
		t.Error("expected fact from store in context")
	}
}

func TestRunPipeline_NoStore(t *testing.T) {
	cfg := PipelineConfig{
		Role: "cub",
		Data: TemplateData{
			Role:      "cub",
			AgentName: "Test",
			Repo:      "repo",
			TaskID:    "t-1",
			Branch:    "b",
			Worktree:  "/tmp/t",
			EnvPrefix: "PP",
		},
		Scope:  "repo",
		TaskID: "t-1",
		Store:  nil,
	}

	result, err := RunPipeline(cfg)
	if err != nil {
		t.Fatalf("RunPipeline without store: %v", err)
	}

	// Should still have rendered template and addendum, just no context.
	if !strings.Contains(result, "BBS TUPLESPACE PROTOCOL:") {
		t.Error("expected addendum even without store")
	}
	// The context header from BuildAgentContext starts with this exact line.
	if strings.Contains(result, "BBS TUPLESPACE CONTEXT (pre-read at launch):") {
		t.Error("should not have context section without store")
	}
}

func TestRunPipeline_UnknownRoleFallback(t *testing.T) {
	cfg := PipelineConfig{
		Role: "nonexistent",
		Data: TemplateData{
			Role:      "nonexistent",
			AgentName: "Ghost",
			Repo:      "repo",
			TaskID:    "t-1",
			Branch:    "b",
			Worktree:  "/tmp/t",
			EnvPrefix: "PP",
		},
		Scope:  "repo",
		TaskID: "t-1",
	}

	result, err := RunPipeline(cfg)
	if err != nil {
		t.Fatalf("RunPipeline with unknown role: %v", err)
	}

	// Should fall back to cub template.
	if !strings.Contains(result, "autonomous contributor") {
		t.Error("expected cub fallback content")
	}
}

func TestApplyUserOverride(t *testing.T) {
	dir := t.TempDir()
	role := "cub"
	customContent := "Custom instructions for {{.AgentName}}"

	// Write a user override file.
	if err := os.WriteFile(filepath.Join(dir, role+".txt"), []byte(customContent), 0o644); err != nil {
		t.Fatal(err)
	}

	original := "Original embedded template"

	// Override should replace the template.
	result := applyUserOverride(original, role, dir)
	if result != customContent {
		t.Errorf("expected override content, got %q", result)
	}

	// Non-existent role should keep original.
	result = applyUserOverride(original, "king", dir)
	if result != original {
		t.Errorf("expected original for missing override, got %q", result)
	}

	// Empty dir should keep original.
	result = applyUserOverride(original, role, "")
	if result != original {
		t.Errorf("expected original for empty dir, got %q", result)
	}
}

func TestRunPipeline_UserOverridePrecedence(t *testing.T) {
	dir := t.TempDir()
	customTemplate := "CUSTOM: Hello {{.AgentName}}, you work on {{.Repo}}"
	if err := os.WriteFile(filepath.Join(dir, "cub.txt"), []byte(customTemplate), 0o644); err != nil {
		t.Fatal(err)
	}

	store := newTestStore(t)
	insertTuple(t, store, "fact", "repo", "test-fact", `{"x":"1"}`, "furniture")

	cfg := PipelineConfig{
		Role: "cub",
		Data: TemplateData{
			Role:      "cub",
			AgentName: "Moss",
			Repo:      "repo",
			TaskID:    "t-1",
			Branch:    "b",
			Worktree:  "/tmp/t",
			EnvPrefix: "PP",
		},
		Scope:           "repo",
		TaskID:          "t-1",
		Store:           store,
		InstructionsDir: dir,
	}

	result, err := RunPipeline(cfg)
	if err != nil {
		t.Fatalf("RunPipeline with override: %v", err)
	}

	// Custom template should be rendered.
	if !strings.Contains(result, "CUSTOM: Hello Moss") {
		t.Error("expected custom template content")
	}

	// Addendum and context should still be appended.
	if !strings.Contains(result, "BBS TUPLESPACE PROTOCOL:") {
		t.Error("custom template should still get addendum")
	}
	if !strings.Contains(result, "BBS TUPLESPACE CONTEXT") {
		t.Error("custom template should still get context")
	}
}

func TestExportTemplates(t *testing.T) {
	dir := t.TempDir()

	exported, err := ExportTemplates(dir)
	if err != nil {
		t.Fatalf("ExportTemplates: %v", err)
	}

	// Should export all known templates.
	roles, _ := ListRoles()
	if len(exported) != len(roles) {
		t.Errorf("expected %d exported files, got %d", len(roles), len(exported))
	}

	// Verify files exist and have content.
	for _, path := range exported {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("cannot read exported file %s: %v", path, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("exported file %s is empty", path)
		}
	}
}

func TestExportTemplates_NoOverwrite(t *testing.T) {
	dir := t.TempDir()

	// Pre-create a customized file.
	customPath := filepath.Join(dir, "cub.txt")
	customContent := "my custom cub template"
	if err := os.WriteFile(customPath, []byte(customContent), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ExportTemplates(dir)
	if err != nil {
		t.Fatalf("ExportTemplates: %v", err)
	}

	// Custom file should not be overwritten.
	data, err := os.ReadFile(customPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != customContent {
		t.Error("ExportTemplates should not overwrite existing files")
	}
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		text string
		want int
	}{
		{"", 0},
		{"a", 1},
		{"abcd", 1},
		{"abcde", 2},
		{strings.Repeat("x", 100), 25},
	}
	for _, tt := range tests {
		got := estimateTokens(tt.text)
		if got != tt.want {
			t.Errorf("estimateTokens(%d chars) = %d, want %d", len(tt.text), got, tt.want)
		}
	}
}

func TestTrimToTokenBudget_UnderBudget(t *testing.T) {
	text := "Short text"
	result := trimToTokenBudget(text, 1000)
	if result != text {
		t.Errorf("text under budget should be unchanged")
	}
}

func TestTrimToTokenBudget_OverBudget(t *testing.T) {
	// Create text that exceeds budget.
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, strings.Repeat("x", 80))
	}
	text := strings.Join(lines, "\n")

	// Budget of 100 tokens = ~400 chars.
	result := trimToTokenBudget(text, 100)

	if !strings.Contains(result, "context trimmed to fit token budget") {
		t.Error("should contain trimming notice")
	}
	if estimateTokens(result) > 110 { // Small margin for the notice.
		t.Errorf("trimmed result (%d tokens) should be near budget (100)", estimateTokens(result))
	}
}

func TestTrimToTokenBudget_ZeroBudget(t *testing.T) {
	text := "Some text"
	result := trimToTokenBudget(text, 0)
	if result != text {
		t.Error("zero budget should be treated as unlimited")
	}
}

func TestRunPipeline_ContextBudget(t *testing.T) {
	store := newTestStore(t)

	// Insert many facts to exceed a small budget.
	for i := 0; i < 50; i++ {
		insertTuple(t, store, "fact", "repo", strings.Repeat("f", 40),
			`{"data":"`+strings.Repeat("d", 60)+`"}`, "furniture")
	}

	cfg := PipelineConfig{
		Role: "cub",
		Data: TemplateData{
			Role:      "cub",
			AgentName: "Test",
			Repo:      "repo",
			TaskID:    "t-1",
			Branch:    "b",
			Worktree:  "/tmp/t",
			EnvPrefix: "PP",
		},
		Scope:         "repo",
		TaskID:        "t-1",
		Store:         store,
		ContextBudget: 50, // Very small budget.
	}

	result, err := RunPipeline(cfg)
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}

	// The context should have been trimmed.
	if !strings.Contains(result, "context trimmed to fit token budget") {
		t.Error("expected context trimming notice with small budget")
	}
}

func TestLoadTemplateText(t *testing.T) {
	// Known role.
	text, err := loadTemplateText("cub")
	if err != nil {
		t.Fatalf("loadTemplateText(cub): %v", err)
	}
	if !strings.Contains(text, "autonomous contributor") {
		t.Error("cub template should contain expected content")
	}

	// Unknown role falls back.
	text, err = loadTemplateText("nonexistent")
	if err != nil {
		t.Fatalf("loadTemplateText(nonexistent): %v", err)
	}
	if !strings.Contains(text, "autonomous contributor") {
		t.Error("unknown role should fall back to cub")
	}
}

func TestInjectAddendum(t *testing.T) {
	base := "base text"

	// With scope.
	result := injectAddendum(base, "repo", "task-1")
	if !strings.Contains(result, "base text") {
		t.Error("should contain base text")
	}
	if !strings.Contains(result, "BBS TUPLESPACE PROTOCOL:") {
		t.Error("should contain addendum")
	}

	// Empty scope skips addendum.
	result = injectAddendum(base, "", "task-1")
	if result != base {
		t.Error("empty scope should skip addendum")
	}
}

func TestInjectContext(t *testing.T) {
	store := newTestStore(t)
	insertTuple(t, store, "fact", "repo", "test-fact", `{"k":"v"}`, "furniture")

	base := "base text"

	// With store.
	result, err := injectContext(base, store, "repo", "task-1", DefaultContextBudget)
	if err != nil {
		t.Fatalf("injectContext: %v", err)
	}
	if !strings.Contains(result, "test-fact") {
		t.Error("should contain context from store")
	}

	// Nil store.
	result, err = injectContext(base, nil, "repo", "task-1", DefaultContextBudget)
	if err != nil {
		t.Fatalf("injectContext nil store: %v", err)
	}
	if result != base {
		t.Error("nil store should return base unchanged")
	}
}
