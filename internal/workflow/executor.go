// Package workflow executor provides the central engine for running workflows.
//
// The Executor parses workflow definitions, validates parameters, resolves CUE
// references, creates instances, and dispatches steps sequentially via registered
// StepHandlers. It supports cancellation, per-step timeouts, crash recovery,
// and pluggable callbacks for telemetry and notifications.
package workflow

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// LivenessChecker determines whether an agent is still alive.
type LivenessChecker interface {
	IsAlive(agentName, repoName string) (bool, error)
}

// CompletionNotifier is called when a workflow instance reaches a terminal state.
type CompletionNotifier interface {
	OnComplete(instance *Instance)
}

// TelemetryHook receives events during workflow execution for observability.
type TelemetryHook func(event string, data map[string]string)

// NotifyFunc is called when a workflow step fails, allowing external alerting.
type NotifyFunc func(instance *Instance, stepIndex int, err error)

// CancelKillFn dismisses the active agent during cancellation.
type CancelKillFn func(ctx context.Context, agentName, repoName string) error

// runningInstance tracks a running workflow's cancel function and metadata.
type runningInstance struct {
	cancel context.CancelFunc
	inst   *Instance
}

// Executor is the central workflow engine.
type Executor struct {
	store    *Store
	registry map[string]StepHandler
	repoRoot string

	// Pluggable callbacks (configured via functional options).
	telemetry    TelemetryHook
	notifyFunc   NotifyFunc
	cancelKillFn CancelKillFn
	liveness     LivenessChecker
	completion   CompletionNotifier

	mu      sync.Mutex
	running map[string]*runningInstance // keyed by instance ID
}

// Option configures an Executor.
type Option func(*Executor)

// WithTelemetry sets the telemetry hook for workflow events.
func WithTelemetry(fn TelemetryHook) Option {
	return func(e *Executor) { e.telemetry = fn }
}

// WithNotifyFunc sets the failure notification callback.
func WithNotifyFunc(fn NotifyFunc) Option {
	return func(e *Executor) { e.notifyFunc = fn }
}

// WithCancelKillFn sets the function used to dismiss agents during cancellation.
func WithCancelKillFn(fn CancelKillFn) Option {
	return func(e *Executor) { e.cancelKillFn = fn }
}

// WithLivenessChecker sets the liveness checker for crash recovery.
func WithLivenessChecker(lc LivenessChecker) Option {
	return func(e *Executor) { e.liveness = lc }
}

// WithCompletionNotifier sets the completion callback.
func WithCompletionNotifier(cn CompletionNotifier) Option {
	return func(e *Executor) { e.completion = cn }
}

