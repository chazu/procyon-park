package output

import (
	"fmt"
	"io"
	"strings"
)

// TextFormatter renders records as space-separated values, one line per record.
// No header row. Nil values render as "-".
type TextFormatter struct{}

// Format writes each record as a single space-separated line.
func (f *TextFormatter) Format(w io.Writer, records []*Record) error {
	for _, rec := range records {
		keys := rec.Keys()
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, formatValue(rec.Get(k)))
		}
		if _, err := fmt.Fprintln(w, strings.Join(parts, " ")); err != nil {
			return err
		}
	}
	return nil
}
