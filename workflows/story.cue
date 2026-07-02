// Single-task: implement in worktree, review, integrate
description: "Single-task: implement in worktree, review, integrate"
start_places: ["request"]
terminal_places: ["done"]
max_review_cycles: 3

// Required params validated at instantiation (pp-workflow-empty-params-dispatch).
// Missing or empty values raise a pre-flight error — no silent empty-prompt dispatch.
// `instance` is auto-injected by the engine, so only caller-supplied params
// belong here.
required: ["description"]

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
		// Self-heal integrate: before landing impl -> feature, sync the parent
		// (feature) branch INTO this story's impl branch in place. A clean sync
		// makes the impl -> feature land a fast-forward; a conflicting sync routes
		// to a resolver instead of fail-stopping (the wave-mate CHANGELOG-clash
		// case). Mirrors full-pipeline's main -> feature self-heal, sync_from=parent.
		id:        "sync"
		in:        ["merging"]
		out:       ["syncing"]
		action:    "sync-worktree"
		sync_from: "parent"
	},
	{
		id:     "land"
		in:     ["syncing"]
		out:    ["merged"]
		action: "merge-worktree"
		preconditions: [
			{
				category:   "signal"
				identity:   "land-sync:{{instance}}"
				constraint: "{status: \"clean\"}"
			},
		]
	},
	{
		id:  "resolve_needed"
		in:  ["syncing"]
		out: ["resolving"]
		preconditions: [
			{
				category:   "signal"
				identity:   "land-sync:{{instance}}"
				constraint: "{status: \"conflict\"}"
			},
		]
	},
	{
		id:   "resolve"
		in:   ["resolving"]
		out:  ["resolved"]
		role: "resolver"
		// The resolver works the impl worktree, which has `git merge <parent>` in
		// progress; workdir_signal repoints its workdir to that resolve worktree.
		workdir_signal: "resolve_worktree"
		description: """
			Resolve the merge conflict between the parent branch and this story's branch for: {{description}}

			Your worktree has a `git merge` of the parent (feature) branch into this
			story's branch IN PROGRESS, with conflict markers (<<<<<<<, =======,
			>>>>>>>). Resolve every conflicted file so it keeps BOTH this story's work
			and the changes already on the parent branch, then conclude the merge with
			`git commit --no-edit`. Do NOT run `git merge --abort`. Do not add features.
			"""
	},
	{
		id:        "re_sync"
		in:        ["resolved"]
		out:       ["syncing"]
		action:    "sync-worktree"
		sync_from: "parent"
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