// NewExecutor creates an Executor with the given store, step registry, and options.
func NewExecutor(store *Store, registry map[string]StepHandler, repoRoot string, opts ...Option) *Executor {
	e := &Executor{
		store:    store,
		registry: registry,
		repoRoot: repoRoot,
		running:  make(map[string]*runningInstance),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Run parses a workflow by name, validates params, resolves the workflow,
// creates a persisted instance, and launches the execute loop in a goroutine.
// Returns the instance ID for tracking.
func (e *Executor) Run(ctx context.Context, workflowName string, repoName string, params map[string]string) (string, error) {
	e.emit("workflow_start", map[string]string{
		"workflow": workflowName,
		"repo":     repoName,
	})

	// Phase 1: Parse (validate structure, extract param declarations).
	parsed, err := ParseWorkflow(workflowName, e.repoRoot)
	if err != nil {
		return "", fmt.Errorf("executor: parse %q: %w", workflowName, err)
	}

	// Phase 2: Validate params — reject unknown, apply defaults, check required.
	resolvedParams, err := validateParams(parsed.Params, params)
	if err != nil {
		return "", fmt.Errorf("executor: params for %q: %w", workflowName, err)
	}

	// Phase 3: Resolve CUE references with concrete parameter values.
	wf, err := ResolveWorkflow(parsed, resolvedParams)
	if err != nil {
		return "", fmt.Errorf("executor: resolve %q: %w", workflowName, err)
	}

	// Phase 4: Create instance.
	inst := &Instance{
		ID:           GenerateInstanceID(),
		WorkflowName: wf.Name,
		RepoName:     repoName,
		RepoRoot:     e.repoRoot,
		Status:       StatusPending,
		CurrentStep:  0,
		Params:       resolvedParams,
		Context:      WorkflowContext{ActiveRepo: repoName},
		StepResults:  make([]StepResult, 0, len(wf.Steps)),
		StartedAt:    time.Now().UTC(),
	}

	if err := e.store.CreateInstance(inst); err != nil {
		return "", fmt.Errorf("executor: persist instance: %w", err)
	}

	// Phase 5: Launch execute loop.
	loopCtx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.running[inst.ID] = &runningInstance{cancel: cancel, inst: inst}
	e.mu.Unlock()

	go e.executeLoop(loopCtx, inst, wf.Steps)

	return inst.ID, nil
}

// Cancel cancels a running workflow instance. If an active agent exists, it is
// dismissed via CancelKillFn.
func (e *Executor) Cancel(ctx context.Context, instanceID string) error {
	e.mu.Lock()
	ri, ok := e.running[instanceID]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("executor: instance %s not running", instanceID)
	}

	// Cancel the context to stop the execute loop.
	ri.cancel()

	// Dismiss active agent if configured and applicable.
	if e.cancelKillFn != nil && ri.inst.Context.ActiveAgent != "" {
		repoName := ri.inst.Context.ActiveRepo
		if repoName == "" {
			repoName = ri.inst.RepoName
		}
		if err := e.cancelKillFn(ctx, ri.inst.Context.ActiveAgent, repoName); err != nil {
			// Log but don't fail — the context cancellation is the primary mechanism.
			e.emit("cancel_kill_error", map[string]string{
				"instance": instanceID,
				"agent":    ri.inst.Context.ActiveAgent,
				"error":    err.Error(),
			})
		}
	}

	return nil
}

// RecoverOnStartup scans for instances that were running when the daemon last
// stopped. For each, it checks agent liveness and either resumes or fails the instance.
func (e *Executor) RecoverOnStartup(ctx context.Context) error {
	instances, err := e.store.ListInstances(StatusRunning, "")
	if err != nil {
		return fmt.Errorf("executor: list running instances: %w", err)
	}

	for _, inst := range instances {
		if err := e.recoverInstance(ctx, inst); err != nil {
			e.emit("recovery_error", map[string]string{
				"instance": inst.ID,
				"error":    err.Error(),
			})
		}
	}
	return nil
}

// recoverInstance handles recovery of a single instance. If the active agent is
// alive, the instance is resumed from its current step. Otherwise it is failed.
func (e *Executor) recoverInstance(ctx context.Context, inst *Instance) error {
	e.emit("recovery_check", map[string]string{
		"instance": inst.ID,
		"workflow": inst.WorkflowName,
		"step":     fmt.Sprintf("%d", inst.CurrentStep),
		"agent":    inst.Context.ActiveAgent,
	})

	// If there's an active agent, check its liveness.
	if inst.Context.ActiveAgent != "" && e.liveness != nil {
		repoName := inst.Context.ActiveRepo
		if repoName == "" {
			repoName = inst.RepoName
		}
		alive, err := e.liveness.IsAlive(inst.Context.ActiveAgent, repoName)
		if err != nil {
			return fmt.Errorf("liveness check for %s: %w", inst.Context.ActiveAgent, err)
		}
		if !alive {
			return e.failInstance(inst, fmt.Errorf("agent %q not alive after restart", inst.Context.ActiveAgent))
		}
	}

	// Re-parse and resolve the workflow to get the step list.
	parsed, err := ParseWorkflow(inst.WorkflowName, inst.RepoRoot)
	if err != nil {
		return e.failInstance(inst, fmt.Errorf("re-parse workflow %q: %w", inst.WorkflowName, err))
	}
	wf, err := ResolveWorkflow(parsed, inst.Params)
	if err != nil {
		return e.failInstance(inst, fmt.Errorf("re-resolve workflow %q: %w", inst.WorkflowName, err))
	}

	// Resume the execute loop from the current step.
	loopCtx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.running[inst.ID] = &runningInstance{cancel: cancel, inst: inst}
	e.mu.Unlock()

	go e.executeLoop(loopCtx, inst, wf.Steps)
	return nil
}

// executeLoop runs steps sequentially from inst.CurrentStep to the end.
// It persists state after each step and handles cancellation and timeouts.
func (e *Executor) executeLoop(ctx context.Context, inst *Instance, steps []Step) {
	defer e.cleanup(inst.ID)

	// Transition to running.
	inst.Status = StatusRunning
	e.persistOrLog(inst)

	for i := inst.CurrentStep; i < len(steps); i++ {
		// Check for cancellation before each step.
		if ctx.Err() != nil {
			e.cancelInstance(inst)
			return
		}

		step := steps[i]
		inst.CurrentStep = i

		e.emit("step_start", map[string]string{
			"instance": inst.ID,
			"step":     fmt.Sprintf("%d", i),
			"type":     step.Type,
		})

		// Look up handler.
		handler, ok := e.registry[step.Type]
		if !ok {
			e.failAndNotify(inst, i, fmt.Errorf("unknown step type: %q", step.Type))
			return
		}

		// Resolve _ctx references in step config.
		resolvedConfig, err := ResolveStepConfig(inst, step.Config, step.Type)
		if err != nil {
			e.failAndNotify(inst, i, fmt.Errorf("resolve config: %w", err))
			return
		}

		// Execute with optional per-step timeout.
		var result *StepResult
		var execErr error

		if step.Timeout != "" {
			timeout, parseErr := time.ParseDuration(step.Timeout)
			if parseErr != nil {
				e.failAndNotify(inst, i, fmt.Errorf("parse step timeout %q: %w", step.Timeout, parseErr))
				return
			}
			stepCtx, cancel := context.WithTimeout(ctx, timeout)
			result, execErr = handler.Execute(stepCtx, inst, i, resolvedConfig)
			cancel()
		} else {
			result, execErr = handler.Execute(ctx, inst, i, resolvedConfig)
		}

		// Handle execution errors (distinct from step-level failures).
		if execErr != nil {
			e.failAndNotify(inst, i, execErr)
			return
		}

		// Persist step result.
		inst.StepResults = append(inst.StepResults, *result)
		e.persistOrLog(inst)

		e.emit("step_end", map[string]string{
			"instance": inst.ID,
			"step":     fmt.Sprintf("%d", i),
			"type":     step.Type,
			"status":   result.Status,
		})

		// If the step failed, fail the workflow.
		if result.Status == "failed" {
			e.failAndNotify(inst, i, fmt.Errorf("step %d (%s) failed: %s", i, step.Type, result.Error))
			return
		}
	}

	// All steps completed successfully.
	e.completeInstance(inst)
}

// validateParams checks provided params against declared params: rejects unknown,
// applies defaults, and ensures required params are present.
func validateParams(declared map[string]Param, provided map[string]string) (map[string]string, error) {
	result := make(map[string]string)

	// Check for unknown params.
	for k := range provided {
		if _, ok := declared[k]; !ok {
			return nil, fmt.Errorf("unknown parameter %q", k)
		}
	}

	// Apply defaults and check required.
	for name, param := range declared {
		if v, ok := provided[name]; ok {
			result[name] = v
		} else if param.Default != nil {
			result[name] = fmt.Sprintf("%v", param.Default)
		} else if param.Required {
			return nil, fmt.Errorf("required parameter %q not provided", name)
		}
	}

	return result, nil
}

// failInstance transitions an instance to failed status.
func (e *Executor) failInstance(inst *Instance, err error) error {
	inst.Status = StatusFailed
	inst.Error = err.Error()
	now := time.Now().UTC()
	inst.CompletedAt = &now
	if persistErr := e.store.UpdateInstance(inst); persistErr != nil {
		return fmt.Errorf("persist failed instance: %w", persistErr)
	}
	if e.completion != nil {
		e.completion.OnComplete(inst)
	}
	return nil
}

// cancelInstance transitions an instance to cancelled status.
func (e *Executor) cancelInstance(inst *Instance) {
	inst.Status = StatusCancelled
	now := time.Now().UTC()
	inst.CompletedAt = &now
	e.persistOrLog(inst)
	if e.completion != nil {
		e.completion.OnComplete(inst)
	}
}

// completeInstance transitions an instance to completed status.
func (e *Executor) completeInstance(inst *Instance) {
	inst.Status = StatusCompleted
	now := time.Now().UTC()
	inst.CompletedAt = &now
	e.persistOrLog(inst)
	e.emit("workflow_complete", map[string]string{
		"instance": inst.ID,
		"workflow": inst.WorkflowName,
	})
	if e.completion != nil {
		e.completion.OnComplete(inst)
	}
}

// failAndNotify fails the instance and calls the notification callback if set.
func (e *Executor) failAndNotify(inst *Instance, stepIndex int, err error) {
	_ = e.failInstance(inst, err)
	if e.notifyFunc != nil {
		e.notifyFunc(inst, stepIndex, err)
	}
	e.emit("workflow_failed", map[string]string{
		"instance": inst.ID,
		"workflow": inst.WorkflowName,
		"step":     fmt.Sprintf("%d", stepIndex),
		"error":    err.Error(),
	})
}

// cleanup removes the instance from the running map.
func (e *Executor) cleanup(instanceID string) {
	e.mu.Lock()
	delete(e.running, instanceID)
	e.mu.Unlock()
}

// persistOrLog updates the instance in the store, logging errors via telemetry.
func (e *Executor) persistOrLog(inst *Instance) {
	if err := e.store.UpdateInstance(inst); err != nil {
		e.emit("persist_error", map[string]string{
			"instance": inst.ID,
			"error":    err.Error(),
		})
	}
}

// emit sends a telemetry event if a hook is configured.
func (e *Executor) emit(event string, data map[string]string) {
	if e.telemetry != nil {
		e.telemetry(event, data)
	}
}

// GetInstance retrieves an instance from the store.
func (e *Executor) GetInstance(repoName, instanceID string) (*Instance, error) {
	return e.store.GetInstance(repoName, instanceID)
}

// ListInstances returns instances matching the given filters.
func (e *Executor) ListInstances(status InstanceStatus, repoName string) ([]*Instance, error) {
	return e.store.ListInstances(status, repoName)
}

// IsRunning checks if an instance is currently executing.
func (e *Executor) IsRunning(instanceID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.running[instanceID]
	return ok
}

