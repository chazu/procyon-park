// Package prime generates agent priming content: static BBS protocol references
// and dynamic tuplespace context snapshots. These are appended to role-specific
// instructions so agents understand how to coordinate via the tuplespace.
package prime

import (
	"fmt"
	"strings"
)

// AgentPromptAddendum returns a static BBS protocol reference block parameterized
// with the given scope and taskID. This block tells agents how to use the tuplespace:
// categories, scopes, identities, atomic claiming, trail-leaving, completion protocol,
// pulse cadence, notification piggybacking, and the full pp bbs CLI reference.
func AgentPromptAddendum(scope, taskID string) string {
	var b strings.Builder

	b.WriteString("BBS TUPLESPACE PROTOCOL:\n")
	b.WriteString("You communicate via a shared tuplespace (bulletin board) instead of mail.\n")
	b.WriteString("Tuples have the form (category, scope, identity, payload).\n")
	b.WriteString("\n")

	// Pre-read orientation
	b.WriteString("BEFORE STARTING:\n")
	fmt.Fprintf(&b, "  pp bbs scan fact %s\n", scope)
	fmt.Fprintf(&b, "  pp bbs scan convention %s\n", scope)
	b.WriteString("Read all fact and convention tuples to understand the current project state.\n")
	b.WriteString("Note: furniture tuples from the tuplespace have been pre-read and included above\n")
	b.WriteString("in \"BBS TUPLESPACE CONTEXT\" — you do not need to scan again unless you want fresh data.\n")
	b.WriteString("\n")

	// Atomic claiming protocol
	b.WriteString("ATOMIC CLAIMING:\n")
	b.WriteString("Before doing any work, you MUST atomically claim your task. This prevents\n")
	b.WriteString("duplicate work when multiple cubs are spawned simultaneously.\n")
	fmt.Fprintf(&b, "  1. Consume the available tuple (atomic — only one cub succeeds):\n")
	fmt.Fprintf(&b, "     pp bbs in available %s %s --timeout 5s\n", scope, taskID)
	b.WriteString("  2. If successful, write the advisory claim tuple:\n")
	fmt.Fprintf(&b, "     pp bbs out claim %s %s '{\"agent\":\"$PP_AGENT_NAME\",\"status\":\"in_progress\"}'\n", scope, taskID)
	fmt.Fprintf(&b, "     bd update %s --status=in_progress\n", taskID)
	b.WriteString("  3. If the 'in' command fails (timeout), the task was claimed by another cub.\n")
	b.WriteString("     Post an obstacle and proceed to wind-down.\n")
	b.WriteString("\n")

	// Trail-leaving while working
	b.WriteString("WHILE WORKING:\n")
	fmt.Fprintf(&b, "  pp bbs out obstacle %s <desc> '{\"task\":\"%s\",\"detail\":\"...\"}'\n", scope, taskID)
	fmt.Fprintf(&b, "  pp bbs out need %s <desc> '{\"task\":\"%s\",\"detail\":\"...\"}'\n", scope, taskID)
	fmt.Fprintf(&b, "  pp bbs out artifact %s <path> '{\"task\":\"%s\",\"type\":\"file\"}'\n", scope, taskID)
	b.WriteString("Write obstacle/need tuples when blocked, artifact tuples for outputs.\n")
	b.WriteString("\n")

	// Completion protocol
	b.WriteString("ON COMPLETION (mandatory — do NOT skip):\n")
	fmt.Fprintf(&b, "  pp bbs out event %s task_done '{\"task\":\"%s\",\"agent\":\"$PP_AGENT_NAME\",\"branch\":\"<your-branch>\"}'\n", scope, taskID)
	b.WriteString("ALWAYS write this event tuple. This is how the king knows you are done.\n")
	b.WriteString("If you skip this, your work will sit unmerged and unreviewed indefinitely.\n")
	b.WriteString("\n")

	// Pulse cadence
	b.WriteString("PULSE CADENCE:\n")
	b.WriteString("Call 'pp bbs pulse --agent-id $PP_AGENT_NAME' at natural breakpoints in your work:\n")
	b.WriteString("  - Before starting a subtask\n")
	b.WriteString("  - After completing a subtask\n")
	b.WriteString("  - When switching between files or phases of work\n")
	b.WriteString("  - Before and after running tests\n")
	b.WriteString("This returns any pending notifications from other agents or the king (e.g., new\n")
	b.WriteString("instructions, priority changes, coordination signals). Pulse is lightweight — it\n")
	b.WriteString("costs one round-trip and returns immediately.\n")
	b.WriteString("\n")

	// Notification piggybacking
	b.WriteString("NOTIFICATION PIGGYBACKING:\n")
	b.WriteString("Every BBS command (out, in, rd, scan) automatically checks for your notifications\n")
	b.WriteString("when you include --agent-id $PP_AGENT_NAME. Notifications are printed to stderr\n")
	b.WriteString("after the command output. This means you receive notifications passively as a\n")
	b.WriteString("side-effect of normal tuplespace operations. Use 'pp bbs pulse' when you haven't\n")
	b.WriteString("made a BBS call recently and want to check for messages.\n")
	b.WriteString("\n")

	// Full CLI reference
	b.WriteString("BBS CLI REFERENCE:\n")
	b.WriteString("  pp bbs out              <category> <scope> <identity> [payload-json]   Write a tuple\n")
	b.WriteString("  pp bbs in               <category> [scope] [identity]                  Read and remove a tuple (blocks until match)\n")
	b.WriteString("  pp bbs rd               <category> [scope] [identity]                  Read a tuple without removing (blocks until match)\n")
	b.WriteString("  pp bbs scan             [category] [scope] [identity]                  List all matching tuples (non-blocking)\n")
	b.WriteString("  pp bbs pulse            --agent-id <name>                              Check for pending notifications\n")
	fmt.Fprintf(&b, "  pp bbs seed-available   <scope>                                        Populate available tuples from bd ready\n")
	b.WriteString("All commands accept --agent-id $PP_AGENT_NAME to enable notification piggybacking.\n")
	b.WriteString("Use \"?\" for any positional arg to wildcard it (e.g., pp bbs scan ? ")
	fmt.Fprintf(&b, "%s to match any category in this scope).\n", scope)

	return b.String()
}
