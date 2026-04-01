// Agentic planning: research and decompose a work item into children.
// Agents use pp workitem create to populate the epic's children.
description: "Plan a work item: research, design, decompose into stories"
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
		id:          "research"
		in:          ["ready"]
		out:         ["researched"]
		role:        "scout"
		description: """
			Research the codebase for work item {{workitem}}.
			Read the work item: pp workitem show {{workitem}}
			Explore the codebase to understand current state, relevant files,
			and technical constraints. Write findings to a markdown document.
			"""
	},
	{
		id:          "decompose"
		in:          ["researched"]
		out:         ["decomposed"]
		role:        "planner"
		description: """
			Decompose work item {{workitem}} into implementation stories.
			Read the work item: pp workitem show {{workitem}}
			Read the scout's findings from observations.

			For each story, create a child work item:
			pp workitem create <identity> --title "Title" --type story \
			  --parent {{workitem}} --repo <repo> --wave N --template story|story-lite \
			  --description "Detailed implementation instructions..."

			Tag mechanical tasks (renames, config changes) with --template story-lite.
			Group small related tasks with --batch <name>.
			Include specific file paths, method names, and verification steps.
			"""
		preconditions: [
			{
				category: "event"
				identity: "task-complete:{{instance}}:task:research"
			},
		]
	},
	{
		id:     "notify"
		in:     ["decomposed"]
		out:    ["done"]
		action: "notify-head"
		preconditions: [
			{
				category: "event"
				identity: "task-complete:{{instance}}:task:decompose"
			},
		]
	},
]
