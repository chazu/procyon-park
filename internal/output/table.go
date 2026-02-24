package output

import (
	"fmt"
	"io"
	"strings"
)

// TableFormatter renders records as fixed-width aligned columns.
// When Color is true, status fields are colorized for TTY output.
type TableFormatter struct {
	Color bool
}

// statusColors maps common status values to ANSI color codes.
var statusColors = map[string]string{
	"active":      "\033[32m", // green
	"running":     "\033[32m", // green
	"in_progress": "\033[32m", // green
	"open":        "\033[32m", // green
	"dead":        "\033[31m", // red
	"error":       "\033[31m", // red
	"failed":      "\033[31m", // red
	"stopped":     "\033[33m", // yellow
	"paused":      "\033[33m", // yellow
	"pending":     "\033[33m", // yellow
}

const ansiReset = "\033[0m"

// Format writes records as a table with a header row and aligned columns.
func (f *TableFormatter) Format(w io.Writer, records []*Record) error {
	if len(records) == 0 {
		return nil
	}

	// Use keys from first record as column definitions.
	keys := records[0].Keys()
	if len(keys) == 0 {
		return nil
	}

	// Compute column widths (max of header and all values).
	widths := make([]int, len(keys))
	for i, k := range keys {
		widths[i] = len(strings.ToUpper(k))
	}
	for _, rec := range records {
		for i, k := range keys {
			s := formatValue(rec.Get(k))
			if len(s) > widths[i] {
				widths[i] = len(s)
			}
		}
	}

	// Print header.
	for i, k := range keys {
		if i > 0 {
			fmt.Fprint(w, "  ")
		}
		fmt.Fprintf(w, "%-*s", widths[i], strings.ToUpper(k))
	}
	fmt.Fprintln(w)

	// Print rows.
	for _, rec := range records {
		for i, k := range keys {
			if i > 0 {
				fmt.Fprint(w, "  ")
			}
			val := formatValue(rec.Get(k))
			if f.Color && isStatusKey(k) {
				val = colorize(val)
			}
			fmt.Fprintf(w, "%-*s", widths[i], val)
		}
		fmt.Fprintln(w)
	}

	return nil
}

// isStatusKey returns true if the column name suggests it holds a status value.
func isStatusKey(key string) bool {
	lower := strings.ToLower(key)
	return lower == "status" || lower == "state" || lower == "actual_status" || lower == "stored_status"
}

// colorize wraps a status string with the appropriate ANSI color.
func colorize(val string) string {
	lower := strings.ToLower(val)
	if color, ok := statusColors[lower]; ok {
		return color + val + ansiReset
	}
	return val
}

// formatValue converts an interface{} to its string representation for display.
func formatValue(v interface{}) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%v", v)
}
