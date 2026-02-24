// Package workflow defines the CUE schema for procyon-park workflow definitions.
//
// Workflows are automated sequences of steps that the daemon executes.
// Each workflow file defines a single workflow that conforms to #Workflow.
//
// Place workflow files in:
//   - ~/.procyon-park/workflows/  (global, system-wide)
//   - <repo>/.procyon-park/workflows/  (repo-specific, takes precedence)
//
// Run workflows with: pp workflow run <name> [--param key=value]...
//
// Context Variables:
//   - _input: Workflow parameters passed at runtime (e.g., _input.taskId)
//   - _ctx: Implicit workflow context populated by step execution:
//       - _ctx.taskId: The primary task ID (set by first spawn step)
//       - _ctx.activeAgent: Most recently spawned agent name
//       - _ctx.activeBranch: Most recently created branch
//       - _ctx.activeRepo: Repository the workflow is running in
//       - _ctx.previousOutput: Output from the last wait step
package workflow

// #WorkflowContext defines the implicit context available during step execution.
// Steps can reference _ctx fields to access information set by previous steps.
// This enables context passing without brittle index-based step references.
#WorkflowContext: {
	// taskId is the primary task ID driving the workflow.
	taskId?: string

	// activeAgent is the name of the most recently spawned agent.
	activeAgent?: string

	// activeBranch is the branch of the most recently spawned agent.
	// Preserved after dismiss if noMerge=true.
	activeBranch?: string

	// activeRepo is the repository the workflow is running in.
	activeRepo?: string

	// previousOutput holds the JSON output from the last wait step.
	previousOutput?: _
}

// #Workflow defines a complete workflow definition.
//
// Example:
//   my_workflow: {
//       name: "my-workflow"
//       description: "Does something useful"
//       params: {taskTitle: {type: "string", required: true}}
//       steps: [
//           {type: "spawn", role: "cub", task: {title: _input.taskTitle}},
//           {type: "wait", timeout: "10m"},
//           {type: "evaluate", expect: {exitCode: 0}},
//           {type: "dismiss"},
//       ]
//   }
#Workflow: {
	// name identifies the workflow. Must be lowercase with hyphens (e.g., "build-and-check").
	// Used as the filename (name.cue) and in `pp workflow run <name>`.
	name: string & =~"^[a-z][a-z0-9-]*$"

	// description provides a human-readable summary shown in `pp workflow defs`.
	description: string | *""

	// params declares workflow parameters. Reference values with _input.paramName in steps.
	params: [string]: #Param

	// steps lists the workflow steps executed in sequence. At least one step is required.
	steps: [...#Step] & [_, ...]

	// aspects optionally inject steps before/after matching steps for cross-cutting concerns.
	aspects?: [...#Aspect]
}

// #Aspect defines a cross-cutting concern that injects steps around matching steps.
// Aspects are applied at load time, transforming the step list before execution.
// Multiple aspects are applied in declaration order (first aspect is innermost).
#Aspect: {
	// match specifies which steps this aspect applies to. All non-empty fields
	// must match (AND semantics).
	match: #AspectMatch

	// before lists steps to inject before each matching step.
	before?: [...#Step]

	// after lists steps to inject after each matching step.
	after?: [...#Step]
}

// #AspectMatch defines criteria for selecting steps in an aspect.
// Empty fields match all values for that criterion.
#AspectMatch: {
	// type matches by step type (e.g., "spawn", "wait", "evaluate", "dismiss", "gate").
	type?: string

	// name matches by step name using glob patterns (e.g., "Build*").
	// For spawn steps, matches against task.title. For others, matches step type.
	name?: string

	// role matches spawn steps by agent role (e.g., "cub", "king").
	// Only applies to spawn steps; non-spawn steps won't match if role is set.
	role?: string
}

// #Param defines a workflow parameter declaration.
#Param: {
	// type specifies the parameter type: "string", "int", or "bool".
	type: "string" | "int" | "bool"

	// required indicates whether the parameter must be provided at runtime.
	// Defaults to true.
	required: bool | *true

	// default provides a default value when required is false.
	default?: _
}

// #Step is a union of all valid step types.
#Step: #SpawnStep | #WaitStep | #EvaluateStep | #DismissStep | #GateStep

// #TaskDef defines a task to be created for a spawned agent.
#TaskDef: {
	// title is the task title (required).
	title: string

	// description provides additional context for the agent.
	description?: string

	// taskType categorizes the task. Defaults to "task".
	taskType: "task" | "feature" | "bug" | *"task"
}

// #SpawnStep launches a new agent to work on a task.
// The agent runs in its own worktree and branch.
#SpawnStep: {
	type: "spawn"

	// timeout limits how long the spawn operation can take (e.g., "30s", "2m").
	timeout?: string

	// role specifies the agent role. Defaults to "cub".
	role: string | *"cub"

	// task defines the work for the spawned agent.
	task: #TaskDef

	// repo overrides the target repository (defaults to current workflow repo).
	repo?: string

	// branch specifies the base branch for the agent's worktree.
	branch?: string
}

// #WaitStep waits for output from the most recently spawned agent.
// Polls for messages addressed to "daemon" from the agent.
#WaitStep: {
	type: "wait"

	// timeout specifies the maximum wait time. Defaults to "10m".
	// Common values: "5m", "10m", "15m", "30m", "1h".
	timeout: string | *"10m"
}

// #EvaluateStep validates the previous wait step's output using CUE unification.
// The expect value is unified with the output; the step passes if:
//   1. The unified result is concrete and valid
//   2. expect subsumes the actual output (all expected fields are present)
#EvaluateStep: {
	type: "evaluate"

	// timeout limits the evaluation duration (usually fast, rarely needed).
	timeout?: string

	// expect defines the expected output structure. Common pattern:
	//   expect: {exitCode: 0}
	expect: _
}

// #DismissStep terminates the most recently spawned agent.
// By default, merges the agent's work back to the parent branch.
#DismissStep: {
	type: "dismiss"

	// timeout limits how long the dismiss operation can take.
	timeout?: string

	// noMerge skips merging the agent's work. The work remains on the feature branch.
	// Useful when you want to review changes before merging manually.
	noMerge?: bool
}

// #GateStep pauses workflow execution. Two variants are available:
// - Human gate: waits for approval from specified users
// - Timer gate: waits for a fixed duration
#GateStep: #HumanGateStep | #TimerGateStep

// #HumanGateStep requires approval from one of the specified approvers.
// Sends a prompt message to approvers and waits for `pp workflow approve`.
#HumanGateStep: {
	type:     "gate"
	gateType: "human"

	// approvers lists users who can approve the gate. At least one is required.
	// Users receive a message with approve/reject instructions.
	approvers: [...string] & [_, ...]

	// prompt customizes the approval request message.
	prompt?: string

	// timeout specifies how long to wait for approval. Defaults to "30m".
	// The workflow fails if no approval is received within the timeout.
	timeout?: string
}

// #TimerGateStep delays workflow execution for a fixed duration.
// Useful for rate limiting, cooldown periods, or scheduled delays.
#TimerGateStep: {
	type:     "gate"
	gateType: "timer"

	// duration specifies how long to wait (e.g., "5m", "1h").
	duration: string

	// timeout is an optional execution timeout (rarely needed since duration is known).
	timeout?: string
}
