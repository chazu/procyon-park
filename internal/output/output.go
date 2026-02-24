// Package output provides formatters for CLI output.
//
// Three formatters are available: table (fixed-width columns with color),
// json (compact or pretty JSON), and text (space-separated, one line per record).
//
// TTY auto-detection selects table format when stdout is a terminal and JSON
// when piped. The --output flag overrides this default.
package output

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mattn/go-isatty"
)

// Format identifies an output format.
type Format string

const (
	FormatTable      Format = "table"
	FormatJSON       Format = "json"
	FormatJSONPretty Format = "json-pretty"
	FormatText       Format = "text"
)

// ParseFormat converts a string to a Format, returning an error for unknown values.
func ParseFormat(s string) (Format, error) {
	switch s {
	case "table":
		return FormatTable, nil
	case "json":
		return FormatJSON, nil
	case "json-pretty":
		return FormatJSONPretty, nil
	case "text":
		return FormatText, nil
	default:
		return "", fmt.Errorf("unknown output format %q (valid: table, json, json-pretty, text)", s)
	}
}

// DetectFormat returns the default format based on whether w is a terminal.
// If w is a *os.File and is a TTY, returns FormatTable; otherwise FormatJSON.
func DetectFormat(w io.Writer) Format {
	if f, ok := w.(*os.File); ok {
		if isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd()) {
			return FormatTable
		}
	}
	return FormatJSON
}

// ResolveFormat returns the explicit format if non-empty, otherwise auto-detects.
func ResolveFormat(explicit string, w io.Writer) (Format, error) {
	if explicit != "" {
		return ParseFormat(explicit)
	}
	return DetectFormat(w), nil
}

// Record is a row of key-value pairs preserving insertion order.
// Keys are column names; values are the display data.
type Record struct {
	keys   []string
	values map[string]interface{}
}

// NewRecord creates a Record with the given ordered keys and values.
func NewRecord() *Record {
	return &Record{values: make(map[string]interface{})}
}

// Set adds or updates a field. The first call for a given key establishes column order.
func (r *Record) Set(key string, value interface{}) {
	if _, exists := r.values[key]; !exists {
		r.keys = append(r.keys, key)
	}
	r.values[key] = value
}

// Keys returns the ordered field names.
func (r *Record) Keys() []string {
	return r.keys
}

// Get returns the value for a key.
func (r *Record) Get(key string) interface{} {
	return r.values[key]
}

// Formatter writes a set of records in a specific format.
type Formatter interface {
	Format(w io.Writer, records []*Record) error
}

// NewFormatter returns a Formatter for the given format.
func NewFormatter(f Format) Formatter {
	switch f {
	case FormatTable:
		return &TableFormatter{Color: true}
	case FormatJSON:
		return &JSONFormatter{Pretty: false}
	case FormatJSONPretty:
		return &JSONFormatter{Pretty: true}
	case FormatText:
		return &TextFormatter{}
	default:
		return &JSONFormatter{Pretty: false}
	}
}

// WriteError writes an error to stderr in the appropriate format.
func WriteError(w io.Writer, f Format, err error) {
	switch f {
	case FormatJSON, FormatJSONPretty:
		fmt.Fprintf(w, `{"error":{"message":%q}}`+"\n", err.Error())
	default:
		fmt.Fprintf(w, "error: %s\n", err.Error())
	}
}

// WriteNotification writes a notification line to stderr.
func WriteNotification(w io.Writer, msg string) {
	fmt.Fprintf(w, "[notification] %s\n", msg)
}

// RelativeDuration formats a duration as a human-readable relative string.
// Examples: "2h 15m", "45s", "3d 1h".
func RelativeDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}

	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	switch {
	case days > 0:
		if hours > 0 {
			return fmt.Sprintf("%dd %dh", days, hours)
		}
		return fmt.Sprintf("%dd", days)
	case hours > 0:
		if minutes > 0 {
			return fmt.Sprintf("%dh %dm", hours, minutes)
		}
		return fmt.Sprintf("%dh", hours)
	case minutes > 0:
		if seconds > 0 {
			return fmt.Sprintf("%dm %ds", minutes, seconds)
		}
		return fmt.Sprintf("%dm", minutes)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}
