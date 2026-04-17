// Feature design: idea → epic → stories+review → finalize
// Takes a feature description and produces a complete set of
// implementation-ready stories with dependencies.
description: "Feature design: research, design epic, decompose+review stories, finalize"
start_places: ["idea"]
terminal_places: ["ready"]

transitions: [
	{
		id:     "setup"
		in:     ["idea"]
		out:    ["researching"]
		action: "create-worktree"
	},
	{
		id:          "design"
		in:          ["researching"]
		out:         ["designed"]
		role:        "scout"
		description: """
			You are a feature designer. Your input: {{description}}

			1. Research the codebase thoroughly to understand current architecture,
			   conventions, and related existing functionality.
			2. Design the feature from a USER-CENTRIC perspective — what does the
			   user experience? What are the entry points? What changes are visible?
			3. Write an epic document (as a markdown file in docs/plans/) containing:
			   - Summary: what the feature does and why
			   - User stories: "As a <role>, I want <goal>, so that <benefit>"
			   - Acceptance criteria: concrete, testable conditions for done
			   - Technical context: relevant files, classes, and patterns discovered
			   - Open questions: anything ambiguous or needing clarification
			4. Report the epic file path via pp observe.
			"""
	},
	{
		id:          "review"
		in:          ["designed"]
		out:         ["reviewed"]
		role:        "reviewer"
		description: """
			Combined design review + story decomposition + technical feasibility.
			Read the epic document (check pp read observation for its file path).

			STEP 1 — Evaluate the design:
			- Are the user stories clear and complete?
			- Are acceptance criteria specific and testable?
			- Does the design account for edge cases?
			- Is the scope appropriate (not too large, not too small)?
			- Are there missing user flows or overlooked stakeholders?

			If the design needs significant rework, write a verdict signal with
			decision "redesign" and explain what needs to change in the rationale.
			STOP here in that case — the designer will iterate.

			STEP 2 — Decompose into orthogonal implementation stories. For each:
			- A clear title
			- Specific implementation instructions (files to modify, methods to add)
			- Documentation updates needed (if any)
			- Test requirements (what to test, how to verify)
			- Estimated complexity (small/medium/large)

			Stories must be orthogonal — no two stories should modify the same code
			for the same reason. Prefer many small stories over few large ones.

			STEP 3 — Technical feasibility check for each story:
			- The specified files and methods actually exist in the codebase
			- The implementation approach is technically sound
			- The story is appropriately scoped (decompose further if too large)
			- No two stories overlap in the code they modify
			- Test requirements are achievable

			Fix any minor issues (wrong file paths, missing edge cases, scope
			adjustments) directly as you go — do not bounce back for small fixes.

			Write the final stories as a plan decision via pp decide.
			Write verdict signal with decision "pass" when done, or "redesign" if
			the whole epic needs rework.
			"""
		preconditions: [
			{
				category: "event"
				identity: "task-complete:{{instance}}:task:design"
			},
		]
	},
	{
		id:  "redesign"
		in:  ["reviewed"]
		out: ["researching"]
		preconditions: [
			{
				category:   "signal"
				identity:   "verdict:{{instance}}"
				constraint: "{decision: \"redesign\"}"
			},
		]
	},
	{
		id:  "stories-approved"
		in:  ["reviewed"]
		out: ["finalizing"]
		preconditions: [
			{
				category:   "signal"
				identity:   "verdict:{{instance}}"
				constraint: "{decision: \"pass\"}"
			},
		]
	},
	{
		id:          "finalize"
		in:          ["finalizing"]
		out:         ["finalized"]
		role:        "planner"
		description: """
			Final review and dependency mapping for the approved stories.
			Read the plan decision with the stories (pp read decision).

			1. Add explicit dependencies between stories:
			   - Which stories must complete before others can start?
			   - Which stories can run in parallel?
			   - Group into waves: wave 1 (no deps), wave 2 (depends on wave 1), etc.

			2. Final feasibility check:
			   - Read the actual source files referenced by each story
			   - Verify line numbers, method names, and class structures are current
			   - Check that the stories collectively cover all acceptance criteria
			     from the original epic

			3. Look for omissions:
			   - Missing error handling stories?
			   - Missing documentation updates?
			   - Missing test coverage?
			   - Anything in the epic's acceptance criteria not addressed by a story?

			4. Write the final plan as a decision via pp decide with:
			   - All stories with dependencies and wave assignments
			   - A summary of what was changed from the previous version
			   - Any risks or caveats for the implementing team

			5. Report completion via pp observe with the plan identity.
			"""
		preconditions: [
			{
				category: "event"
				identity: "task-complete:{{instance}}:task:review"
			},
		]
	},
	{
		id:     "notify"
		in:     ["finalized"]
		out:    ["ready"]
		action: "notify-head"
		preconditions: [
			{
				category: "event"
				identity: "task-complete:{{instance}}:task:finalize"
			},
		]
	},
]
