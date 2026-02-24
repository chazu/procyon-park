package prime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Integration tests for the full priming pipeline end-to-end.
// These exercise the complete flow: template rendering → user override →
// addendum injection → context injection → budget trimming.

// --- 1. Render each role template and verify structural sections ---

func TestIntegration_RoleTemplateStructure(t *testing.T) {
	roles, err := ListRoles()
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}

	data := TemplateData{
		Role:      "cub",
		AgentName: "IntegBot",
		Repo:      "test-repo",
		TaskID:    "task-integ",
		Branch:    "agent/IntegBot/task-integ",
		Worktree:  "/tmp/worktrees/IntegBot",
		EnvPrefix: "PP",
	}

	for _, role := range roles {
		t.Run(role, func(t *testing.T) {
			data.Role = role
			result, err := RenderTemplate(role, data)
			if err != nil {
				t.Fatalf("RenderTemplate(%q): %v", role, err)
			}

			if result == "" {
				t.Fatal("rendered template is empty")
			}

			// All role templates should contain the agent name and repo.
			if !strings.Contains(result, "IntegBot") {
				t.Error("missing agent name in rendered template")
			}
			if !strings.Contains(result, "test-repo") {
				t.Error("missing repo name in rendered template")
			}

			// All role templates should have env variable references.
			if !strings.Contains(result, "PP_") {
				t.Error("missing PP_ env prefix references")
			}

			// Role-specific structural sections.
			switch role {
			case "cub":
				assertContains(t, result, "autonomous contributor", "cub identity")
				assertContains(t, result, "WORKFLOW:", "cub workflow section")
				assertContains(t, result, "COMPLETION PROTOCOL:", "cub completion protocol")
				assertContains(t, result, "RESPONSIBILITIES:", "cub responsibilities")
			case "king":
				assertContains(t, result, "coordinator", "king identity")
				assertContains(t, result, "WORKFLOW:", "king workflow section")
				assertContains(t, result, "RESPONSIBILITIES:", "king responsibilities")
			case "reviewer", "merge-handler":
				// These should at minimum render without error and contain env data.
				assertContains(t, result, "PP_AGENT_NAME", "env prefix usage")
			}
		})
	}
}

// --- 2. User override precedence: custom file replaces embedded but still gets addendum + context ---

func TestIntegration_UserOverridePrecedence(t *testing.T) {
	dir := t.TempDir()
	customTemplate := "CUSTOM OVERRIDE: Agent {{.AgentName}} on {{.Repo}}, task {{.TaskID}}"
	if err := os.WriteFile(filepath.Join(dir, "cub.txt"), []byte(customTemplate), 0o644); err != nil {
		t.Fatal(err)
	}

	store := newTestStore(t)
	insertTuple(t, store, "fact", "myrepo", "override-fact", `{"source":"test"}`, "furniture")

	cfg := PipelineConfig{
		Role: "cub",
		Data: TemplateData{
			Role:      "cub",
			AgentName: "OverrideBot",
			Repo:      "myrepo",
			TaskID:    "task-ovr",
			Branch:    "agent/OverrideBot/task-ovr",
			Worktree:  "/tmp/wt/OverrideBot",
			EnvPrefix: "PP",
		},
		Scope:           "myrepo",
		TaskID:          "task-ovr",
		Store:           store,
		InstructionsDir: dir,
	}

	result, err := RunPipeline(cfg)
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}

	// Custom template content should be rendered.
	assertContains(t, result, "CUSTOM OVERRIDE: Agent OverrideBot on myrepo, task task-ovr", "custom rendered content")

	// Original cub template content should NOT be present.
	if strings.Contains(result, "autonomous contributor") {
		t.Error("should not contain original cub template content when override is active")
	}

	// Addendum should still be appended after the override.
	assertContains(t, result, "BBS TUPLESPACE PROTOCOL:", "addendum after override")
	assertContains(t, result, "ATOMIC CLAIMING:", "claiming protocol after override")

	// Context should still be appended after the override.
	assertContains(t, result, "BBS TUPLESPACE CONTEXT", "context after override")
	assertContains(t, result, "override-fact", "fact from store after override")
}

