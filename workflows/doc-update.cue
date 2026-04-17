// Doc-update: implement documentation changes directly in the main working
// directory (no worktree) and commit to a dedicated branch. Reviewed by a
// specialized doc-reviewer focused on clarity and accuracy.
//
// Why no worktree? Documentation changes rarely conflict with in-flight code
// work and touch a disjoint file set (docs/*, *.md). Skipping the worktree
// step keeps the workflow fast and avoids merge overhead for purely textual
// edits. The implementer creates and commits to a feature branch in the main
// repo checkout; no merge-worktree step is needed — the branch is the artifact.
description: "Doc-update: edit docs in main checkout, review for clarity, commit to branch"
start_places: ["request"]
terminal_places: ["done"]
max_review_cycles: 2

transitions: [
	{
		id:   "implement"
		in:   ["request"]
		out:  ["implemented"]
		role: "implementer"
		description: """
			Documentation-only change: {{description}}

			Scope: This workflow is for documentation edits ONLY (README, docs/,
			code comments, inline help text). Do NOT modify source code logic,
			tests, or configuration unless the description explicitly calls for it.

			Working directory: you are running in the main repo checkout — there
			is no worktree for this workflow. Before editing, create a dedicated
			branch so your commit does not land on the default branch:

			  git checkout -b docs/{{instance}}

			Then make the edits, commit on that branch, and leave the branch in
			place (no merge step runs — the branch itself is the artifact for a
			human to merge or open a PR from).

			Verify claims: when documenting behavior, read the code you describe
			and make sure the prose matches reality. Stale or wrong docs are
			worse than missing docs.
			"""
	},
	{
		id:      "review"
		in:      ["implemented"]
		out:     ["reviewed"]
		role:    "doc-reviewer"
		timeout: 900
		description: """
			Review the documentation change for: {{description}}

			Focus on clarity (will a new reader understand this?) and accuracy
			(does the prose match the code/config it describes?). Verify any
			commands, paths, or symbol names mentioned.

			IMPORTANT: When done, write a verdict signal:
			  pp signal verdict:{{instance}} decision pass
			  (or decision fix if changes are needed)
			"""
	},
	{
		id:  "pass"
		in:  ["reviewed"]
		out: ["approved"]
		preconditions: [
			{
				category:   "signal"
				identity:   "verdict:{{instance}}"
				constraint: "{decision: \"pass\"}"
			},
		]
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
		out: ["approved"]
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
			Address documentation review feedback for: {{description}}

			Verdict rationale (from reviewer/foreman): {{verdict_reason}}

			Observations recorded during review (address each one):
			{{review_observations}}

			Stay on the existing docs/{{instance}} branch — do not create a new
			one. Amend the documentation to resolve each observation, then commit
			on top of the existing branch.
			"""
	},
	{
		id:     "notify"
		in:     ["approved"]
		out:    ["done"]
		action: "notify-head"
	},
]
