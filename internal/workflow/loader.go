package workflow

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
)

// spawnConfigForMatch is used locally by aspect matching to inspect spawn step roles.
type spawnConfigForMatch struct {
	Role string `json:"role"`
}

//go:embed schema.cue
var schemaCUE string

// inputStubCUE is prepended at parse time so _input.paramName references compile
// without concrete values. The open constraint [string]: _ allows any param names.
const inputStubCUE = "_input: [string]: _\n"

// ctxStubCUE is prepended at parse time so _ctx.fieldName references compile
// without concrete values. This allows workflows to reference context fields
// (activeAgent, activeBranch, etc.) that are populated at runtime by step execution.
const ctxStubCUE = "_ctx: [string]: _\n"

// parseTimeStub combines input and context stubs for parse-time compilation.
const parseTimeStub = inputStubCUE + ctxStubCUE

// DefaultWorkflowsDir returns the default global workflows directory.
func DefaultWorkflowsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".procyon-park", "workflows")
}

// ParsedWorkflow holds a partially-evaluated workflow ready for parameter resolution.
// The CUE value may contain non-concrete _input.paramName references.
type ParsedWorkflow struct {
	rawData     []byte           // Raw CUE file contents (without _input stub)
	Params      map[string]Param // Extracted param declarations
	Name        string           // Workflow name
	Description string           // Workflow description
	StepCount   int              // Number of steps (for listing)
	Source      string           // "global" or repo path
	FilePath    string
}

// ParseWorkflow parses a workflow definition by name without resolving parameters.
// Repo-specific workflows take precedence over global.
func ParseWorkflow(name string, repoRoot string) (*ParsedWorkflow, error) {
	filePath, source, err := resolveWorkflowPath(name, repoRoot)
	if err != nil {
		return nil, err
	}

	return parseWorkflowFromFile(filePath, source)
}

// parseWorkflowFromFile compiles and validates a workflow's structure, extracting
// param declarations without requiring all values to be concrete.
func parseWorkflowFromFile(filePath, source string) (*ParsedWorkflow, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("workflow: read file: %w", err)
	}

	return parseWorkflowFromBytes(data, filePath, source)
}

// parseWorkflowFromBytes parses workflow CUE from raw bytes.
func parseWorkflowFromBytes(data []byte, filePath, source string) (*ParsedWorkflow, error) {
	wfVal, err := loadCUEValue(data, filePath, parseTimeStub)
	if err != nil {
		return nil, fmt.Errorf("workflow: parse: %w", err)
	}

	ctx := cuecontext.New()

	// Compile schema.
	schemaVal := ctx.CompileString(schemaCUE)
	if schemaVal.Err() != nil {
		return nil, fmt.Errorf("workflow: compile schema: %w", schemaVal.Err())
	}

	// Find the workflow value (relaxed: don't require full concreteness).
	wfField, err := findWorkflowValue(schemaVal, wfVal, false)
	if err != nil {
		return nil, fmt.Errorf("workflow: %s: %w", filepath.Base(filePath), err)
	}

	// Extract name (must be concrete even at parse time).
	nameVal := wfField.LookupPath(cue.ParsePath("name"))
	name, err := nameVal.String()
	if err != nil {
		return nil, fmt.Errorf("workflow: extract name: %w", err)
	}

	// Extract description (may have default "").
	var desc string
	descVal := wfField.LookupPath(cue.ParsePath("description"))
	if descVal.Err() == nil {
		desc, _ = descVal.String()
	}

	// Extract params (param declarations are always concrete).
	params, err := extractParams(wfField)
	if err != nil {
		return nil, fmt.Errorf("workflow: extract params: %w", err)
	}

	// Count steps.
	stepCount := 0
	stepsVal := wfField.LookupPath(cue.ParsePath("steps"))
	if stepsVal.Err() == nil {
		iter, _ := stepsVal.List()
		for iter.Next() {
			stepCount++
		}
	}

	return &ParsedWorkflow{
		rawData:     data,
		Params:      params,
		Name:        name,
		Description: desc,
		StepCount:   stepCount,
		Source:      source,
		FilePath:    filePath,
	}, nil
}

