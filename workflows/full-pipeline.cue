// Full pipeline: dispatch waves, review+test, fix cycle
// Planning is decoupled — work items must already have children before running.
description: "Full pipeline: dispatch waves, review+test, fix cycle"
start_places: ["request"]
terminal_places: ["done"]
max_review_cycles: 3

transitions: [
	{
		id:     "setup"
		in:     ["request"]
		out:    ["dispatching"]
		action: "create-worktree"
	},
	{
		id:     "dispatch"
		in:     ["dispatching"]
		out:    ["integrated"]
		action: "dispatch-waves"
	},
	{
		id:  "fork"
		in:  ["integrated"]
		out: ["reviewing", "testing"]
	},
	{
		id:          "review"
		in:          ["reviewing"]
		out:         ["review_done"]
		role:        "reviewer"
		description: "Review implementation for: {{description}}. IMPORTANT: When done, write observations about what you found."
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
		description: "Evaluate review and test results for: {{description}}. Read observations from reviewers/testers. Write verdict: pp signal verdict:{{instance}} decision pass (or fix/exhausted). Also write review cycle count: pp signal review_cycle:{{instance}} count N."
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
