// Agentic review: review and refine a work item and its children.
// The reviewer can add comments, edit descriptions, decompose, flag blockers.
description: "Review a work item: check accuracy, refine stories, flag issues"
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
		id:          "review"
		in:          ["ready"]
		out:         ["reviewed"]
		role:        "reviewer"
		description: """
			Review work item {{workitem}} and its children.

			1. Read the work item: pp workitem show {{workitem}}
			2. List children: pp workitem children {{workitem}}
			3. For each child, read its detail: pp workitem show <child-id>

			For each child story, verify:
			- Description references real files and methods in the codebase
			- Implementation approach is technically sound
			- Scope is appropriate (decompose if too large)
			- No overlap with other stories

			You CAN:
			- Add comments: pp workitem comment <id> "feedback"
			- Edit descriptions: pp workitem update <id> --description "corrected"
			- Create sub-tasks: pp workitem create <id> --parent <story> --type story
			- Flag blockers: pp workitem block <id> --reason "X doesn't exist"
			- Adjust wave/template: pp workitem update <id> --wave 2 --template story-lite

			You MUST NOT: Edit source code files.
			"""
	},
	{
		id:     "notify"
		in:     ["reviewed"]
		out:    ["done"]
		action: "notify-head"
		preconditions: [
			{
				category: "event"
				identity: "task-complete:{{instance}}:task:review"
			},
		]
	},
]
