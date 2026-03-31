// Scout mission: research a topic, write findings document
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
		out: ["done"]
		preconditions: [
			{
				category: "event"
				identity: "task-complete:{{instance}}:task:scout"
			},
		]
	},
]
