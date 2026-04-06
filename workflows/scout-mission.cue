// Scout mission: research a topic, write findings document
// Merges findings into the source branch before cleanup.
description: "Scout mission: research a topic, write findings document"
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
		id:     "merge"
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
]