func TestIntegration_UserOverrideOnlyAffectsTargetRole(t *testing.T) {
	dir := t.TempDir()
	// Override only cub, not king.
	if err := os.WriteFile(filepath.Join(dir, "cub.txt"), []byte("CUSTOM IMP"), 0o644); err != nil {
		t.Fatal(err)
	}

	data := TemplateData{
		Role:      "king",
		AgentName: "Bot",
		Repo:      "repo",
		TaskID:    "t-1",
		Branch:    "b",
		Worktree:  "/tmp/t",
		EnvPrefix: "PP",
	}

	cfg := PipelineConfig{
		Role:            "king",
		Data:            data,
		Scope:           "repo",
		TaskID:          "t-1",
		InstructionsDir: dir,
	}

	result, err := RunPipeline(cfg)
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}

	// King should use its own embedded template, not the cub override.
	assertContains(t, result, "coordinator", "king template content")
	if strings.Contains(result, "CUSTOM IMP") {
		t.Error("king should not be affected by cub override")
	}
}

// --- 3. BBS addendum contains all required sections ---

func TestIntegration_AddendumRequiredSections(t *testing.T) {
	cfg := PipelineConfig{
		Role: "cub",
		Data: TemplateData{
			Role:      "cub",
			AgentName: "AddBot",
			Repo:      "myrepo",
			TaskID:    "task-add",
			Branch:    "agent/AddBot/task-add",
			Worktree:  "/tmp/wt",
			EnvPrefix: "PP",
		},
		Scope:  "myrepo",
		TaskID: "task-add",
	}

	result, err := RunPipeline(cfg)
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}

	required := []struct {
		section string
		desc    string
	}{
		{"BBS TUPLESPACE PROTOCOL:", "protocol header"},
		{"BEFORE STARTING:", "orientation section"},
		{"ATOMIC CLAIMING:", "claiming protocol"},
		{"WHILE WORKING:", "trail-leaving section"},
		{"ON COMPLETION (mandatory", "completion protocol"},
		{"PULSE CADENCE:", "pulse cadence"},
		{"NOTIFICATION PIGGYBACKING:", "notification piggybacking"},
		{"BBS CLI REFERENCE:", "CLI reference"},
	}

	for _, req := range required {
		if !strings.Contains(result, req.section) {
			t.Errorf("addendum missing required section: %s (%s)", req.section, req.desc)
		}
	}

	// Verify the addendum is parameterized with scope and task.
	assertContains(t, result, "pp bbs in available myrepo task-add", "parameterized claiming command")
	assertContains(t, result, "pp bbs out claim myrepo task-add", "parameterized claim write")
	assertContains(t, result, "bd update task-add --status=in_progress", "parameterized bd update")

	// CLI reference commands.
	cliCommands := []string{
		"pp bbs out",
		"pp bbs in",
		"pp bbs rd",
		"pp bbs scan",
		"pp bbs pulse",
		"pp bbs seed-available",
	}
	for _, cmd := range cliCommands {
		assertContains(t, result, cmd, "CLI command: "+cmd)
	}
}

// --- 4. Tuplespace context injection ---

func TestIntegration_TuplespaceContextInjection(t *testing.T) {
	store := newTestStore(t)

	// Seed diverse tuple types.
	insertTuple(t, store, "convention", "myrepo", "use-gofmt", `{"detail":"always gofmt"}`, "furniture")
	insertTuple(t, store, "fact", "myrepo", "go-1.22", `{"source":"go.mod"}`, "furniture")
	insertTuple(t, store, "claim", "myrepo", "task-99", `{"agent":"Sprocket","status":"in_progress"}`, "session")
	insertTuple(t, store, "obstacle", "myrepo", "flaky-tests", `{"task":"task-99","detail":"CI intermittent"}`, "session")
	insertTuple(t, store, "need", "myrepo", "api-docs", `{"task":"task-99","detail":"need API docs"}`, "session")

	cfg := PipelineConfig{
		Role: "cub",
		Data: TemplateData{
			Role:      "cub",
			AgentName: "CtxBot",
			Repo:      "myrepo",
			TaskID:    "task-ctx",
			Branch:    "agent/CtxBot/task-ctx",
			Worktree:  "/tmp/wt",
			EnvPrefix: "PP",
		},
		Scope:  "myrepo",
		TaskID: "task-ctx",
		Store:  store,
	}

	result, err := RunPipeline(cfg)
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}

	// All seeded tuples should appear in the context section.
	assertContains(t, result, "CONVENTIONS:", "conventions section in context")
	assertContains(t, result, "use-gofmt", "convention identity")
	assertContains(t, result, "detail=always gofmt", "convention payload")

	assertContains(t, result, "FACTS:", "facts section in context")
	assertContains(t, result, "go-1.22", "fact identity")

	assertContains(t, result, "ACTIVE AGENT ACTIVITY:", "claims section in context")
	assertContains(t, result, "task-99", "claim identity")
	assertContains(t, result, "agent=Sprocket", "claim agent")

	assertContains(t, result, "OBSTACLES:", "obstacles section in context")
	assertContains(t, result, "flaky-tests", "obstacle identity")

	assertContains(t, result, "NEEDS:", "needs section in context")
	assertContains(t, result, "api-docs", "need identity")
}

