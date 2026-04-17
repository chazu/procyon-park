// Single-task: implement in worktree, review, integrate
description: "Single-task: implement in worktree, review, integrate"
start_places: ["request"]
terminal_places: ["done"]
max_review_cycles: 3

// Shared fragments embedded below. Hidden fields (underscore-prefixed) are
// stripped by `cue export`, so they never appear in the emitted template.
//
//   _worktree_bookend.create  → action: "create-worktree"
//   _worktree_bookend.merge   → action: "merge-worktree"
//   _notify_on_complete       → action: "notify-head"
//
// Transitions embed the fragment (bare reference inside a struct literal)
// alongside their own id/in/out — this keeps the action wiring DRY and makes
// the bookended create → merge → notify pattern easy to add to new templates.
_worktree_bookend: {
	create: {action: "create-worktree"}
	merge: {action:  "merge-worktree"}
}
_notify_on_complete: {action: "notify-head"}

transitions: [
	{
		id:  "setup"
		in:  ["request"]
		out: ["ready"]
		_worktree_bookend.create
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
		id:  "integrate"
		in:  ["merging"]
		out: ["merged"]
		_worktree_bookend.merge
	},
	{
		id:  "notify"
		in:  ["merged"]
		out: ["done"]
		_notify_on_complete
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
