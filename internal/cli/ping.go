package cli

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/chazu/procyon-park/internal/ipc"
	"github.com/spf13/cobra"
)

// pingCmd sends a system.ping to the daemon and reports the round-trip time.
var pingCmd = &cobra.Command{
	Use:   "ping",
	Short: "Health check the daemon via system.ping IPC",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := EnsureDaemon(); err != nil {
			return NewExitErr(ExitConnection, fmt.Errorf("daemon not reachable: %w", err))
		}

		start := time.Now()
		result, err := ipc.Call(SocketPath(), "system.ping", nil)
		rtt := time.Since(start)

		if err != nil {
			return NewExitErr(ExitConnection, fmt.Errorf("ping failed: %w", err))
		}

		w := cmd.OutOrStdout()
		if OutputJSON() {
			resp := map[string]interface{}{
				"status": "ok",
				"rtt_ms": rtt.Milliseconds(),
			}
			// Include daemon response if present.
			if result != nil {
				var daemonResp interface{}
				if json.Unmarshal(result, &daemonResp) == nil {
					resp["daemon"] = daemonResp
				}
			}
			data, _ := json.Marshal(resp)
			fmt.Fprintln(w, string(data))
		} else {
			fmt.Fprintf(w, "pong (%s)\n", rtt.Truncate(time.Millisecond))
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(pingCmd)
}
