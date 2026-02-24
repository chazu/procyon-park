package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// JSONFormatter renders records as JSON. When Pretty is true, output is
// indented; otherwise output is compact (one line).
type JSONFormatter struct {
	Pretty bool
}

// Format writes records as a JSON array. Field names are converted to
// snake_case. Time values are formatted as ISO 8601. Duration values are
// rendered as integer seconds with a "_seconds" suffix on the key.
func (f *JSONFormatter) Format(w io.Writer, records []*Record) error {
	items := make([]map[string]interface{}, 0, len(records))
	for _, rec := range records {
		m := make(map[string]interface{})
		for _, k := range rec.Keys() {
			sk := toSnakeCase(k)
			v := rec.Get(k)
			switch val := v.(type) {
			case time.Time:
				m[sk] = val.UTC().Format(time.RFC3339)
			case time.Duration:
				m[sk+"_seconds"] = int64(val.Seconds())
			default:
				m[sk] = v
			}
		}
		items = append(items, m)
	}

	var data []byte
	var err error
	if f.Pretty {
		data, err = json.MarshalIndent(items, "", "  ")
	} else {
		data, err = json.Marshal(items)
	}
	if err != nil {
		return fmt.Errorf("json marshal: %w", err)
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

// toSnakeCase converts a string to snake_case. It handles camelCase,
// PascalCase, and strings already in snake_case.
func toSnakeCase(s string) string {
	// Already snake_case or lowercase — fast path.
	if s == strings.ToLower(s) {
		return s
	}

	var b strings.Builder
	b.Grow(len(s) + 4)
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + ('a' - 'A'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
