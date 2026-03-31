// Single-task: implement in worktree, review, integrate
description: "Single-task: implement in worktree, review, integrate"
start_places: ["request"]
terminal_places: ["done"]

transitions: [
	{
		id:     "setup"
		in:     ["request"]
		out:    ["ready"]
		action: "create-worktree"
	},
	{
		id:          "implement"
		in:          ["ready"]
		out:         ["implemented"]
		role:        "implementer"
		description: "{{description}}"
	},
	{
		id:          "review"
		in:          ["implemented"]
		out:         ["reviewed"]
		role:        "reviewer"
		description: "Review implementation for: {{description}}"
		preconditions: [
			{
				category: "event"
				identity: "task-complete:{{instance}}:task:implement"
			},
		]
	},
	{
		id:  "pass"
		in:  ["reviewed"]
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
	{
		id:  "fail"
		in:  ["reviewed"]
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
		id:          "fix"
		in:          ["fixing"]
		out:         ["implemented"]
		role:        "fixer"
		description: "Fix issues found in review for: {{description}}"
	},
]
