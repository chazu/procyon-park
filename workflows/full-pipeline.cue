// Full pipeline: dispatch waves, review+test, fix cycle
// Planning is decoupled — work items must already have children before running.
description: "Full pipeline: dispatch waves, review+test, fix cycle"
start_places: ["request"]
terminal_places: ["done"]
max_review_cycles: 3

// Workflow-level affinity default (F4.2). Applies to every task tuple
// spawned by this workflow unless a transition overrides it or the CLI
// layers an override on top. Shape matches workflows/_affinity.cue
// #Affinity (launcher_only?, workers?, model?, repo?, team?).
affinity: {team: true}

// Shared fragments for the create-worktree → merge-worktree → notify-head
// bookend. Hidden fields are stripped by `cue export` so they never reach
// the emitted template.
_worktree_bookend: {
	create: {action: "create-worktree"}
	merge: {action:  "merge-worktree"}
}
_notify_on_complete: {action: "notify-head"}

transitions: [
	{
		id:  "setup"
		in:  ["request"]
		out: ["dispatching"]
		_worktree_bookend.create
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
		// Per-transition affinity override (F4.1): keep foreman evaluation on
		// the launcher's own worker pool so verdicts ride the same trust
		// boundary as the run.
		affinity: {launcher_only: true}
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
		id:   "fix"
		in:   ["fixing"]
		out:  ["fix_done"]
		role: "fixer"
		description: """
			Fix issues found in review/test for: {{description}}

			Verdict rationale (from foreman): {{verdict_reason}}

			Observations recorded by reviewers/testers (address each one):
			{{review_observations}}

			Focus on resolving the specific issues above rather than re-searching the
			observation log. If more context is needed you may still read observations
			via pp read observation, but the list above is the authoritative work queue.
			"""
	},
	{
		id:  "re_review"
		in:  ["fix_done"]
		out: ["reviewing", "testing"]
	},
	{
		id:     "sync"
		in:     ["merging"]
		out:    ["syncing"]
		action: "sync-worktree"
	},
	{
		id:     "re_sync"
		in:     ["resolved"]
		out:    ["syncing"]
		action: "sync-worktree"
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
		// The resolver works the dedicated resolve worktree (git merge main in
		// progress), NOT the impl worktree — workdir_signal repoints it.
		workdir_signal: "resolve_worktree"
		description: """
			Resolve the merge conflict between main and the feature branch for: {{description}}

			Your worktree has `git merge main` IN PROGRESS with conflict markers.
			Resolve every conflicted file so it keeps BOTH this branch's work and the
			changes that landed on main, then conclude the merge with `git commit
			--no-edit`. Do NOT run `git merge --abort`. Do not add new features.
			"""
	},
	{
		id:  "notify"
		in:  ["merged"]
		out: ["done"]
		_notify_on_complete
	},
]