func TestIntegration_TuplespaceContextScopeIsolation(t *testing.T) {
	store := newTestStore(t)

	insertTuple(t, store, "fact", "repo-a", "fact-from-a", `{"x":"1"}`, "furniture")
	insertTuple(t, store, "fact", "repo-b", "fact-from-b", `{"x":"2"}`, "furniture")

	cfg := PipelineConfig{
		Role: "cub",
		Data: TemplateData{
			Role:      "cub",
			AgentName: "Bot",
			Repo:      "repo-a",
			TaskID:    "t-1",
			Branch:    "b",
			Worktree:  "/tmp/t",
			EnvPrefix: "PP",
		},
		Scope:  "repo-a",
		TaskID: "t-1",
		Store:  store,
	}

	result, err := RunPipeline(cfg)
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}

	assertContains(t, result, "fact-from-a", "facts from requested scope")
	if strings.Contains(result, "fact-from-b") {
		t.Error("context should not include facts from other scopes")
	}
}

func TestIntegration_TaskSpecificEscalations(t *testing.T) {
	store := newTestStore(t)

	// Obstacle referencing our task.
	insertTuple(t, store, "obstacle", "myrepo", "blocked-dep", `{"task":"task-esc","detail":"dep missing"}`, "session")
	// Need referencing our task.
	insertTuple(t, store, "need", "myrepo", "review-needed", `{"task":"task-esc","detail":"needs review"}`, "session")
	// Obstacle for a different task (should not appear in task-specific section).
	insertTuple(t, store, "obstacle", "myrepo", "unrelated-issue", `{"task":"other-task","detail":"unrelated"}`, "session")

	cfg := PipelineConfig{
		Role: "cub",
		Data: TemplateData{
			Role:      "cub",
			AgentName: "EscBot",
			Repo:      "myrepo",
			TaskID:    "task-esc",
			Branch:    "b",
			Worktree:  "/tmp/t",
			EnvPrefix: "PP",
		},
		Scope:  "myrepo",
		TaskID: "task-esc",
		Store:  store,
	}

	result, err := RunPipeline(cfg)
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}

	assertContains(t, result, "TASK-SPECIFIC ESCALATIONS:", "escalations section")
	assertContains(t, result, "blocked-dep", "task-specific obstacle")
	assertContains(t, result, "review-needed", "task-specific need")
}

// --- 5. Context budget trimming ---

func TestIntegration_ContextBudgetTrimming(t *testing.T) {
	store := newTestStore(t)

	// Insert enough tuples to blow past a small token budget.
	for i := 0; i < maxPerCategory; i++ {
		identity := fmt.Sprintf("long-fact-%03d-%s", i, strings.Repeat("x", 50))
		payload := fmt.Sprintf(`{"data":"%s"}`, strings.Repeat("d", 80))
		insertTuple(t, store, "fact", "repo", identity, payload, "furniture")
	}

	cfg := PipelineConfig{
		Role: "cub",
		Data: TemplateData{
			Role:      "cub",
			AgentName: "BudgetBot",
			Repo:      "repo",
			TaskID:    "t-budget",
			Branch:    "b",
			Worktree:  "/tmp/t",
			EnvPrefix: "PP",
		},
		Scope:         "repo",
		TaskID:        "t-budget",
		Store:         store,
		ContextBudget: 30, // Very small — ~120 chars.
	}

	result, err := RunPipeline(cfg)
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}

	// The context should be trimmed.
	assertContains(t, result, "context trimmed to fit token budget", "trimming notice")

	// The template and addendum should still be present (they're not subject to context budget).
	assertContains(t, result, "BudgetBot", "agent name survives budget trim")
	assertContains(t, result, "BBS TUPLESPACE PROTOCOL:", "addendum survives budget trim")
}

