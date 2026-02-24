package workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func writeWorkflowFile(t *testing.T, dir, name, content string) {
	t.Helper()
	wfDir := filepath.Join(dir, ".procyon-park", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, name+".cue"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// Test fixtures
// ---------------------------------------------------------------------------

const validWorkflowCUE = `
build_check: {
	name: "build-check"
	description: "Run build and check"
	params: {}
	steps: [
		{type: "spawn", role: "cub", task: {title: "Build the project"}},
		{type: "wait", timeout: "5m"},
		{type: "evaluate", expect: {exitCode: 0}},
		{type: "dismiss"},
	]
}
`

const validWorkflowWithParams = `
deploy: {
	name: "deploy"
	description: "Deploy to environment"
	params: {
		env: {type: "string", required: true}
		dry_run: {type: "bool", required: false, default: false}
	}
	steps: [
		{type: "spawn", role: "cub", task: {title: "Deploy to environment", taskType: "task"}},
		{type: "wait", timeout: "10m"},
		{type: "dismiss"},
	]
}
`

const invalidCUESyntax = `
this is not valid { cue syntax !!!
`

// ---------------------------------------------------------------------------
// Schema Validation Tests
// ---------------------------------------------------------------------------

func TestLoadWorkflowValid(t *testing.T) {
	repoRoot := t.TempDir()
	writeWorkflowFile(t, repoRoot, "build-check", validWorkflowCUE)

	wf, err := loadWorkflowFromFile(
		filepath.Join(repoRoot, ".procyon-park", "workflows", "build-check.cue"),
		repoRoot,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if wf.Name != "build-check" {
		t.Errorf("expected name %q, got %q", "build-check", wf.Name)
	}
	if wf.Description != "Run build and check" {
		t.Errorf("expected description %q, got %q", "Run build and check", wf.Description)
	}
	if len(wf.Steps) != 4 {
		t.Fatalf("expected 4 steps, got %d", len(wf.Steps))
	}
	if wf.Steps[0].Type != "spawn" {
		t.Errorf("expected step 0 type %q, got %q", "spawn", wf.Steps[0].Type)
	}
	if wf.Steps[1].Type != "wait" {
		t.Errorf("expected step 1 type %q, got %q", "wait", wf.Steps[1].Type)
	}
	if wf.Steps[2].Type != "evaluate" {
		t.Errorf("expected step 2 type %q, got %q", "evaluate", wf.Steps[2].Type)
	}
	if wf.Steps[3].Type != "dismiss" {
		t.Errorf("expected step 3 type %q, got %q", "dismiss", wf.Steps[3].Type)
	}
	if wf.Source != repoRoot {
		t.Errorf("expected source %q, got %q", repoRoot, wf.Source)
	}
}

func TestLoadWorkflowWithParams(t *testing.T) {
	repoRoot := t.TempDir()
	writeWorkflowFile(t, repoRoot, "deploy", validWorkflowWithParams)

	wf, err := loadWorkflowFromFile(
		filepath.Join(repoRoot, ".procyon-park", "workflows", "deploy.cue"),
		repoRoot,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if wf.Name != "deploy" {
		t.Errorf("expected name %q, got %q", "deploy", wf.Name)
	}
	if len(wf.Params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(wf.Params))
	}
	envParam, ok := wf.Params["env"]
	if !ok {
		t.Fatal("expected 'env' param")
	}
	if envParam.Type != "string" {
		t.Errorf("expected env param type %q, got %q", "string", envParam.Type)
	}
	if !envParam.Required {
		t.Error("expected env param to be required")
	}
}

func TestLoadWorkflowInvalidCUE(t *testing.T) {
	repoRoot := t.TempDir()
	writeWorkflowFile(t, repoRoot, "bad", invalidCUESyntax)

	_, err := loadWorkflowFromFile(
		filepath.Join(repoRoot, ".procyon-park", "workflows", "bad.cue"),
		repoRoot,
	)
	if err == nil {
		t.Fatal("expected error for invalid CUE syntax")
	}
}

func TestLoadWorkflowStepConfig(t *testing.T) {
	repoRoot := t.TempDir()
	writeWorkflowFile(t, repoRoot, "build-check", validWorkflowCUE)

	wf, err := loadWorkflowFromFile(
		filepath.Join(repoRoot, ".procyon-park", "workflows", "build-check.cue"),
		repoRoot,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the spawn step config contains role and task.
	spawnStep := wf.Steps[0]
	if spawnStep.Type != "spawn" {
		t.Fatalf("expected spawn step, got %s", spawnStep.Type)
	}
	if string(spawnStep.Config) == "" {
		t.Fatal("expected non-empty config for spawn step")
	}

	// Verify wait step config contains timeout.
	waitStep := wf.Steps[1]
	if waitStep.Type != "wait" {
		t.Fatalf("expected wait step, got %s", waitStep.Type)
	}
	if string(waitStep.Config) == "" {
		t.Fatal("expected non-empty config for wait step")
	}
}

// ---------------------------------------------------------------------------
// Step Timeout Tests
// ---------------------------------------------------------------------------

const workflowWithStepTimeout = `
timeout_wf: {
	name: "timeout-test"
	description: "Workflow with step-level timeouts"
	params: {}
	steps: [
		{type: "spawn", timeout: "30s", role: "cub", task: {title: "Build something"}},
		{type: "wait", timeout: "5m"},
		{type: "evaluate", timeout: "1m", expect: {exitCode: 0}},
		{type: "dismiss"},
	]
}
`

func TestLoadWorkflowStepTimeout(t *testing.T) {
	repoRoot := t.TempDir()
	writeWorkflowFile(t, repoRoot, "timeout-test", workflowWithStepTimeout)

	wf, err := loadWorkflowFromFile(
		filepath.Join(repoRoot, ".procyon-park", "workflows", "timeout-test.cue"),
		repoRoot,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if wf.Steps[0].Timeout != "30s" {
		t.Errorf("step 0 timeout = %q, want %q", wf.Steps[0].Timeout, "30s")
	}
	if wf.Steps[1].Timeout != "5m" {
		t.Errorf("step 1 timeout = %q, want %q", wf.Steps[1].Timeout, "5m")
	}
	// WaitStep config should still contain timeout for the handler.
	var waitCfg map[string]interface{}
	if err := json.Unmarshal(wf.Steps[1].Config, &waitCfg); err != nil {
		t.Fatalf("unmarshal wait config: %v", err)
	}
	if _, ok := waitCfg["timeout"]; !ok {
		t.Error("wait step config should still contain timeout for handler")
	}
	if wf.Steps[2].Timeout != "1m" {
		t.Errorf("step 2 timeout = %q, want %q", wf.Steps[2].Timeout, "1m")
	}
	if wf.Steps[3].Timeout != "" {
		t.Errorf("step 3 timeout = %q, want empty", wf.Steps[3].Timeout)
	}
}

// ---------------------------------------------------------------------------
// Two-phase Loader Tests
// ---------------------------------------------------------------------------

const workflowWithInterpolation = `
deploy_interpolated: {
	name: "deploy-interpolated"
	description: "Deploy with param interpolation"
	params: {
		env: {type: "string", required: true}
		taskTitle: {type: "string", required: true}
	}
	steps: [
		{type: "spawn", role: "cub", task: {title: _input.taskTitle}},
		{type: "wait", timeout: "10m"},
		{type: "dismiss"},
	]
}
`

const workflowWithMultipleInterpolations = `
multi_interp: {
	name: "multi-interp"
	description: "Multiple param interpolations"
	params: {
		taskTitle: {type: "string", required: true}
		repo: {type: "string", required: true}
	}
	steps: [
		{type: "spawn", role: "cub", task: {title: _input.taskTitle}, repo: _input.repo},
		{type: "wait", timeout: "5m"},
		{type: "dismiss"},
	]
}
`

func TestParseWorkflowFromBytes(t *testing.T) {
	parsed, err := parseWorkflowFromBytes([]byte(validWorkflowCUE), "test.cue", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if parsed.Name != "build-check" {
		t.Errorf("expected name %q, got %q", "build-check", parsed.Name)
	}
	if parsed.Description != "Run build and check" {
		t.Errorf("expected description %q, got %q", "Run build and check", parsed.Description)
	}
	if parsed.StepCount != 4 {
		t.Errorf("expected 4 steps, got %d", parsed.StepCount)
	}
	if len(parsed.Params) != 0 {
		t.Errorf("expected 0 params, got %d", len(parsed.Params))
	}
}

func TestParseWorkflowExtractsParams(t *testing.T) {
	parsed, err := parseWorkflowFromBytes([]byte(validWorkflowWithParams), "test.cue", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if parsed.Name != "deploy" {
		t.Errorf("expected name %q, got %q", "deploy", parsed.Name)
	}
	if len(parsed.Params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(parsed.Params))
	}
	envParam, ok := parsed.Params["env"]
	if !ok {
		t.Fatal("expected 'env' param")
	}
	if envParam.Type != "string" {
		t.Errorf("expected env param type %q, got %q", "string", envParam.Type)
	}
	if !envParam.Required {
		t.Error("expected env param to be required")
	}
}

func TestParseWorkflowWithInterpolation(t *testing.T) {
	// Parse phase should succeed even with _input references (non-concrete).
	parsed, err := parseWorkflowFromBytes([]byte(workflowWithInterpolation), "test.cue", "test")
	if err != nil {
		t.Fatalf("parse should succeed with _input references: %v", err)
	}

	if parsed.Name != "deploy-interpolated" {
		t.Errorf("expected name %q, got %q", "deploy-interpolated", parsed.Name)
	}
	if parsed.StepCount != 3 {
		t.Errorf("expected 3 steps, got %d", parsed.StepCount)
	}
	if len(parsed.Params) != 2 {
		t.Errorf("expected 2 params, got %d", len(parsed.Params))
	}
}

func TestResolveWorkflowInterpolatesParams(t *testing.T) {
	parsed, err := parseWorkflowFromBytes([]byte(workflowWithInterpolation), "test.cue", "test")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	wf, err := ResolveWorkflow(parsed, map[string]string{
		"env":       "production",
		"taskTitle": "Deploy to production",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if wf.Name != "deploy-interpolated" {
		t.Errorf("expected name %q, got %q", "deploy-interpolated", wf.Name)
	}
	if len(wf.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(wf.Steps))
	}

	// Verify the spawn step's task title was interpolated.
	var cfg SpawnConfig
	if err := json.Unmarshal(wf.Steps[0].Config, &cfg); err != nil {
		t.Fatalf("unmarshal spawn config: %v", err)
	}
	if cfg.Task.Title != "Deploy to production" {
		t.Errorf("expected task title %q, got %q", "Deploy to production", cfg.Task.Title)
	}
}

func TestResolveWorkflowMultipleInterpolations(t *testing.T) {
	parsed, err := parseWorkflowFromBytes([]byte(workflowWithMultipleInterpolations), "test.cue", "test")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	wf, err := ResolveWorkflow(parsed, map[string]string{
		"taskTitle": "Build the widget",
		"repo":      "my-repo",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	var cfg SpawnConfig
	if err := json.Unmarshal(wf.Steps[0].Config, &cfg); err != nil {
		t.Fatalf("unmarshal spawn config: %v", err)
	}
	if cfg.Task.Title != "Build the widget" {
		t.Errorf("expected task title %q, got %q", "Build the widget", cfg.Task.Title)
	}
	if cfg.Repo != "my-repo" {
		t.Errorf("expected repo %q, got %q", "my-repo", cfg.Repo)
	}
}

func TestResolveWorkflowMissingParamFails(t *testing.T) {
	parsed, err := parseWorkflowFromBytes([]byte(workflowWithInterpolation), "test.cue", "test")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Resolve without providing params that are referenced by _input.
	_, err = ResolveWorkflow(parsed, nil)
	if err == nil {
		t.Fatal("expected error when resolving with missing interpolated params")
	}
}

func TestResolveWorkflowNoParamsNeeded(t *testing.T) {
	parsed, err := parseWorkflowFromBytes([]byte(validWorkflowCUE), "test.cue", "test")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	wf, err := ResolveWorkflow(parsed, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if wf.Name != "build-check" {
		t.Errorf("expected name %q, got %q", "build-check", wf.Name)
	}
}

func TestBuildInputCUE(t *testing.T) {
	// Empty params.
	result := buildInputCUE(nil)
	if result != inputStubCUE {
		t.Errorf("expected stub CUE for nil params, got %q", result)
	}

	// With params.
	result = buildInputCUE(map[string]string{
		"name": "hello world",
	})
	if !strings.Contains(result, `"hello world"`) {
		t.Errorf("expected quoted value in CUE output, got %q", result)
	}
	if !strings.Contains(result, "name:") {
		t.Errorf("expected field name in CUE output, got %q", result)
	}
}

func TestParseAndResolveRoundTrip(t *testing.T) {
	repoRoot := t.TempDir()
	writeWorkflowFile(t, repoRoot, "deploy-interpolated", workflowWithInterpolation)

	fp := filepath.Join(repoRoot, ".procyon-park", "workflows", "deploy-interpolated.cue")

	parsed, err := parseWorkflowFromFile(fp, repoRoot)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	wf, err := ResolveWorkflow(parsed, map[string]string{
		"env":       "staging",
		"taskTitle": "Deploy to staging",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if wf.Name != "deploy-interpolated" {
		t.Errorf("name = %q, want %q", wf.Name, "deploy-interpolated")
	}
	if wf.Source != repoRoot {
		t.Errorf("source = %q, want %q", wf.Source, repoRoot)
	}
	if wf.FilePath != fp {
		t.Errorf("filePath = %q, want %q", wf.FilePath, fp)
	}

	var cfg SpawnConfig
	if err := json.Unmarshal(wf.Steps[0].Config, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.Task.Title != "Deploy to staging" {
		t.Errorf("task title = %q, want %q", cfg.Task.Title, "Deploy to staging")
	}
}

// ---------------------------------------------------------------------------
// Path Resolution Tests
// ---------------------------------------------------------------------------

func TestResolveWorkflowPathRepoOverridesGlobal(t *testing.T) {
	repoRoot := t.TempDir()
	writeWorkflowFile(t, repoRoot, "test-wf", validWorkflowCUE)

	repoPath := filepath.Join(repoRoot, ".procyon-park", "workflows", "test-wf.cue")
	path, source, err := resolveWorkflowPath("test-wf", repoRoot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != repoPath {
		t.Errorf("expected path %q, got %q", repoPath, path)
	}
	if source != repoRoot {
		t.Errorf("expected source %q, got %q", repoRoot, source)
	}
}

func TestResolveWorkflowPathNotFound(t *testing.T) {
	repoRoot := t.TempDir()
	_, _, err := resolveWorkflowPath("nonexistent", repoRoot)
	if err == nil {
		t.Fatal("expected error for missing workflow")
	}
	expected := `workflow "nonexistent" not found`
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

// ---------------------------------------------------------------------------
// List Workflows Tests
// ---------------------------------------------------------------------------

func TestListWorkflowsRepoOnly(t *testing.T) {
	repoRoot := t.TempDir()
	writeWorkflowFile(t, repoRoot, "build-check", validWorkflowCUE)
	writeWorkflowFile(t, repoRoot, "deploy", validWorkflowWithParams)

	summaries, err := ListWorkflows(repoRoot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := make(map[string]bool)
	for _, s := range summaries {
		found[s.Name] = true
	}

	if !found["build-check"] {
		t.Error("expected build-check in list")
	}
	if !found["deploy"] {
		t.Error("expected deploy in list")
	}
}

func TestListWorkflowsSkipsInvalidFiles(t *testing.T) {
	repoRoot := t.TempDir()
	writeWorkflowFile(t, repoRoot, "good", validWorkflowCUE)
	writeWorkflowFile(t, repoRoot, "bad", invalidCUESyntax)

	summaries, err := ListWorkflows(repoRoot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, s := range summaries {
		if s.Name == "bad" {
			t.Error("invalid workflow should not appear in list")
		}
	}
	found := false
	for _, s := range summaries {
		if s.Name == "build-check" {
			found = true
		}
	}
	if !found {
		t.Error("expected build-check in list")
	}
}

func TestListWorkflowsWithInterpolation(t *testing.T) {
	repoRoot := t.TempDir()
	writeWorkflowFile(t, repoRoot, "deploy-interpolated", workflowWithInterpolation)
	writeWorkflowFile(t, repoRoot, "build-check", validWorkflowCUE)

	summaries, err := ListWorkflows(repoRoot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := make(map[string]bool)
	for _, s := range summaries {
		found[s.Name] = true
	}

	if !found["deploy-interpolated"] {
		t.Error("expected deploy-interpolated in list")
	}
	if !found["build-check"] {
		t.Error("expected build-check in list")
	}
}

// ---------------------------------------------------------------------------
// CUE Module Infrastructure Tests
// ---------------------------------------------------------------------------

func TestEnsureModuleInfrastructure(t *testing.T) {
	dir := t.TempDir()

	if err := EnsureModuleInfrastructure(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify cue.mod/module.cue exists.
	moduleCUE := filepath.Join(dir, "cue.mod", "module.cue")
	data, err := os.ReadFile(moduleCUE)
	if err != nil {
		t.Fatalf("module.cue not created: %v", err)
	}
	if !strings.Contains(string(data), "procyon.dev/workflows") {
		t.Errorf("module.cue should contain module path, got: %s", string(data))
	}

	// Verify tasks/ directory and package file exist.
	tasksPkg := filepath.Join(dir, "tasks", "tasks.cue")
	if _, err := os.Stat(tasksPkg); os.IsNotExist(err) {
		t.Error("tasks/tasks.cue not created")
	}

	// Verify aspects/ directory and package file exist.
	aspectsPkg := filepath.Join(dir, "aspects", "aspects.cue")
	if _, err := os.Stat(aspectsPkg); os.IsNotExist(err) {
		t.Error("aspects/aspects.cue not created")
	}

	// Calling again should be idempotent.
	if err := EnsureModuleInfrastructure(dir); err != nil {
		t.Fatalf("idempotent call failed: %v", err)
	}
}

func TestEnsureModuleInfrastructureIdempotent(t *testing.T) {
	dir := t.TempDir()

	if err := EnsureModuleInfrastructure(dir); err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Modify module.cue to verify it's not overwritten.
	moduleCUE := filepath.Join(dir, "cue.mod", "module.cue")
	custom := []byte("module: \"custom.dev/workflows@v0\"\nlanguage: version: \"v0.12.0\"\n")
	if err := os.WriteFile(moduleCUE, custom, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := EnsureModuleInfrastructure(dir); err != nil {
		t.Fatalf("second call: %v", err)
	}

	data, _ := os.ReadFile(moduleCUE)
	if !strings.Contains(string(data), "custom.dev") {
		t.Error("module.cue was overwritten on second call")
	}
}

func TestLoadCUEValueWithModuleRoot(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureModuleInfrastructure(dir); err != nil {
		t.Fatal(err)
	}

	wfPath := filepath.Join(dir, "test-wf.cue")
	if err := os.WriteFile(wfPath, []byte(validWorkflowCUE), 0o644); err != nil {
		t.Fatal(err)
	}

	wf, err := loadWorkflowFromFile(wfPath, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wf.Name != "build-check" {
		t.Errorf("expected name %q, got %q", "build-check", wf.Name)
	}
}

func TestFindModuleRoot(t *testing.T) {
	dir := t.TempDir()

	// No cue.mod: should return empty.
	if got := findModuleRoot(dir); got != "" {
		t.Errorf("expected empty, got %q", got)
	}

	// Create cue.mod directory.
	os.MkdirAll(filepath.Join(dir, "cue.mod"), 0o755)

	if got := findModuleRoot(dir); got != dir {
		t.Errorf("expected %q, got %q", dir, got)
	}

	// Subdirectory should find parent module root.
	subDir := filepath.Join(dir, "sub", "dir")
	os.MkdirAll(subDir, 0o755)
	if got := findModuleRoot(subDir); got != dir {
		t.Errorf("expected %q from subdir, got %q", dir, got)
	}
}

// ---------------------------------------------------------------------------
// Aspect Tests
// ---------------------------------------------------------------------------

const workflowWithAspects = `
aspected: {
	name: "aspected-wf"
	description: "Workflow with aspects"
	params: {}
	steps: [
		{type: "spawn", role: "cub", task: {title: "Build something"}},
		{type: "wait", timeout: "5m"},
		{type: "evaluate", expect: {exitCode: 0}},
		{type: "dismiss"},
	]
	aspects: [
		{
			match: {type: "spawn"}
			before: [{type: "gate", gateType: "timer", duration: "1s"}]
		},
	]
}
`

func TestLoadWorkflowWithAspects(t *testing.T) {
	repoRoot := t.TempDir()
	writeWorkflowFile(t, repoRoot, "aspected-wf", workflowWithAspects)

	wf, err := loadWorkflowFromFile(
		filepath.Join(repoRoot, ".procyon-park", "workflows", "aspected-wf.cue"),
		repoRoot,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if wf.Name != "aspected-wf" {
		t.Errorf("expected name %q, got %q", "aspected-wf", wf.Name)
	}

	// Original 4 steps + 1 injected before spawn = 5 steps.
	if len(wf.Steps) != 5 {
		t.Fatalf("expected 5 steps after aspect expansion, got %d", len(wf.Steps))
	}

	if wf.Steps[0].Type != "gate" {
		t.Errorf("step 0: expected gate (injected before), got %s", wf.Steps[0].Type)
	}
	if wf.Steps[1].Type != "spawn" {
		t.Errorf("step 1: expected spawn, got %s", wf.Steps[1].Type)
	}
	if wf.Steps[2].Type != "wait" {
		t.Errorf("step 2: expected wait, got %s", wf.Steps[2].Type)
	}
	if wf.Steps[3].Type != "evaluate" {
		t.Errorf("step 3: expected evaluate, got %s", wf.Steps[3].Type)
	}
	if wf.Steps[4].Type != "dismiss" {
		t.Errorf("step 4: expected dismiss, got %s", wf.Steps[4].Type)
	}

	if len(wf.Aspects) != 1 {
		t.Fatalf("expected 1 aspect, got %d", len(wf.Aspects))
	}
	if wf.Aspects[0].Match.Type != "spawn" {
		t.Errorf("aspect match type = %q, want %q", wf.Aspects[0].Match.Type, "spawn")
	}
}

const workflowWithAfterAspect = `
after_aspected: {
	name: "after-aspected-wf"
	description: "Workflow with after aspect"
	params: {}
	steps: [
		{type: "spawn", role: "cub", task: {title: "Build something"}},
		{type: "wait", timeout: "5m"},
		{type: "dismiss"},
	]
	aspects: [
		{
			match: {type: "wait"}
			after: [{type: "gate", gateType: "timer", duration: "2s"}]
		},
	]
}
`

func TestLoadWorkflowWithAfterAspect(t *testing.T) {
	repoRoot := t.TempDir()
	writeWorkflowFile(t, repoRoot, "after-aspected-wf", workflowWithAfterAspect)

	wf, err := loadWorkflowFromFile(
		filepath.Join(repoRoot, ".procyon-park", "workflows", "after-aspected-wf.cue"),
		repoRoot,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(wf.Steps) != 4 {
		t.Fatalf("expected 4 steps after aspect expansion, got %d", len(wf.Steps))
	}

	if wf.Steps[0].Type != "spawn" {
		t.Errorf("step 0: expected spawn, got %s", wf.Steps[0].Type)
	}
	if wf.Steps[1].Type != "wait" {
		t.Errorf("step 1: expected wait, got %s", wf.Steps[1].Type)
	}
	if wf.Steps[2].Type != "gate" {
		t.Errorf("step 2: expected gate (injected after), got %s", wf.Steps[2].Type)
	}
	if wf.Steps[3].Type != "dismiss" {
		t.Errorf("step 3: expected dismiss, got %s", wf.Steps[3].Type)
	}
}

const workflowWithRoleAspect = `
role_aspected: {
	name: "role-aspected-wf"
	description: "Workflow with role-matching aspect"
	params: {}
	steps: [
		{type: "spawn", role: "cub", task: {title: "Build"}},
		{type: "wait", timeout: "5m"},
		{type: "spawn", role: "reviewer", task: {title: "Review"}},
		{type: "wait", timeout: "5m"},
		{type: "dismiss"},
	]
	aspects: [
		{
			match: {role: "reviewer"}
			before: [{type: "gate", gateType: "timer", duration: "3s"}]
		},
	]
}
`

func TestLoadWorkflowAspectMatchByRole(t *testing.T) {
	repoRoot := t.TempDir()
	writeWorkflowFile(t, repoRoot, "role-aspected-wf", workflowWithRoleAspect)

	wf, err := loadWorkflowFromFile(
		filepath.Join(repoRoot, ".procyon-park", "workflows", "role-aspected-wf.cue"),
		repoRoot,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Original 5 steps + 1 injected before second spawn (role=reviewer) = 6 steps.
	if len(wf.Steps) != 6 {
		t.Fatalf("expected 6 steps after aspect expansion, got %d", len(wf.Steps))
	}

	if wf.Steps[0].Type != "spawn" {
		t.Errorf("step 0: expected spawn, got %s", wf.Steps[0].Type)
	}
	if wf.Steps[1].Type != "wait" {
		t.Errorf("step 1: expected wait, got %s", wf.Steps[1].Type)
	}
	if wf.Steps[2].Type != "gate" {
		t.Errorf("step 2: expected gate (injected before reviewer spawn), got %s", wf.Steps[2].Type)
	}
	if wf.Steps[3].Type != "spawn" {
		t.Errorf("step 3: expected spawn (reviewer), got %s", wf.Steps[3].Type)
	}
	if wf.Steps[4].Type != "wait" {
		t.Errorf("step 4: expected wait, got %s", wf.Steps[4].Type)
	}
	if wf.Steps[5].Type != "dismiss" {
		t.Errorf("step 5: expected dismiss, got %s", wf.Steps[5].Type)
	}
}

func TestExpandAspectsNoAspects(t *testing.T) {
	steps := []Step{
		{Type: "spawn", Config: json.RawMessage(`{"role":"cub","task":{"title":"test"}}`)},
		{Type: "wait", Config: json.RawMessage(`{"timeout":"5m"}`)},
	}

	expanded := expandAspects(steps, nil)
	if len(expanded) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(expanded))
	}
}

func TestExpandAspectsMultipleAspects(t *testing.T) {
	steps := []Step{
		{Type: "spawn", Config: json.RawMessage(`{"role":"cub","task":{"title":"test"}}`)},
		{Type: "wait", Config: json.RawMessage(`{"timeout":"5m"}`)},
	}
	aspects := []Aspect{
		{
			Match:  AspectMatch{Type: "spawn"},
			Before: []Step{{Type: "gate", Config: json.RawMessage(`{"gateType":"timer","duration":"1s"}`)}},
		},
		{
			Match: AspectMatch{Type: "wait"},
			After: []Step{{Type: "gate", Config: json.RawMessage(`{"gateType":"timer","duration":"2s"}`)}},
		},
	}

	expanded := expandAspects(steps, aspects)
	if len(expanded) != 4 {
		t.Fatalf("expected 4 steps, got %d", len(expanded))
	}
	if expanded[0].Type != "gate" {
		t.Errorf("step 0: expected gate, got %s", expanded[0].Type)
	}
	if expanded[1].Type != "spawn" {
		t.Errorf("step 1: expected spawn, got %s", expanded[1].Type)
	}
	if expanded[2].Type != "wait" {
		t.Errorf("step 2: expected wait, got %s", expanded[2].Type)
	}
	if expanded[3].Type != "gate" {
		t.Errorf("step 3: expected gate, got %s", expanded[3].Type)
	}
}

func TestExpandAspectsInjectedStepsNotRematched(t *testing.T) {
	steps := []Step{
		{Type: "spawn", Config: json.RawMessage(`{"role":"cub","task":{"title":"test"}}`)},
	}
	aspects := []Aspect{
		{
			Match:  AspectMatch{Type: "spawn"},
			Before: []Step{{Type: "gate", Config: json.RawMessage(`{"gateType":"timer","duration":"1s"}`)}},
		},
		{
			Match:  AspectMatch{Type: "gate"},
			Before: []Step{{Type: "wait", Config: json.RawMessage(`{"timeout":"1s"}`)}},
		},
	}

	expanded := expandAspects(steps, aspects)
	// First aspect: gate + spawn = 2 steps
	// Second aspect: gate from aspect 1 IS in step list for aspect 2's iteration,
	// so it gets matched: wait + gate + spawn = 3 steps
	if len(expanded) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(expanded))
	}
	if expanded[0].Type != "wait" {
		t.Errorf("step 0: expected wait (injected by second aspect), got %s", expanded[0].Type)
	}
	if expanded[1].Type != "gate" {
		t.Errorf("step 1: expected gate (injected by first aspect), got %s", expanded[1].Type)
	}
	if expanded[2].Type != "spawn" {
		t.Errorf("step 2: expected spawn (original), got %s", expanded[2].Type)
	}
}

func TestAspectInjectedStepsDontInheritTimeout(t *testing.T) {
	steps := []Step{
		{Type: "spawn", Timeout: "30s", Config: json.RawMessage(`{"role":"cub","task":{"title":"test"}}`)},
	}
	aspects := []Aspect{
		{
			Match:  AspectMatch{Type: "spawn"},
			Before: []Step{{Type: "gate", Config: json.RawMessage(`{"gateType":"timer","duration":"1s"}`)}},
		},
	}

	expanded := expandAspects(steps, aspects)
	if len(expanded) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(expanded))
	}
	if expanded[0].Timeout != "" {
		t.Errorf("injected step should not inherit timeout, got %q", expanded[0].Timeout)
	}
	if expanded[1].Timeout != "30s" {
		t.Errorf("original step should keep timeout, got %q", expanded[1].Timeout)
	}
}

const workflowWithBeforeAndAfterAspect = `
both_aspected: {
	name: "both-aspected-wf"
	description: "Aspect with both before and after"
	params: {}
	steps: [
		{type: "spawn", role: "cub", task: {title: "Build"}},
		{type: "wait", timeout: "5m"},
		{type: "dismiss"},
	]
	aspects: [
		{
			match: {type: "spawn"}
			before: [{type: "gate", gateType: "timer", duration: "1s"}]
			after:  [{type: "gate", gateType: "timer", duration: "2s"}]
		},
	]
}
`

func TestLoadWorkflowAspectWithBeforeAndAfter(t *testing.T) {
	repoRoot := t.TempDir()
	writeWorkflowFile(t, repoRoot, "both-aspected-wf", workflowWithBeforeAndAfterAspect)

	wf, err := loadWorkflowFromFile(
		filepath.Join(repoRoot, ".procyon-park", "workflows", "both-aspected-wf.cue"),
		repoRoot,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// gate(before) + spawn + gate(after) + wait + dismiss = 5 steps.
	if len(wf.Steps) != 5 {
		t.Fatalf("expected 5 steps, got %d", len(wf.Steps))
	}
	if wf.Steps[0].Type != "gate" {
		t.Errorf("step 0: expected gate (before), got %s", wf.Steps[0].Type)
	}
	if wf.Steps[1].Type != "spawn" {
		t.Errorf("step 1: expected spawn, got %s", wf.Steps[1].Type)
	}
	if wf.Steps[2].Type != "gate" {
		t.Errorf("step 2: expected gate (after), got %s", wf.Steps[2].Type)
	}
	if wf.Steps[3].Type != "wait" {
		t.Errorf("step 3: expected wait, got %s", wf.Steps[3].Type)
	}
	if wf.Steps[4].Type != "dismiss" {
		t.Errorf("step 4: expected dismiss, got %s", wf.Steps[4].Type)
	}
}

// ---------------------------------------------------------------------------
// Context Resolution Tests
// ---------------------------------------------------------------------------

func TestBuildContextCUE(t *testing.T) {
	// Nil context.
	result := buildContextCUE(nil)
	if !strings.Contains(result, "_ctx: {}") {
		t.Errorf("expected empty context for nil, got %q", result)
	}

	// Empty context.
	result = buildContextCUE(&WorkflowContext{})
	if !strings.Contains(result, "_ctx:") {
		t.Errorf("expected _ctx in output, got %q", result)
	}

	// Full context.
	ctx := &WorkflowContext{
		TaskID:       ".kss-123",
		ActiveAgent:  "Widget",
		ActiveBranch: "agent/Widget/kss-123",
		ActiveRepo:   "my-repo",
	}
	result = buildContextCUE(ctx)
	if !strings.Contains(result, `taskId: ".kss-123"`) {
		t.Errorf("expected taskId in output, got %q", result)
	}
	if !strings.Contains(result, `activeAgent: "Widget"`) {
		t.Errorf("expected activeAgent in output, got %q", result)
	}
	if !strings.Contains(result, `activeBranch: "agent/Widget/kss-123"`) {
		t.Errorf("expected activeBranch in output, got %q", result)
	}
	if !strings.Contains(result, `activeRepo: "my-repo"`) {
		t.Errorf("expected activeRepo in output, got %q", result)
	}

	// Context with PreviousOutput.
	ctx.PreviousOutput = json.RawMessage(`{"exitCode": 0}`)
	result = buildContextCUE(ctx)
	if !strings.Contains(result, `previousOutput: {"exitCode": 0}`) {
		t.Errorf("expected previousOutput in output, got %q", result)
	}
}

func TestResolveStepConfig_NoCtxReference(t *testing.T) {
	instance := &Instance{
		ID:       "wf-test",
		RepoName: "my-repo",
		Context: WorkflowContext{
			ActiveAgent: "Widget",
		},
	}
	config := json.RawMessage(`{"role": "cub", "task": {"title": "Build"}}`)

	resolved, err := ResolveStepConfig(instance, config, "spawn")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(resolved) != string(config) {
		t.Errorf("expected unchanged config, got %q", string(resolved))
	}
}

func TestResolveStepConfig_WithCtxReference(t *testing.T) {
	instance := &Instance{
		ID:       "wf-test",
		RepoName: "my-repo",
		Context: WorkflowContext{
			ActiveAgent:  "Widget",
			ActiveBranch: "agent/Widget/kss-123",
			TaskID:       ".kss-123",
		},
	}
	config := json.RawMessage(`{"task": {"title": "Review branch: " + _ctx.activeBranch}}`)

	resolved, err := ResolveStepConfig(instance, config, "spawn")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed struct {
		Task struct {
			Title string `json:"title"`
		} `json:"task"`
	}
	if err := json.Unmarshal(resolved, &parsed); err != nil {
		t.Fatalf("unmarshal resolved config: %v", err)
	}
	expected := "Review branch: agent/Widget/kss-123"
	if parsed.Task.Title != expected {
		t.Errorf("expected title %q, got %q", expected, parsed.Task.Title)
	}
}

func TestResolveStepConfig_MissingCtxField(t *testing.T) {
	instance := &Instance{
		ID:       "wf-test",
		RepoName: "my-repo",
		Context:  WorkflowContext{},
	}
	config := json.RawMessage(`{"task": {"title": "Agent: " + _ctx.activeAgent}}`)

	_, err := ResolveStepConfig(instance, config, "spawn")
	if err == nil {
		t.Fatal("expected error for missing ctx field, got nil")
	}
	if !strings.Contains(err.Error(), "not concrete") && !strings.Contains(err.Error(), "incomplete") {
		t.Errorf("expected concrete/incomplete error, got: %v", err)
	}
}
