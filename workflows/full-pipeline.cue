// Full pipeline: plan, implement, review+test, fix cycle
description: "Full pipeline: plan, implement, review+test, fix cycle"
start_places: ["request"]
terminal_places: ["done"]
max_review_cycles: 3

transitions: [
	{
		id:     "setup"
		in:     ["request"]
		out:    ["planning"]
		action: "create-worktree"
	},
	{
		id:          "plan"
		in:          ["planning"]
		out:         ["plan_ready"]
		role:        "planner"
		description: "Create implementation plan for: {{description}}"
	},
	{
		id:          "dispatch_integrate"
		in:          ["plan_ready"]
		out:         ["integrated"]
		role:        "foreman"
		description: "Dispatch implementers and integrate results for: {{description}}"
	},
	{
		id:  "fork"
		in:  ["integrated"]
		out: ["reviewing", "testing"]
		preconditions: [
			{
				category: "event"
				identity: "task-complete:{{instance}}:task:dispatch_integrate"
			},
		]
	},
	{
		id:          "review"
		in:          ["reviewing"]
		out:         ["review_done"]
		role:        "reviewer"
		description: "Review implementation for: {{description}}"
	},
	{
		id:          "test"
		in:          ["testing"]
		out:         ["test_done"]
		role:        "tester"
		description: "Test implementation for: {{description}}"
	},
	{
		id:          "evaluate"
		in:          ["review_done", "test_done"]
		out:         ["evaluating"]
		role:        "foreman"
		description: "Evaluate review and test results for: {{description}}"
	},
	{
		id:  "pass"
		in:  ["evaluating"]
		out: ["merging"]
		preconditions: [
			{
				category:   "signal"
				identity:   "verdict:{{instance}}"
				constraint: "{decision: \"pass\"}"
			},
		]
	},
	{
		id:  "fix_needed"
		in:  ["evaluating"]
		out: ["fixing"]
		preconditions: [
			{
				category:   "signal"
				identity:   "verdict:{{instance}}"
				constraint: "{decision: \"fix\"}"
			},
		]
	},
	{
		id:  "exhausted"
		in:  ["evaluating"]
		out: ["merging"]
		preconditions: [
			{
				category:   "signal"
				identity:   "verdict:{{instance}}"
				constraint: "{decision: \"exhausted\"}"
			},
		]
	},
	{
		id:          "fix"
		in:          ["fixing"]
		out:         ["fix_done"]
		role:        "fixer"
		description: "Fix issues found in review/test for: {{description}}"
	},
	{
		id:  "re_review"
		in:  ["fix_done"]
		out: ["reviewing", "testing"]
		preconditions: [
			{
				category: "event"
				identity: "task-complete:{{instance}}:task:fix"
			},
		]
	},
	{
		id:     "integrate"
		in:     ["merging"]
		out:    ["merged"]
		action: "merge-worktree"
	},
	{
		id:     "notify"
		in:     ["merged"]
		out:    ["done"]
		action: "notify-head"
	},
]