func TestIntegration_LargeTuplespaceDoesNotBlowUpContext(t *testing.T) {
	store := newTestStore(t)

	// Insert far more tuples than maxPerCategory.
	for i := 0; i < 100; i++ {
		insertTuple(t, store, "fact", "repo", fmt.Sprintf("fact-%03d", i), `{"n":`+fmt.Sprint(i)+`}`, "furniture")
	}
	for i := 0; i < 50; i++ {
		insertTuple(t, store, "claim", "repo", fmt.Sprintf("task-%03d", i), `{"agent":"Bot","status":"in_progress"}`, "session")
	}

	cfg := PipelineConfig{
		Role: "cub",
		Data: TemplateData{
			Role:      "cub",
			AgentName: "ScaleBot",
			Repo:      "repo",
			TaskID:    "t-scale",
			Branch:    "b",
			Worktree:  "/tmp/t",
			EnvPrefix: "PP",
		},
		Scope:         "repo",
		TaskID:        "t-scale",
		Store:         store,
		ContextBudget: DefaultContextBudget,
	}

	result, err := RunPipeline(cfg)
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}

	// Should succeed without error.
	if result == "" {
		t.Fatal("expected non-empty result")
	}

	// Category cap should apply.
	assertContains(t, result, "more fact tuples omitted", "fact category cap notice")
	assertContains(t, result, "more claim tuples omitted", "claim category cap notice")
}

// --- 6. ExportTemplates writes all roles ---

func TestIntegration_ExportTemplatesAllRoles(t *testing.T) {
	dir := t.TempDir()

	exported, err := ExportTemplates(dir)
	if err != nil {
		t.Fatalf("ExportTemplates: %v", err)
	}

	roles, err := ListRoles()
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}

	if len(exported) != len(roles) {
		t.Fatalf("exported %d files, want %d (one per role)", len(exported), len(roles))
	}

	// Verify each file exists, has content, and its name matches a role.
	for _, path := range exported {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("cannot read %s: %v", path, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("exported file %s is empty", path)
		}

		basename := filepath.Base(path)
		roleName := strings.TrimSuffix(basename, ".txt")
		found := false
		for _, r := range roles {
			if r == roleName {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("exported file %q does not match any known role", basename)
		}
	}
}

func TestIntegration_ExportTemplatesIdempotent(t *testing.T) {
	dir := t.TempDir()

	first, err := ExportTemplates(dir)
	if err != nil {
		t.Fatalf("first export: %v", err)
	}

	// Second export should not overwrite and should return empty (all exist).
	second, err := ExportTemplates(dir)
	if err != nil {
		t.Fatalf("second export: %v", err)
	}

	if len(second) != 0 {
		t.Errorf("second export should skip existing files, got %d exports", len(second))
	}

	// Original files should still be intact.
	for _, path := range first {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("file missing after second export: %v", err)
		}
		if len(data) == 0 {
			t.Errorf("file empty after second export: %s", path)
		}
	}
}

func TestIntegration_ExportedTemplatesRenderCorrectly(t *testing.T) {
	dir := t.TempDir()

	_, err := ExportTemplates(dir)
	if err != nil {
		t.Fatalf("ExportTemplates: %v", err)
	}

	// Use exported templates as user overrides and verify they produce
	// identical output to the embedded templates.
	data := TemplateData{
		Role:      "cub",
		AgentName: "ExportBot",
		Repo:      "repo",
		TaskID:    "t-exp",
		Branch:    "agent/ExportBot/t-exp",
		Worktree:  "/tmp/wt",
		EnvPrefix: "PP",
	}

	// Pipeline with no override (embedded template).
	embeddedCfg := PipelineConfig{
		Role:   "cub",
		Data:   data,
		Scope:  "repo",
		TaskID: "t-exp",
	}
	embeddedResult, err := RunPipeline(embeddedCfg)
	if err != nil {
		t.Fatalf("embedded pipeline: %v", err)
	}

	// Pipeline with exported templates as override dir.
	overrideCfg := PipelineConfig{
		Role:            "cub",
		Data:            data,
		Scope:           "repo",
		TaskID:          "t-exp",
		InstructionsDir: dir,
	}
	overrideResult, err := RunPipeline(overrideCfg)
	if err != nil {
		t.Fatalf("override pipeline: %v", err)
	}

	// Exported templates should produce the same result as embedded.
	if embeddedResult != overrideResult {
		t.Error("exported templates should render identically to embedded templates")
	}
}

// --- 7. Full end-to-end pipeline integration ---