// ResolveWorkflow takes a parsed workflow and actual parameter values, resolves all
// CUE _input.paramName references, and returns a fully concrete Workflow.
func ResolveWorkflow(parsed *ParsedWorkflow, params map[string]string) (*Workflow, error) {
	inputCUE := buildInputCUE(params)

	wfVal, err := loadCUEValue(parsed.rawData, parsed.FilePath, inputCUE)
	if err != nil {
		return nil, fmt.Errorf("workflow: resolve: %w", err)
	}

	ctx := cuecontext.New()

	// Compile schema.
	schemaVal := ctx.CompileString(schemaCUE)
	if schemaVal.Err() != nil {
		return nil, fmt.Errorf("workflow: compile schema: %w", schemaVal.Err())
	}

	// Find the workflow value (strict: require full concreteness).
	wfField, err := findWorkflowValue(schemaVal, wfVal, true)
	if err != nil {
		return nil, fmt.Errorf("workflow: %s: %w", filepath.Base(parsed.FilePath), err)
	}

	// Validate against schema by unifying with #Workflow.
	schemaType := schemaVal.LookupPath(cue.ParsePath("#Workflow"))
	unified := schemaType.Unify(wfField)
	if err := unified.Validate(cue.Concrete(true)); err != nil {
		return nil, fmt.Errorf("workflow: validate: %w", err)
	}

	// Convert to Go struct.
	wf, err := cueToWorkflow(unified)
	if err != nil {
		return nil, fmt.Errorf("workflow: convert: %w", err)
	}

	wf.Source = parsed.Source
	wf.FilePath = parsed.FilePath

	return wf, nil
}

// loadCUEValue loads a CUE file using the cue/load package for module-aware loading.
// The inputCUE content is prepended to the workflow file data in the overlay since
// CUE hidden fields (starting with _) are scoped to the file they're declared in.
func loadCUEValue(data []byte, filePath string, inputCUE string) (cue.Value, error) {
	absPath := filePath
	if !filepath.IsAbs(absPath) {
		var err error
		absPath, err = filepath.Abs(absPath)
		if err != nil {
			return cue.Value{}, fmt.Errorf("workflow: resolve path: %w", err)
		}
	}

	dir := filepath.Dir(absPath)
	fileName := filepath.Base(absPath)

	// Prepend _input stub/values to the workflow file content.
	// Hidden fields (_input) are file-scoped in CUE, so they must be in the same file.
	combined := inputCUE + string(data)

	overlay := map[string]load.Source{
		absPath: load.FromString(combined),
	}

	// Find the module root by walking up from dir looking for cue.mod/.
	moduleRoot := findModuleRoot(dir)

	cfg := &load.Config{
		Dir:                 dir,
		Overlay:             overlay,
		Package:             "_", // load files without package declarations
		AcceptLegacyModules: true,
	}
	if moduleRoot != "" {
		cfg.ModuleRoot = moduleRoot
	}

	instances := load.Instances([]string{fileName}, cfg)
	if len(instances) == 0 {
		return cue.Value{}, fmt.Errorf("workflow: no instances for %s", fileName)
	}

	inst := instances[0]
	if inst.Err != nil {
		return cue.Value{}, fmt.Errorf("workflow: load instance: %w", inst.Err)
	}

	ctx := cuecontext.New()
	val := ctx.BuildInstance(inst)
	if val.Err() != nil {
		return cue.Value{}, fmt.Errorf("workflow: build instance: %w", val.Err())
	}

	return val, nil
}

// findModuleRoot walks up from dir looking for a cue.mod/ directory.
func findModuleRoot(dir string) string {
	abs := dir
	for {
		modDir := filepath.Join(abs, "cue.mod")
		if info, err := os.Stat(modDir); err == nil && info.IsDir() {
			return abs
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "" // reached filesystem root
		}
		abs = parent
	}
}

// buildInputCUE generates CUE source that provides _input param values.
func buildInputCUE(params map[string]string) string {
	if len(params) == 0 {
		return inputStubCUE
	}
	var sb strings.Builder
	sb.WriteString("_input: {\n")
	for k, v := range params {
		jsonVal, _ := json.Marshal(v)
		sb.WriteString(fmt.Sprintf("\t%s: %s\n", k, string(jsonVal)))
	}
	sb.WriteString("}\n")
	return sb.String()
}

