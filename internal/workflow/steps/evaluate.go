// Package steps provides workflow step handler implementations.
package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/chazu/procyon-park/internal/workflow"
)

// EvaluateHandler validates the previous wait step's output against an expected
// CUE schema using CUE unification. The expect value from the step config is
// unified with the actual output; the step passes if expect subsumes actual.
type EvaluateHandler struct{}

// Execute runs CUE unification of the expected schema against the most recent
// wait step output found in instance.StepResults.
func (h *EvaluateHandler) Execute(ctx context.Context, instance *workflow.Instance, stepIndex int, config json.RawMessage) (*workflow.StepResult, error) {
	now := time.Now().UTC()
	result := &workflow.StepResult{
		StepIndex: stepIndex,
		StepType:  "evaluate",
		StartedAt: now,
	}

	// Parse the evaluate config.
	var cfg workflow.EvaluateConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		result.Status = "failed"
		result.Error = fmt.Sprintf("invalid evaluate config: %v", err)
		end := time.Now().UTC()
		result.EndedAt = &end
		return result, nil
	}

	// Find the most recent wait step output.
	actual, err := findLastWaitOutput(instance)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		end := time.Now().UTC()
		result.EndedAt = &end
		return result, nil
	}

	// Run CUE evaluation.
	if err := cueEvaluate(cfg.Expect, actual); err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		end := time.Now().UTC()
		result.EndedAt = &end
		return result, nil
	}

	result.Status = "completed"
	result.Output = actual
	end := time.Now().UTC()
	result.EndedAt = &end
	return result, nil
}

// findLastWaitOutput walks instance.StepResults backwards to find the most
// recent completed wait step and returns its output.
func findLastWaitOutput(instance *workflow.Instance) (json.RawMessage, error) {
	for i := len(instance.StepResults) - 1; i >= 0; i-- {
		sr := instance.StepResults[i]
		if sr.StepType == "wait" && sr.Status == "completed" && len(sr.Output) > 0 {
			return sr.Output, nil
		}
	}

	// Fall back to WorkflowContext.PreviousOutput.
	if len(instance.Context.PreviousOutput) > 0 {
		return instance.Context.PreviousOutput, nil
	}

	return nil, fmt.Errorf("evaluate: no previous wait step output found")
}

// cueEvaluate compiles both expect and actual as CUE values, unifies them,
// and checks that expect subsumes actual (all expected fields are present and match).
func cueEvaluate(expect json.RawMessage, actual json.RawMessage) error {
	ctx := cuecontext.New()

	// Compile the expected value.
	expectCUE := fmt.Sprintf("expect: %s", string(expect))
	expectVal := ctx.CompileString(expectCUE)
	if expectVal.Err() != nil {
		return fmt.Errorf("evaluate: compile expect: %w", expectVal.Err())
	}
	expectField := expectVal.LookupPath(cue.ParsePath("expect"))

	// Compile the actual value.
	actualCUE := fmt.Sprintf("actual: %s", string(actual))
	actualVal := ctx.CompileString(actualCUE)
	if actualVal.Err() != nil {
		return fmt.Errorf("evaluate: compile actual: %w", actualVal.Err())
	}
	actualField := actualVal.LookupPath(cue.ParsePath("actual"))

	// Unify expect with actual.
	unified := expectField.Unify(actualField)
	if err := unified.Validate(); err != nil {
		return fmt.Errorf("evaluate: unification failed: expected %s but got %s: %w",
			string(expect), string(actual), err)
	}

	// Check subsumption: expect must subsume actual (actual satisfies expect).
	if err := expectField.Subsume(actualField); err != nil {
		return fmt.Errorf("evaluate: output does not match expected schema: expected %s but got %s: %w",
			string(expect), string(actual), err)
	}

	return nil
}