func TestIntegration_FullEndToEnd(t *testing.T) {
	store := newTestStore(t)

	// Set up realistic tuplespace state.
	insertTuple(t, store, "convention", "myproject", "commit-style", `{"rule":"conventional commits"}`, "furniture")
	insertTuple(t, store, "fact", "myproject", "go-1.22", `{"source":"go.mod"}`, "furniture")
	insertTuple(t, store, "fact", "myproject", "uses-cobra-cli", `{"source":"go.sum"}`, "furniture")
	insertTuple(t, store, "claim", "myproject", "task-10", `{"agent":"Sprocket","status":"in_progress"}`, "session")
	insertTuple(t, store, "claim", "myproject", "task-11", `{"agent":"Widget","status":"in_progress"}`, "session")
	insertTuple(t, store, "obstacle", "myproject", "flaky-ci", `{"task":"task-e2e","detail":"CI flaky on macOS"}`, "session")

	cfg := PipelineConfig{
		Role: "cub",
		Data: TemplateData{
			Role:      "cub",
			AgentName: "E2EBot",
			Repo:      "myproject",
			TaskID:    "task-e2e",
			Branch:    "agent/E2EBot/task-e2e",
			Worktree:  "/tmp/worktrees/myproject/E2EBot",
			EnvPrefix: "PP",
		},
		Scope:  "myproject",
		TaskID: "task-e2e",
		Store:  store,
	}

	result, err := RunPipeline(cfg)
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}

	// === Template layer ===
	assertContains(t, result, "autonomous contributor", "cub identity")
	assertContains(t, result, "E2EBot", "agent name")
	assertContains(t, result, "myproject", "repo name")
	assertContains(t, result, "task-e2e", "task ID in template")
	assertContains(t, result, "agent/E2EBot/task-e2e", "branch in template")
	assertContains(t, result, "/tmp/worktrees/myproject/E2EBot", "worktree in template")
	assertContains(t, result, "PP_AGENT_NAME", "env var in template")

	// === Addendum layer ===
	assertContains(t, result, "BBS TUPLESPACE PROTOCOL:", "addendum header")
	assertContains(t, result, "pp bbs in available myproject task-e2e", "claiming command parameterized")
	assertContains(t, result, "pp bbs out claim myproject task-e2e", "claim write parameterized")

	// === Context layer ===
	assertContains(t, result, "BBS TUPLESPACE CONTEXT", "context header")
	assertContains(t, result, "CONVENTIONS:", "conventions in context")
	assertContains(t, result, "commit-style", "convention identity")
	assertContains(t, result, "FACTS:", "facts in context")
	assertContains(t, result, "go-1.22", "fact in context")
	assertContains(t, result, "ACTIVE AGENT ACTIVITY:", "claims in context")
	assertContains(t, result, "agent=Sprocket", "claim agent in context")
	assertContains(t, result, "OBSTACLES:", "obstacles in context")
	assertContains(t, result, "flaky-ci", "obstacle in context")

	// === Task-specific escalations ===
	assertContains(t, result, "TASK-SPECIFIC ESCALATIONS:", "escalation section")
	assertContains(t, result, "flaky-ci", "task-specific obstacle")

	// === Ordering: template before addendum before context ===
	templateIdx := strings.Index(result, "autonomous contributor")
	addendumIdx := strings.Index(result, "BBS TUPLESPACE PROTOCOL:")
	contextIdx := strings.Index(result, "BBS TUPLESPACE CONTEXT")

	if templateIdx >= addendumIdx {
		t.Error("template content should appear before addendum")
	}
	if addendumIdx >= contextIdx {
		t.Error("addendum should appear before context")
	}
}

func TestIntegration_AllRolesEndToEnd(t *testing.T) {
	store := newTestStore(t)
	insertTuple(t, store, "fact", "repo", "test-fact", `{"k":"v"}`, "furniture")

	roles, err := ListRoles()
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}

	for _, role := range roles {
		t.Run(role, func(t *testing.T) {
			cfg := PipelineConfig{
				Role: role,
				Data: TemplateData{
					Role:      role,
					AgentName: "MultiBot",
					Repo:      "repo",
					TaskID:    "t-multi",
					Branch:    "agent/MultiBot/t-multi",
					Worktree:  "/tmp/wt",
					EnvPrefix: "PP",
				},
				Scope:  "repo",
				TaskID: "t-multi",
				Store:  store,
			}

			result, err := RunPipeline(cfg)
			if err != nil {
				t.Fatalf("RunPipeline(%q): %v", role, err)
			}

			// Every role should produce non-empty output with all three layers.
			if result == "" {
				t.Fatal("empty result")
			}
			assertContains(t, result, "MultiBot", "agent name")
			assertContains(t, result, "BBS TUPLESPACE PROTOCOL:", "addendum present")
			assertContains(t, result, "BBS TUPLESPACE CONTEXT", "context present")
			assertContains(t, result, "test-fact", "context fact")
		})
	}
}

// --- Helper ---

func assertContains(t *testing.T, haystack, needle, description string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("expected %q in output (%s)", needle, description)
	}
}
