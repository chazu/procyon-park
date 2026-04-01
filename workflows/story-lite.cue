// Story-lite: implement in worktree, merge directly (no review cycle)
// Use for mechanical changes: renames, config updates, adding tests.
description: "Lite story: implement in worktree, merge directly"
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
		id:     "integrate"
		in:     ["implemented"]
		out:    ["merged"]
		action: "merge-worktree"
		preconditions: [
			{
				category: "event"
				identity: "task-complete:{{instance}}:task:implement"
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
