// Spike: time-boxed exploration with a throwaway prototype.
//
// The agent gets a real worktree and CAN write code — the goal is to
// *learn*, not to ship. Unlike scout-mission (pure research) or story
// (merge on success), a spike's output is a DESIGN DECISION recorded
// via `pp decide`. The prototype code is discarded after the decision
// lands; the worktree and impl branch are removed, nothing is merged.
//
// Use this when:
//   - You need to prove a technical approach works before committing
//     to a full story.
//   - The best way to answer an architectural question is to try it.
//   - Research alone (scout-mission) isn't enough — you need to run code.
description: "Spike: time-boxed prototype exploration; output is a decision, code is discarded"
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
		id:   "spike"
		in:   ["ready"]
		out:  ["spiking"]
		role: "scout"
		description: """
			You are running a time-boxed SPIKE. Your input: {{description}}

			GROUND RULES:
			- You have a real worktree and can write prototype code freely.
			- The code you write will be DISCARDED at the end of this spike —
			  do not polish it, do not worry about style, do not write tests
			  unless a test is the fastest way to learn what you need to learn.
			- Your actual deliverable is a DESIGN DECISION, not code.

			PROCESS:
			1. Clarify the question the spike must answer. State it explicitly
			   as a single sentence in a pp observe before you start coding.
			2. Explore: write the smallest prototype that could answer the
			   question. Iterate quickly. It is fine — expected — for the
			   prototype to be ugly, incomplete, or even to fail. A failed
			   spike that teaches you the approach won't work is a success.
			3. Keep notes as you go via pp observe — especially dead ends,
			   surprising findings, and constraints you discovered.
			4. When you have enough evidence to answer the question (or to
			   conclude it cannot be answered cheaply), STOP coding.
			5. Record your conclusion via `pp decide`:
			     pp decide spike:{{instance}} "<one-line summary of the decision>" \\
			       --rationale "<what you tried, what worked, what didn't, \\
			                     and the recommended direction for real work>"
			   The decision should stand on its own — downstream readers will
			   see the decision but NOT your prototype code.
			6. If the spike is inconclusive, still record a decision with that
			   verdict (e.g. "approach-X viable but needs Y investigated first")
			   so the next step is clear.

			REMEMBER: the worktree is about to be destroyed. Anything worth
			keeping MUST be captured in your decision's detail/rationale or
			in pp observations — not in the code.
			"""
	},
	{
		id:  "complete"
		in:  ["spiking"]
		out: ["discarding"]
		preconditions: [
			{
				category: "event"
				identity: "task-complete:{{instance}}:task:spike"
			},
		]
	},
	{
		id:     "discard"
		in:     ["discarding"]
		out:    ["discarded"]
		action: "discard-worktree"
	},
	{
		id:     "notify"
		in:     ["discarded"]
		out:    ["done"]
		action: "notify-head"
	},
]
