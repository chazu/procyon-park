// Mission brief: brief -> intent -> research -> review -> plan.
//
// The high-level Auftragstaktik intake flow. Takes an operator's free-text
// brief and matures it into a human-reviewable PLAN, writing both the
// structured intent and the final plan back into the mission tuple so the
// dashboard can render them without touching the filesystem.
//
// Threaded params (supplied by `pp mission start`, MissionCLI):
//   {{description}} — the operator's raw brief
//   {{mission}}     — the mission tuple id (msn-<hex>) the agents write back to
//   {{repo}}        — the BBS storage scope
//   {{instance}}    — the workflow instance id (engine-provided)
//
// Roles are REUSED, not invented: planner (interpret-intent + synthesize-plan),
// scout (research), reviewer (adversarial review). All mission-specific guidance
// lives in the transition `description` prompts.
//
// The workflow TERMINATES once the plan is written and the mission flipped to
// 'awaiting-approval'. It does NOT park on a gate or hold the worktree —
// approval is a separate human action (`pp mission approve|reject`, W1 CLI / W3 UI).
description: "Mission brief: interpret intent, research, adversarial review, synthesize a human-reviewable plan"

start_places: ["brief"]
terminal_places: ["done"]

transitions: [
	{
		id:     "setup"
		in:     ["brief"]
		out:    ["intent-pending"]
		action: "create-worktree"
	},
	{
		id:          "interpret-intent"
		in:          ["intent-pending"]
		out:         ["intent-done"]
		role:        "planner"
		description: """
			You are interpreting an operator's mission brief into STRUCTURED INTENT.

			The brief: {{description}}

			Extract, in this order:
			  1. OBJECTIVE — one or two sentences: what outcome the operator wants and why.
			  2. SUCCESS CRITERIA — concrete, checkable conditions for "done". Each must be
			     observable/verifiable (a test passes, an endpoint returns X, a UI shows Y).
			     Avoid vague criteria like "works well".
			  3. CONSTRAINTS — hard requirements the solution must honor (compatibility,
			     conventions, performance, security, scope-of-touch).
			  4. OUT-OF-SCOPE — what this mission explicitly does NOT cover, to prevent
			     scope creep. Be specific about tempting-but-excluded adjacent work.

			Write the intent as MARKDOWN into the mission tuple's payload.intent:

			    pp mission set-intent {{mission}} "<intent markdown>" --repo {{repo}}

			(This is a read-modify-write — it preserves the brief and status.)

			Then record a one-paragraph summary of the intent via:

			    pp observe mission-intent:{{instance}} "<summary>"

			Do NOT design a solution yet — only clarify intent.
			"""
	},
	{
		id:          "research"
		in:          ["intent-done"]
		out:         ["researched"]
		role:        "scout"
		description: """
			You are researching the codebase to ground the mission plan.

			Read the structured intent first:
			    pp mission show {{mission}} --repo {{repo}}
			(also check observations: pp read observation {{repo}})

			Investigate the questions the intent raises:
			  - Which files, modules, classes, and conventions are relevant?
			  - What already exists that the mission can reuse or must integrate with?
			  - What are the technical constraints, risks, and unknowns?
			  - Are any of the success criteria harder than they look? Why?

			If the brief is broad, you MAY fan out — but a single thorough scout is fine.
			Write your findings via:

			    pp observe mission-research:{{instance}} "<findings: files, constraints, risks, open questions>"

			Report concrete file paths and method names — not generalities.
			"""
		preconditions: [
			{
				category: "event"
				identity: "task-complete:{{instance}}:task:interpret-intent"
			},
		]
	},
	{
		id:          "review"
		in:          ["researched"]
		out:         ["reviewed"]
		role:        "reviewer"
		description: """
			ADVERSARIALLY review the collected research for the mission.

			Read the intent and the scout's findings:
			    pp mission show {{mission}} --repo {{repo}}
			    pp read observation {{repo}}

			Stress-test the research — do NOT rubber-stamp:
			  - Is it SOUND? Are the cited files/methods/constraints actually correct?
			  - Is it COMPLETE? What questions raised by the intent went unanswered?
			  - Are there GAPS — unexplored modules, missing integration points, ignored
			    failure modes, untested assumptions?
			  - Are any success criteria un-addressed by the research?
			  - Are there WRONG ASSUMPTIONS that would mislead the plan?

			Emit your verdict as an observation:

			    pp observe mission-review:{{instance}} "<verdict: sound/gaps/wrong-assumptions + what the planner must account for>"

			Be specific and concrete — this verdict steers the plan.
			"""
		preconditions: [
			{
				category: "event"
				identity: "task-complete:{{instance}}:task:research"
			},
		]
	},
	{
		id:          "synthesize-plan"
		in:          ["reviewed"]
		out:         ["planned"]
		role:        "planner"
		description: """
			Synthesize the MISSION PLAN — the human-reviewable deliverable of this mission.

			Read everything produced so far:
			    pp mission show {{mission}} --repo {{repo}}
			    pp read observation {{repo}}
			(intent, the scout's research, and the reviewer's adversarial verdict.)

			Write a MARKDOWN plan with these sections, in order:
			  # Mission Plan
			  ## Intent            — objective restated from the structured intent.
			  ## Success Criteria  — the concrete checkable criteria (carry from intent).
			  ## Research Synthesis — what the research + review established; fold in the
			                          reviewer's gaps/corrections, not just the raw findings.
			  ## Proposed Approach — the recommended path at a HIGH LEVEL.
			  ## Work Breakdown    — epics / themes only (NOT concrete work items or stories).
			  ## Risks             — what could go wrong + mitigations.
			  ## Definition of Done — when the mission is complete.

			Store the plan in the mission tuple AND flip the mission to awaiting-approval:

			    pp mission set-plan {{mission}} "<plan markdown>" --repo {{repo}}

			(set-plan is a read-modify-write that writes payload.plan and sets
			 status='awaiting-approval' in one step.)

			OPTIONALLY also write the same plan to docs/plans/<mission>-plan.md and
			commit it, so the plan lands in git history when the worktree is merged.

			Do NOT wait for approval — approval is a separate human action. Once the
			plan is stored and the status is awaiting-approval, your task is complete.
			"""
		preconditions: [
			{
				category: "event"
				identity: "task-complete:{{instance}}:task:review"
			},
		]
	},
	{
		id:     "merge"
		in:     ["planned"]
		out:    ["merged"]
		action: "merge-worktree"
		preconditions: [
			{
				category: "event"
				identity: "task-complete:{{instance}}:task:synthesize-plan"
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
