// Story-lite: implement in worktree, merge directly (no review cycle)
// Use for mechanical changes: renames, config updates, adding tests.
description: "Lite story: implement in worktree, merge directly"
start_places: ["request"]
terminal_places: ["done"]

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
		id:  "integrate"
		in:  ["implemented"]
		out: ["merged"]
		_worktree_bookend.merge
		preconditions: [
			{
				category: "event"
				identity: "task-complete:{{instance}}:task:implement"
			},
		]
	},
	{
		id:  "notify"
		in:  ["merged"]
		out: ["done"]
		_notify_on_complete
	},
]
