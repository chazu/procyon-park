// Single-task: implement in worktree, review, integrate
description: "Single-task: implement in worktree, review, integrate"
start_places: ["request"]
terminal_places: ["done"]
max_review_cycles: 3

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
		// Reviewers read a diff and emit a verdict — 15 minutes is plenty.
		// Omit `timeout` to fall back to the role's defaultTimeout.
		timeout:     900
		description: "Review implementation for: {{description}}. IMPORTANT: When done, write a verdict signal: pp signal verdict:{{instance}} decision pass (or decision fix if changes needed)."
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
		id:  "exhausted"
		in:  ["reviewed"]
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
		id:   "fix"
		in:   ["fixing"]
		out:  ["implemented"]
		role: "fixer"
		description: """
			Fix issues found in review for: {{description}}

			Verdict rationale (from reviewer/foreman): {{verdict_reason}}

			Observations recorded during review (address each one):
			{{review_observations}}

			Focus on resolving the specific issues above rather than re-searching the
			observation log. If more context is needed you may still read observations
			via pp read observation, but the list above is the authoritative work queue.
			"""
	},
]