// buildContextCUE generates CUE source that provides _ctx values from WorkflowContext.
func buildContextCUE(ctx *WorkflowContext) string {
	if ctx == nil {
		return "_ctx: {}\n"
	}
	var sb strings.Builder
	sb.WriteString("_ctx: {\n")
	if ctx.TaskID != "" {
		jsonVal, _ := json.Marshal(ctx.TaskID)
		sb.WriteString(fmt.Sprintf("\ttaskId: %s\n", string(jsonVal)))
	}
	if ctx.ActiveAgent != "" {
		jsonVal, _ := json.Marshal(ctx.ActiveAgent)
		sb.WriteString(fmt.Sprintf("\tactiveAgent: %s\n", string(jsonVal)))
	}
	if ctx.ActiveBranch != "" {
		jsonVal, _ := json.Marshal(ctx.ActiveBranch)
		sb.WriteString(fmt.Sprintf("\tactiveBranch: %s\n", string(jsonVal)))
	}
	if ctx.ActiveRepo != "" {
		jsonVal, _ := json.Marshal(ctx.ActiveRepo)
		sb.WriteString(fmt.Sprintf("\tactiveRepo: %s\n", string(jsonVal)))
	}
	if len(ctx.PreviousOutput) > 0 {
		sb.WriteString(fmt.Sprintf("\tpreviousOutput: %s\n", string(ctx.PreviousOutput)))
	}
	sb.WriteString("}\n")
	return sb.String()
}

// ResolveStepConfig resolves _ctx references in a step config at execution time.
// This allows step configurations to reference workflow context values like
// _ctx.activeAgent. The function re-parses the step config with the current
// context values injected.
func ResolveStepConfig(instance *Instance, stepConfig json.RawMessage, stepType string) (json.RawMessage, error) {
	// Quick check: if no _ctx reference, return as-is.
	configStr := string(stepConfig)
	if !strings.Contains(configStr, "_ctx") {
		return stepConfig, nil
	}

	// Build a minimal CUE document with the step config and context.
	ctxCUE := buildContextCUE(&instance.Context)
	inputCUE := buildInputCUE(instance.Params)

	// Wrap the step config in a CUE document for resolution.
	// Use a regular field name (not hidden) so we can extract it via LookupPath.
	stepCUE := fmt.Sprintf(`
%s
%s
resolvedStep: %s
`, ctxCUE, inputCUE, configStr)

	ctx := cuecontext.New()
	val := ctx.CompileString(stepCUE)
	if val.Err() != nil {
		return nil, fmt.Errorf("workflow: compile step config: %w", val.Err())
	}

	// Extract the resolved step config.
	stepVal := val.LookupPath(cue.ParsePath("resolvedStep"))
	if stepVal.Err() != nil {
		return nil, fmt.Errorf("workflow: extract resolved config: %w", stepVal.Err())
	}

	// Validate that the result is concrete.
	if err := stepVal.Validate(cue.Concrete(true)); err != nil {
		return nil, fmt.Errorf("workflow: resolved config not concrete: %w", err)
	}

	// Convert back to JSON.
	resolvedJSON, err := stepVal.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("workflow: marshal resolved config: %w", err)
	}

	return resolvedJSON, nil
}

// extractParams extracts param declarations from a CUE workflow value.
func extractParams(wfVal cue.Value) (map[string]Param, error) {
	params := make(map[string]Param)
	paramsVal := wfVal.LookupPath(cue.ParsePath("params"))
	if paramsVal.Err() != nil {
		return params, nil
	}

	paramsJSON, err := paramsVal.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("workflow: marshal params: %w", err)
	}

	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		return nil, fmt.Errorf("workflow: unmarshal params: %w", err)
	}

	return params, nil
}

// loadWorkflowFromFile loads and validates a workflow from a specific file path.
func loadWorkflowFromFile(filePath, source string) (*Workflow, error) {
	parsed, err := parseWorkflowFromFile(filePath, source)
	if err != nil {
		return nil, err
	}
	return ResolveWorkflow(parsed, nil)
}

