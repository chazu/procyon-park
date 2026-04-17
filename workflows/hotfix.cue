// Hotfix: implement in worktree, test (no review), merge directly to main.
// Use for urgent fixes that bypass the review cycle but require test verification.
// Differs from story-lite by:
//   - adding a "test" step between implement and integrate (role: tester)
//   - targeting the main branch directly (integrate merges feature branch into main)
description: "Hotfix: implement, test, merge directly to main (no review)"
start_places: ["request"]
terminal_places: ["done"]

transitions: [
	{
		id:            "setup"
		in:            ["request"]
		out:           ["ready"]
		action:        "create-worktree"
		// Branch the worktree directly off main. On integrate, merge-worktree
		// will merge the impl branch back into main — no feature branch is created.
		parent_branch: "main"
	},
	{
		id:          "implement"
		in:          ["ready"]
		out:         ["implemented"]
		role:        "implementer"
		description: "{{description}}"
	},
	{
		id:          "test"
		in:          ["implemented"]
		out:         ["tested"]
		role:        "tester"
		description: "Run and verify tests for hotfix: {{description}}. Ensure the change is correct and regression-free; record any failing tests or unexpected behavior as observations. No review cycle — if tests pass, the change merges to main."
	},
	{
		id:     "integrate"
		in:     ["tested"]
		out:    ["merged"]
		action: "merge-worktree"
		preconditions: [
			{
				category: "event"
				identity: "task-complete:{{instance}}:task:test"
			},
		]
	},
	{
		id:     "notify"
		in:     ["merged"]
		out:    ["done"]
		action: "notify-head"
	},
]
