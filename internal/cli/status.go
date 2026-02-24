package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/chazu/procyon-park/internal/ipc"
	"github.com/chazu/procyon-park/internal/output"
	"github.com/spf13/cobra"
)

// statusCmd combines daemon status and agent list into a single overview.
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show system overview (daemon status + agent list)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := EnsureDaemon(); err != nil {
			return NewExitErr(ExitConnection, fmt.Errorf("daemon not reachable: %w", err))
		}

		sock := SocketPath()
		w := cmd.OutOrStdout()

		// Fetch daemon status.
		daemonResult, daemonErr := ipc.Call(sock, "system.status", nil)

		// Fetch agent list.
		agentResult, agentErr := ipc.Call(sock, "agent.list", nil)

		if OutputJSON() {
			resp := map[string]interface{}{}
			if daemonErr == nil && daemonResult != nil {
				var d interface{}
				json.Unmarshal(daemonResult, &d)
				resp["daemon"] = d
			} else if daemonErr != nil {
				resp["daemon_error"] = daemonErr.Error()
			}
			if agentErr == nil && agentResult != nil {
				var a interface{}
				json.Unmarshal(agentResult, &a)
				resp["agents"] = a
			} else if agentErr != nil {
				resp["agents_error"] = agentErr.Error()
			}
			data, _ := json.Marshal(resp)
			fmt.Fprintln(w, string(data))
			return nil
		}

		// Text/table mode: print sections.
		fmt.Fprintln(w, "=== Daemon ===")
		if daemonErr != nil {
			fmt.Fprintf(w, "  error: %s\n", daemonErr)
		} else {
			printDaemonStatus(w, daemonResult)
		}

		fmt.Fprintln(w)
		fmt.Fprintln(w, "=== Agents ===")
		if agentErr != nil {
			fmt.Fprintf(w, "  error: %s\n", agentErr)
		} else {
			printAgentList(w, agentResult)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

// printDaemonStatus renders daemon status for text/table mode.
func printDaemonStatus(w io.Writer, raw json.RawMessage) {
	var status map[string]interface{}
	if err := json.Unmarshal(raw, &status); err != nil {
		fmt.Fprintf(w, "  %s\n", string(raw))
		return
	}
	rec := output.NewRecord()
	for _, key := range []string{"status", "uptime", "pid", "version"} {
		if v, ok := status[key]; ok {
			rec.Set(key, v)
		}
	}
	if len(rec.Keys()) > 0 {
		f := output.NewFormatter(output.FormatTable)
		f.Format(w, []*output.Record{rec})
	} else {
		fmt.Fprintf(w, "  %s\n", string(raw))
	}
}

// printAgentList renders agent list for text/table mode.
func printAgentList(w io.Writer, raw json.RawMessage) {
	var agents []map[string]interface{}
	if err := json.Unmarshal(raw, &agents); err != nil {
		fmt.Fprintf(w, "  %s\n", string(raw))
		return
	}
	if len(agents) == 0 {
		fmt.Fprintln(w, "  (no agents)")
		return
	}
	var records []*output.Record
	for _, a := range agents {
		rec := output.NewRecord()
		for _, key := range []string{"name", "role", "status", "task", "uptime"} {
			if v, ok := a[key]; ok {
				rec.Set(key, v)
			}
		}
		records = append(records, rec)
	}
	f := output.NewFormatter(output.FormatTable)
	f.Format(w, records)
}