// findWorkflowValue searches the CUE value for a workflow definition.
// When concrete is true, requires all values to be concrete (resolve phase).
// When false, allows non-concrete values like _input references (parse phase).
func findWorkflowValue(schema, val cue.Value, concrete bool) (cue.Value, error) {
	schemaType := schema.LookupPath(cue.ParsePath("#Workflow"))

	check := func(v cue.Value) bool {
		unified := schemaType.Unify(v)
		if concrete {
			return unified.Validate(cue.Concrete(true)) == nil
		}
		// Relaxed check: verify name is a concrete string and steps exist.
		nameVal := v.LookupPath(cue.ParsePath("name"))
		if _, err := nameVal.String(); err != nil {
			return false
		}
		stepsVal := v.LookupPath(cue.ParsePath("steps"))
		return stepsVal.Err() == nil
	}

	// First, try the value directly (if the file IS the workflow).
	if check(val) {
		return val, nil
	}

	// Otherwise, iterate top-level fields to find one matching #Workflow.
	iter, err := val.Fields()
	if err != nil {
		return cue.Value{}, fmt.Errorf("no workflow definition found")
	}
	for iter.Next() {
		field := iter.Value()
		if check(field) {
			return field, nil
		}
	}

	return cue.Value{}, fmt.Errorf("no workflow definition found matching #Workflow schema")
}

// cueToWorkflow converts a validated CUE value to a Go Workflow struct.
func cueToWorkflow(val cue.Value) (*Workflow, error) {
	jsonBytes, err := val.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("workflow: marshal to JSON: %w", err)
	}

	// Parse into a raw map to handle step configs properly.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(jsonBytes, &raw); err != nil {
		return nil, fmt.Errorf("workflow: unmarshal raw: %w", err)
	}

	wf := &Workflow{}

	if v, ok := raw["name"]; ok {
		if err := json.Unmarshal(v, &wf.Name); err != nil {
			return nil, fmt.Errorf("workflow: unmarshal name: %w", err)
		}
	}

	if v, ok := raw["description"]; ok {
		if err := json.Unmarshal(v, &wf.Description); err != nil {
			return nil, fmt.Errorf("workflow: unmarshal description: %w", err)
		}
	}

	if v, ok := raw["params"]; ok {
		if err := json.Unmarshal(v, &wf.Params); err != nil {
			return nil, fmt.Errorf("workflow: unmarshal params: %w", err)
		}
	}
	if wf.Params == nil {
		wf.Params = make(map[string]Param)
	}

	// Steps: each step has a "type" field and other fields that become Config.
	if v, ok := raw["steps"]; ok {
		var rawSteps []json.RawMessage
		if err := json.Unmarshal(v, &rawSteps); err != nil {
			return nil, fmt.Errorf("workflow: unmarshal steps: %w", err)
		}
		for _, rs := range rawSteps {
			step, err := parseStepJSON(rs)
			if err != nil {
				return nil, err
			}
			wf.Steps = append(wf.Steps, step)
		}
	}

	// Extract aspects if present.
	if v, ok := raw["aspects"]; ok {
		var rawAspects []json.RawMessage
		if err := json.Unmarshal(v, &rawAspects); err != nil {
			return nil, fmt.Errorf("workflow: unmarshal aspects: %w", err)
		}
		for _, ra := range rawAspects {
			aspect, err := parseAspectJSON(ra)
			if err != nil {
				return nil, fmt.Errorf("workflow: parse aspect: %w", err)
			}
			wf.Aspects = append(wf.Aspects, aspect)
		}
	}

	// Apply aspect expansion: transform the step list by splicing before/after steps.
	wf.Steps = expandAspects(wf.Steps, wf.Aspects)

	return wf, nil
}

