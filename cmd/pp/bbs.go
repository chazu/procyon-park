// bbs.go implements the 'pp bbs' subcommands for interacting with the BBS tuplespace.
// Commands: out, in, rd, scan, pulse, seed-available.
// All commands communicate with the daemon via JSON-RPC over Unix socket.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/chazu/procyon-park/internal/cli"
	"github.com/chazu/procyon-park/internal/ipc"
	"github.com/chazu/procyon-park/internal/output"
	"github.com/spf13/cobra"
)

// bbsCmd is the parent 'pp bbs' command.
var bbsCmd = &cobra.Command{
	Use:   "bbs",
	Short: "Interact with the BBS tuplespace",
	Long: `BBS (Bulletin Board System) tuplespace commands.

Commands:
  out              Write a tuple to the tuplespace
  in               Atomically read and remove a matching tuple (blocking)
  rd               Read a matching tuple without removing (non-blocking)
  scan             List all matching tuples (non-blocking)
  pulse            Check for and drain pending notifications
  seed-available   Populate available tuples for a scope

Wildcard: Use "?" for any positional argument to match any value.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

// --- flags shared across subcommands ---

var (
	bbsAgentID  string
	bbsInstance string
	bbsTimeout  string
)

// --- pp bbs out ---

var bbsOutCmd = &cobra.Command{
	Use:   "out <category> <scope> <identity> [payload-json]",
	Short: "Write a tuple to the tuplespace",
	Long: `Write a tuple with the given category, scope, identity, and optional JSON payload.

Examples:
  pp bbs out fact myrepo "health-ok" '{"status":"green"}'
  pp bbs out claim myrepo task-1 '{"agent":"Widget"}' --agent-id Widget
  pp bbs out available myrepo task-1`,
	Args: cobra.RangeArgs(3, 4),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := cli.EnsureDaemon(); err != nil {
			return cli.NewExitErr(cli.ExitConnection, err)
		}

		payload := "{}"
		if len(args) == 4 {
			payload = args[3]
		}

		lifecycle, _ := cmd.Flags().GetString("lifecycle")
		taskID, _ := cmd.Flags().GetString("task-id")
		ttl, _ := cmd.Flags().GetInt("ttl")

		params := map[string]interface{}{
			"category":  args[0],
			"scope":     args[1],
			"identity":  args[2],
			"payload":   payload,
			"lifecycle": lifecycle,
		}
		if bbsInstance != "" {
			params["instance"] = bbsInstance
		}
		if bbsAgentID != "" {
			params["agent_id"] = bbsAgentID
		}
		if taskID != "" {
			params["task_id"] = taskID
		}
		if ttl > 0 {
			params["ttl"] = ttl
		}

		result, err := ipc.Call(cli.SocketPath(), "tuple.write", params)
		if err != nil {
			return cli.NewExitErr(cli.ExitError, fmt.Errorf("tuple.write: %w", err))
		}

		if cli.OutputJSON() {
			fmt.Println(string(result))
		} else {
			var res struct {
				ID int64 `json:"id"`
			}
			if err := json.Unmarshal(result, &res); err == nil {
				fmt.Fprintf(os.Stdout, "tuple %d written\n", res.ID)
			} else {
				fmt.Println(string(result))
			}
		}

		bbsPiggybackNotifications()
		return nil
	},
}

// --- pp bbs in ---

var bbsInCmd = &cobra.Command{
	Use:   "in <category> [scope] [identity]",
	Short: "Atomically read and remove a matching tuple (blocking)",
	Long: `Blocking take: waits for a matching tuple, removes it, and returns it.
Use "?" for scope or identity to match any value.
Returns exit code 4 if the timeout expires with no match.

Examples:
  pp bbs in available myrepo task-1 --timeout 5s
  pp bbs in notification Widget ? --timeout 10s`,
	Args: cobra.RangeArgs(1, 3),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := cli.EnsureDaemon(); err != nil {
			return cli.NewExitErr(cli.ExitConnection, err)
		}

		params := buildPatternParams(args)

		timeout, err := time.ParseDuration(bbsTimeout)
		if err != nil {
			return cli.NewExitErr(cli.ExitUsage, fmt.Errorf("invalid --timeout: %w", err))
		}

		deadline := time.Now().Add(timeout)
		pollInterval := 100 * time.Millisecond

		for {
			result, err := ipc.Call(cli.SocketPath(), "tuple.take", params)
			if err != nil {
				return cli.NewExitErr(cli.ExitError, fmt.Errorf("tuple.take: %w", err))
			}

			if string(result) != "null" && len(result) > 0 {
				printTupleResult(result)
				bbsPiggybackNotifications()
				return nil
			}

			if time.Now().Add(pollInterval).After(deadline) {
				bbsPiggybackNotifications()
				return cli.NewExitErr(cli.ExitTimeout, fmt.Errorf("timeout waiting for matching tuple"))
			}
			time.Sleep(pollInterval)
		}
	},
}

// --- pp bbs rd ---

var bbsRdCmd = &cobra.Command{
	Use:   "rd <category> [scope] [identity]",
	Short: "Read a matching tuple without removing (non-blocking)",
	Long: `Non-destructive read: finds the oldest matching tuple and returns it.
Use "?" for scope or identity to match any value.
Returns null/empty if no match found.

Examples:
  pp bbs rd claim myrepo task-1
  pp bbs rd fact myrepo ?`,
	Args: cobra.RangeArgs(1, 3),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := cli.EnsureDaemon(); err != nil {
			return cli.NewExitErr(cli.ExitConnection, err)
		}

		params := buildPatternParams(args)

		result, err := ipc.Call(cli.SocketPath(), "tuple.read", params)
		if err != nil {
			return cli.NewExitErr(cli.ExitError, fmt.Errorf("tuple.read: %w", err))
		}

		printTupleResult(result)
		bbsPiggybackNotifications()
		return nil
	},
}

// --- pp bbs scan ---

var bbsScanCmd = &cobra.Command{
	Use:   "scan [category] [scope] [identity]",
	Short: "List all matching tuples (non-blocking)",
	Long: `Scan returns all tuples matching the pattern. Non-destructive.
Use "?" for any positional argument to match any value.
All arguments are optional — omitting them matches everything.

Examples:
  pp bbs scan claim myrepo
  pp bbs scan ? myrepo
  pp bbs scan fact`,
	Args: cobra.RangeArgs(0, 3),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := cli.EnsureDaemon(); err != nil {
			return cli.NewExitErr(cli.ExitConnection, err)
		}

		params := buildPatternParams(args)

		result, err := ipc.Call(cli.SocketPath(), "tuple.scan", params)
		if err != nil {
			return cli.NewExitErr(cli.ExitError, fmt.Errorf("tuple.scan: %w", err))
		}

		if cli.OutputJSON() {
			fmt.Println(string(result))
		} else {
			var rows []map[string]interface{}
			if err := json.Unmarshal(result, &rows); err != nil {
				fmt.Println(string(result))
			} else {
				printTupleTable(rows)
			}
		}

		bbsPiggybackNotifications()
		return nil
	},
}

// --- pp bbs pulse ---

var bbsPulseCmd = &cobra.Command{
	Use:   "pulse",
	Short: "Check for and drain pending notifications",
	Long: `Drains all pending notification tuples for the given agent.
Notifications are printed to stderr.

The --agent-id flag is required for this command.

Examples:
  pp bbs pulse --agent-id Widget`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if bbsAgentID == "" {
			return cli.NewExitErr(cli.ExitUsage, fmt.Errorf("--agent-id is required for pulse"))
		}

		if err := cli.EnsureDaemon(); err != nil {
			return cli.NewExitErr(cli.ExitConnection, err)
		}

		notifications, err := drainNotifications(cli.SocketPath(), bbsAgentID)
		if err != nil {
			return cli.NewExitErr(cli.ExitError, err)
		}

		if cli.OutputJSON() {
			data, _ := json.Marshal(notifications)
			fmt.Println(string(data))
		} else if len(notifications) == 0 {
			if !cli.Quiet() {
				fmt.Println("no pending notifications")
			}
		} else {
			for _, n := range notifications {
				identity, _ := n["identity"].(string)
				payload, _ := n["payload"].(string)
				output.WriteNotification(os.Stderr, fmt.Sprintf("%s: %s", identity, payload))
			}
		}
		return nil
	},
}

// --- pp bbs seed-available ---

var bbsSeedAvailableCmd = &cobra.Command{
	Use:   "seed-available <scope> [task-id...]",
	Short: "Populate available tuples for a scope",
	Long: `Creates an "available" tuple for each task ID.
Task IDs can be provided as positional arguments after the scope.
If no task IDs are provided, reads them from stdin (one per line).

Examples:
  pp bbs seed-available myrepo task-1 task-2 task-3
  bd ready --ids | pp bbs seed-available myrepo`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := cli.EnsureDaemon(); err != nil {
			return cli.NewExitErr(cli.ExitConnection, err)
		}

		scope := args[0]
		taskIDs := args[1:]

		// If no task IDs provided as args, read from stdin.
		if len(taskIDs) == 0 {
			taskIDs = readLinesFromStdin()
			if len(taskIDs) == 0 {
				return cli.NewExitErr(cli.ExitUsage, fmt.Errorf("no task IDs provided"))
			}
		}

		seeded := 0
		for _, taskID := range taskIDs {
			if taskID == "" {
				continue
			}
			params := map[string]interface{}{
				"category": "available",
				"scope":    scope,
				"identity": taskID,
				"payload":  "{}",
			}
			_, err := ipc.Call(cli.SocketPath(), "tuple.write", params)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to seed %s: %v\n", taskID, err)
				continue
			}
			seeded++
		}

		if cli.OutputJSON() {
			data, _ := json.Marshal(map[string]interface{}{"seeded": seeded, "scope": scope})
			fmt.Println(string(data))
		} else {
			fmt.Fprintf(os.Stdout, "%d available tuples seeded for scope %q\n", seeded, scope)
		}

		bbsPiggybackNotifications()
		return nil
	},
}

// --- init: register subcommands and flags ---

func init() {
	// Global BBS flags (shared across subcommands via persistent flags on parent).
	bbsCmd.PersistentFlags().StringVar(&bbsAgentID, "agent-id", "", "agent ID for notification piggybacking")
	bbsCmd.PersistentFlags().StringVar(&bbsInstance, "instance", "", "tuple instance (default: server-assigned)")

	// out-specific flags
	bbsOutCmd.Flags().String("lifecycle", "session", "tuple lifecycle: furniture, session, or ephemeral")
	bbsOutCmd.Flags().String("task-id", "", "associated task ID")
	bbsOutCmd.Flags().Int("ttl", 0, "time-to-live in seconds (0 = no expiry)")

	// in-specific flags
	bbsInCmd.Flags().StringVar(&bbsTimeout, "timeout", "30s", "max wait duration (e.g., 5s, 1m)")

	// Register subcommands.
	bbsCmd.AddCommand(bbsOutCmd)
	bbsCmd.AddCommand(bbsInCmd)
	bbsCmd.AddCommand(bbsRdCmd)
	bbsCmd.AddCommand(bbsScanCmd)
	bbsCmd.AddCommand(bbsPulseCmd)
	bbsCmd.AddCommand(bbsSeedAvailableCmd)
}

// --- helpers ---

// wildcardToPtr returns nil for "?" (wildcard) or a pointer to the string.
func wildcardToPtr(s string) *string {
	if s == "?" {
		return nil
	}
	return &s
}

// buildPatternParams builds a patternParams map from positional args.
// Supports 0-3 args: [category] [scope] [identity]. "?" means wildcard (nil).
func buildPatternParams(args []string) map[string]*string {
	params := map[string]*string{}
	if len(args) >= 1 {
		params["category"] = wildcardToPtr(args[0])
	}
	if len(args) >= 2 {
		params["scope"] = wildcardToPtr(args[1])
	}
	if len(args) >= 3 {
		params["identity"] = wildcardToPtr(args[2])
	}
	return params
}

// printTupleResult prints a single tuple result (from read/take).
func printTupleResult(result json.RawMessage) {
	if string(result) == "null" || len(result) == 0 {
		if cli.OutputJSON() {
			fmt.Println("null")
		}
		return
	}

	if cli.OutputJSON() {
		fmt.Println(string(result))
		return
	}

	var row map[string]interface{}
	if err := json.Unmarshal(result, &row); err != nil {
		fmt.Println(string(result))
		return
	}

	// Pretty-print the tuple as indented JSON for text mode.
	data, err := json.MarshalIndent(row, "", "  ")
	if err != nil {
		fmt.Println(string(result))
		return
	}
	fmt.Println(string(data))
}

// printTupleTable prints tuples in table format using the output package.
func printTupleTable(rows []map[string]interface{}) {
	if len(rows) == 0 {
		if !cli.Quiet() {
			fmt.Println("no matching tuples")
		}
		return
	}

	records := make([]*output.Record, 0, len(rows))
	for _, row := range rows {
		rec := output.NewRecord()
		rec.Set("id", row["id"])
		rec.Set("category", row["category"])
		rec.Set("scope", row["scope"])
		rec.Set("identity", row["identity"])
		rec.Set("instance", row["instance"])
		rec.Set("payload", row["payload"])
		rec.Set("lifecycle", row["lifecycle"])
		if agentID, ok := row["agent_id"]; ok && agentID != nil {
			rec.Set("agent_id", agentID)
		}
		records = append(records, rec)
	}

	f := output.Format("table")
	if cli.NoColor() {
		f = output.FormatTable
	}
	formatter := output.NewFormatter(f)
	formatter.Format(os.Stdout, records) //nolint:errcheck
}

// bbsPiggybackNotifications checks for pending notifications if --agent-id is set.
// Notifications are printed to stderr as a side effect.
func bbsPiggybackNotifications() {
	if bbsAgentID == "" {
		return
	}
	notifications, err := drainNotifications(cli.SocketPath(), bbsAgentID)
	if err != nil {
		return // silently ignore piggyback errors
	}
	for _, n := range notifications {
		identity, _ := n["identity"].(string)
		payload, _ := n["payload"].(string)
		output.WriteNotification(os.Stderr, fmt.Sprintf("%s: %s", identity, payload))
	}
}

// drainNotifications calls tuple.pulse to drain and return notifications for an agent.
func drainNotifications(socketPath, agentID string) ([]map[string]interface{}, error) {
	params := map[string]string{"agent_id": agentID}
	result, err := ipc.Call(socketPath, "tuple.pulse", params)
	if err != nil {
		return nil, fmt.Errorf("tuple.pulse: %w", err)
	}

	var notifications []map[string]interface{}
	if err := json.Unmarshal(result, &notifications); err != nil {
		return nil, fmt.Errorf("unmarshal pulse result: %w", err)
	}
	return notifications, nil
}

// readLinesFromStdin reads non-empty lines from stdin.
func readLinesFromStdin() []string {
	var lines []string
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
