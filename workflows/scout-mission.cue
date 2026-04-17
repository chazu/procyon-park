// Scout mission: research a topic, write findings document
// Merges findings into the source branch before cleanup.
description: "Scout mission: research a topic, write findings document"
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
		id:          "scout"
		in:          ["ready"]
		out:         ["scouting"]
		role:        "scout"
		description: "{{description}}"
	},
	{
		id:  "complete"
		in:  ["scouting"]
		out: ["merging"]
		preconditions: [
			{
				category: "event"
				identity: "task-complete:{{instance}}:task:scout"
			},
		]
	},
	{
		id:  "merge"
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
]