// parseAspectJSON parses a single aspect from JSON, handling step configs the same
// way as the main step parser.
func parseAspectJSON(data json.RawMessage) (Aspect, error) {
	var raw struct {
		Match  AspectMatch       `json:"match"`
		Before []json.RawMessage `json:"before"`
		After  []json.RawMessage `json:"after"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Aspect{}, fmt.Errorf("workflow: unmarshal aspect: %w", err)
	}

	aspect := Aspect{Match: raw.Match}

	for _, rs := range raw.Before {
		step, err := parseStepJSON(rs)
		if err != nil {
			return Aspect{}, fmt.Errorf("workflow: parse before step: %w", err)
		}
		aspect.Before = append(aspect.Before, step)
	}
	for _, rs := range raw.After {
		step, err := parseStepJSON(rs)
		if err != nil {
			return Aspect{}, fmt.Errorf("workflow: parse after step: %w", err)
		}
		aspect.After = append(aspect.After, step)
	}

	return aspect, nil
}

// parseStepJSON parses a single step from JSON, extracting the type and building config.
func parseStepJSON(data json.RawMessage) (Step, error) {
	var stepMap map[string]json.RawMessage
	if err := json.Unmarshal(data, &stepMap); err != nil {
		return Step{}, fmt.Errorf("workflow: unmarshal step: %w", err)
	}
	var stepType string
	if t, ok := stepMap["type"]; ok {
		if err := json.Unmarshal(t, &stepType); err != nil {
			return Step{}, fmt.Errorf("workflow: unmarshal step type: %w", err)
		}
	}
	var stepTimeout string
	if t, ok := stepMap["timeout"]; ok {
		if err := json.Unmarshal(t, &stepTimeout); err != nil {
			return Step{}, fmt.Errorf("workflow: unmarshal step timeout: %w", err)
		}
	}
	// Remove "type" from the map; the rest is config.
	// Note: "timeout" stays in the config map so step handlers
	// (e.g. WaitStep) can still access it in their own config.
	delete(stepMap, "type")
	configBytes, err := json.Marshal(stepMap)
	if err != nil {
		return Step{}, fmt.Errorf("workflow: marshal step config: %w", err)
	}
	return Step{Type: stepType, Timeout: stepTimeout, Config: configBytes}, nil
}

// expandAspects applies aspects to the step list as a single-pass transformation.
// Each aspect's match criteria are tested against the original step list. Injected
// steps are never re-matched. Multiple aspects are applied in declaration order
// (first aspect is innermost). Aspect-injected steps don't inherit timeouts.
func expandAspects(steps []Step, aspects []Aspect) []Step {
	if len(aspects) == 0 {
		return steps
	}

	for _, aspect := range aspects {
		var expanded []Step
		for _, step := range steps {
			if stepMatchesAspect(step, aspect.Match) {
				expanded = append(expanded, aspect.Before...)
				expanded = append(expanded, step)
				expanded = append(expanded, aspect.After...)
			} else {
				expanded = append(expanded, step)
			}
		}
		steps = expanded
	}
	return steps
}

// stepMatchesAspect checks whether a step matches the aspect's match criteria.
// All non-empty match fields must match (AND semantics).
func stepMatchesAspect(step Step, match AspectMatch) bool {
	if match.Type != "" && step.Type != match.Type {
		return false
	}

	if match.Role != "" {
		if step.Type != "spawn" {
			return false
		}
		var cfg spawnConfigForMatch
		if err := json.Unmarshal(step.Config, &cfg); err != nil {
			return false
		}
		if cfg.Role != match.Role {
			return false
		}
	}

	if match.Name != "" {
		stepName := extractStepName(step)
		matched, err := filepath.Match(match.Name, stepName)
		if err != nil || !matched {
			return false
		}
	}

	return true
}

// extractStepName returns a name for matching purposes.
// For spawn steps, returns the task title. For others, returns the step type.
func extractStepName(step Step) string {
	if step.Type == "spawn" {
		var cfg struct {
			Task struct {
				Title string `json:"title"`
			} `json:"task"`
		}
		if err := json.Unmarshal(step.Config, &cfg); err == nil && cfg.Task.Title != "" {
			return cfg.Task.Title
		}
	}
	return step.Type
}

// resolveWorkflowPath finds the .cue file for a named workflow.
// It returns the file path, the source label, and any error.
func resolveWorkflowPath(name string, repoRoot string) (string, string, error) {
	// Check repo-specific first.
	if repoRoot != "" {
		repoPath := filepath.Join(repoRoot, ".procyon-park", "workflows", name+".cue")
		if _, err := os.Stat(repoPath); err == nil {
			return repoPath, repoRoot, nil
		}
	}

	// Fall back to global.
	globalPath := filepath.Join(DefaultWorkflowsDir(), name+".cue")
	if _, err := os.Stat(globalPath); err == nil {
		return globalPath, "global", nil
	}

	return "", "", fmt.Errorf("workflow %q not found", name)
}

// ListWorkflows lists available workflows from global and repo directories.
// Uses parse phase only (no param resolution needed for listing).
func ListWorkflows(repoRoot string) ([]WorkflowSummary, error) {
	seen := make(map[string]bool)
	var summaries []WorkflowSummary

	// Repo workflows first (higher precedence).
	if repoRoot != "" {
		repoDir := filepath.Join(repoRoot, ".procyon-park", "workflows")
		entries, err := os.ReadDir(repoDir)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".cue") {
					continue
				}
				name := strings.TrimSuffix(e.Name(), ".cue")
				fp := filepath.Join(repoDir, e.Name())
				parsed, err := parseWorkflowFromFile(fp, repoRoot)
				if err != nil {
					continue // skip invalid files
				}
				seen[name] = true
				summaries = append(summaries, WorkflowSummary{
					Name:        parsed.Name,
					Description: parsed.Description,
					Source:      repoRoot,
					StepCount:   parsed.StepCount,
				})
			}
		}
	}

	// Global workflows.
	globalDir := DefaultWorkflowsDir()
	entries, err := os.ReadDir(globalDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".cue") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".cue")
			if seen[name] {
				continue // repo override
			}
			fp := filepath.Join(globalDir, e.Name())
			parsed, err := parseWorkflowFromFile(fp, "global")
			if err != nil {
				continue
			}
			summaries = append(summaries, WorkflowSummary{
				Name:        parsed.Name,
				Description: parsed.Description,
				Source:      "global",
				StepCount:   parsed.StepCount,
			})
		}
	}

	return summaries, nil
}

// EnsureModuleInfrastructure creates the CUE module infrastructure in a workflows
// directory if it doesn't already exist. This includes cue.mod/module.cue, and
// the tasks/ and aspects/ package subdirectories.
func EnsureModuleInfrastructure(workflowsDir string) error {
	// Create cue.mod directory and module.cue.
	modDir := filepath.Join(workflowsDir, "cue.mod")
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		return fmt.Errorf("workflow: create cue.mod: %w", err)
	}

	moduleCUE := filepath.Join(modDir, "module.cue")
	if _, err := os.Stat(moduleCUE); os.IsNotExist(err) {
		content := `module: "procyon.dev/workflows@v0"
language: version: "v0.12.0"
`
		if err := os.WriteFile(moduleCUE, []byte(content), 0o644); err != nil {
			return fmt.Errorf("workflow: write module.cue: %w", err)
		}
	}

	// Create tasks/ package directory with shared definitions.
	tasksDir := filepath.Join(workflowsDir, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		return fmt.Errorf("workflow: create tasks dir: %w", err)
	}

	tasksPkg := filepath.Join(tasksDir, "tasks.cue")
	if _, err := os.Stat(tasksPkg); os.IsNotExist(err) {
		content := `package tasks

// #CommonTask defines shared task structure for workflow steps.
#CommonTask: {
	title:        string
	description?: string
	taskType:     "task" | "feature" | "bug" | *"task"
}

// #StandardSteps provides reusable step sequences.
#StandardSteps: {
	wait_and_eval: [
		{type: "wait", timeout: string | *"10m"},
		{type: "evaluate", expect: _},
		{type: "dismiss"},
	]
}
`
		if err := os.WriteFile(tasksPkg, []byte(content), 0o644); err != nil {
			return fmt.Errorf("workflow: write tasks.cue: %w", err)
		}
	}

	// Create aspects/ package directory for cross-cutting concerns.
	aspectsDir := filepath.Join(workflowsDir, "aspects")
	if err := os.MkdirAll(aspectsDir, 0o755); err != nil {
		return fmt.Errorf("workflow: create aspects dir: %w", err)
	}

	aspectsPkg := filepath.Join(aspectsDir, "aspects.cue")
	if _, err := os.Stat(aspectsPkg); os.IsNotExist(err) {
		content := `package aspects

// #Timeout defines timeout configuration for workflow aspects.
#Timeout: {
	default: string | *"10m"
	max:     string | *"1h"
}

// #Retry defines retry configuration for workflow aspects.
#Retry: {
	maxAttempts: int | *1
	backoff:     string | *"30s"
}
`
		if err := os.WriteFile(aspectsPkg, []byte(content), 0o644); err != nil {
			return fmt.Errorf("workflow: write aspects.cue: %w", err)
		}
	}

	return nil
}
