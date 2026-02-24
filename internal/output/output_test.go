package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// --- Record tests ---

func TestRecordSetAndGet(t *testing.T) {
	r := NewRecord()
	r.Set("name", "Marble")
	r.Set("status", "active")
	r.Set("count", 42)

	if got := r.Get("name"); got != "Marble" {
		t.Errorf("Get(name) = %v, want Marble", got)
	}
	if got := r.Get("status"); got != "active" {
		t.Errorf("Get(status) = %v, want active", got)
	}
	if got := r.Get("count"); got != 42 {
		t.Errorf("Get(count) = %v, want 42", got)
	}
	if got := r.Get("missing"); got != nil {
		t.Errorf("Get(missing) = %v, want nil", got)
	}
}

func TestRecordKeyOrder(t *testing.T) {
	r := NewRecord()
	r.Set("c", 3)
	r.Set("a", 1)
	r.Set("b", 2)

	keys := r.Keys()
	want := []string{"c", "a", "b"}
	if len(keys) != len(want) {
		t.Fatalf("Keys() len = %d, want %d", len(keys), len(want))
	}
	for i, k := range keys {
		if k != want[i] {
			t.Errorf("Keys()[%d] = %q, want %q", i, k, want[i])
		}
	}
}

func TestRecordSetOverwrite(t *testing.T) {
	r := NewRecord()
	r.Set("name", "old")
	r.Set("name", "new")

	if got := r.Get("name"); got != "new" {
		t.Errorf("Get(name) = %v, want new", got)
	}
	// Key order should not duplicate.
	if len(r.Keys()) != 1 {
		t.Errorf("Keys() len = %d, want 1", len(r.Keys()))
	}
}

// --- Format parsing ---

func TestParseFormat(t *testing.T) {
	cases := []struct {
		input string
		want  Format
		err   bool
	}{
		{"table", FormatTable, false},
		{"json", FormatJSON, false},
		{"json-pretty", FormatJSONPretty, false},
		{"text", FormatText, false},
		{"yaml", "", true},
		{"", "", true},
	}
	for _, tc := range cases {
		got, err := ParseFormat(tc.input)
		if tc.err && err == nil {
			t.Errorf("ParseFormat(%q) expected error", tc.input)
		}
		if !tc.err && err != nil {
			t.Errorf("ParseFormat(%q) unexpected error: %v", tc.input, err)
		}
		if got != tc.want {
			t.Errorf("ParseFormat(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// --- TTY detection ---

func TestDetectFormatNonFile(t *testing.T) {
	// A bytes.Buffer is not a *os.File, so it should detect as JSON.
	var buf bytes.Buffer
	if got := DetectFormat(&buf); got != FormatJSON {
		t.Errorf("DetectFormat(buffer) = %q, want %q", got, FormatJSON)
	}
}

func TestDetectFormatPipe(t *testing.T) {
	// A pipe fd is not a terminal.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	if got := DetectFormat(w); got != FormatJSON {
		t.Errorf("DetectFormat(pipe) = %q, want %q", got, FormatJSON)
	}
}

func TestResolveFormatExplicit(t *testing.T) {
	var buf bytes.Buffer
	f, err := ResolveFormat("text", &buf)
	if err != nil {
		t.Fatal(err)
	}
	if f != FormatText {
		t.Errorf("ResolveFormat(text) = %q, want text", f)
	}
}

func TestResolveFormatAutoDetect(t *testing.T) {
	var buf bytes.Buffer
	f, err := ResolveFormat("", &buf)
	if err != nil {
		t.Fatal(err)
	}
	if f != FormatJSON {
		t.Errorf("ResolveFormat('', buffer) = %q, want json", f)
	}
}

// --- Table formatter ---

func TestTableFormatterBasic(t *testing.T) {
	records := makeTestRecords()
	var buf bytes.Buffer
	f := &TableFormatter{Color: false}
	if err := f.Format(&buf, records); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 { // header + 2 rows
		t.Fatalf("expected 3 lines, got %d:\n%s", len(lines), out)
	}

	// Header should be uppercase.
	if !strings.Contains(lines[0], "NAME") {
		t.Errorf("header missing NAME: %q", lines[0])
	}
	if !strings.Contains(lines[0], "STATUS") {
		t.Errorf("header missing STATUS: %q", lines[0])
	}

	// Data rows.
	if !strings.Contains(lines[1], "Marble") {
		t.Errorf("row 1 missing Marble: %q", lines[1])
	}
	if !strings.Contains(lines[2], "Sprocket") {
		t.Errorf("row 2 missing Sprocket: %q", lines[2])
	}
}

func TestTableFormatterColor(t *testing.T) {
	r := NewRecord()
	r.Set("name", "Marble")
	r.Set("status", "active")

	var buf bytes.Buffer
	f := &TableFormatter{Color: true}
	if err := f.Format(&buf, []*Record{r}); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, "\033[32m") {
		t.Error("expected green ANSI code for active status")
	}
	if !strings.Contains(out, ansiReset) {
		t.Error("expected ANSI reset after status")
	}
}

func TestTableFormatterNoColor(t *testing.T) {
	r := NewRecord()
	r.Set("name", "Marble")
	r.Set("status", "dead")

	var buf bytes.Buffer
	f := &TableFormatter{Color: false}
	if err := f.Format(&buf, []*Record{r}); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if strings.Contains(out, "\033[") {
		t.Error("expected no ANSI codes when Color=false")
	}
}

func TestTableFormatterNilValue(t *testing.T) {
	r := NewRecord()
	r.Set("name", "Marble")
	r.Set("task", nil)

	var buf bytes.Buffer
	f := &TableFormatter{Color: false}
	if err := f.Format(&buf, []*Record{r}); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(buf.String(), "-") {
		t.Error("expected nil to render as '-'")
	}
}

func TestTableFormatterEmpty(t *testing.T) {
	var buf bytes.Buffer
	f := &TableFormatter{Color: false}
	if err := f.Format(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty output for no records, got %q", buf.String())
	}
}

func TestTableFormatterColumnAlignment(t *testing.T) {
	r1 := NewRecord()
	r1.Set("name", "A")
	r1.Set("value", "short")

	r2 := NewRecord()
	r2.Set("name", "LongerName")
	r2.Set("value", "x")

	var buf bytes.Buffer
	f := &TableFormatter{Color: false}
	if err := f.Format(&buf, []*Record{r1, r2}); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}

	// All VALUE columns should start at the same position.
	positions := make([]int, len(lines))
	for i, line := range lines {
		positions[i] = strings.Index(line, "VALUE") // header
		if positions[i] < 0 {
			// Data rows: find the second column value after the gap.
			positions[i] = strings.LastIndex(line, "  ") + 2
		}
	}
	// The second column should be aligned at position 12 (10 chars + 2 gap).
	for i, line := range lines {
		if positions[i] != 12 {
			t.Errorf("line %d: second column at %d, want 12: %q", i, positions[i], line)
		}
	}
}

// --- JSON formatter ---

func TestJSONFormatterCompact(t *testing.T) {
	records := makeTestRecords()
	var buf bytes.Buffer
	f := &JSONFormatter{Pretty: false}
	if err := f.Format(&buf, records); err != nil {
		t.Fatal(err)
	}

	out := strings.TrimSpace(buf.String())

	// Should be valid JSON.
	var items []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0]["name"] != "Marble" {
		t.Errorf("items[0].name = %v, want Marble", items[0]["name"])
	}

	// Compact: no newlines within the JSON (just the trailing newline).
	if strings.Count(out, "\n") > 0 {
		t.Error("compact JSON should be single-line")
	}
}

func TestJSONFormatterPretty(t *testing.T) {
	r := NewRecord()
	r.Set("name", "Marble")

	var buf bytes.Buffer
	f := &JSONFormatter{Pretty: true}
	if err := f.Format(&buf, []*Record{r}); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, "  ") {
		t.Error("pretty JSON should contain indentation")
	}
}

func TestJSONFormatterSnakeCase(t *testing.T) {
	r := NewRecord()
	r.Set("AgentName", "Marble")
	r.Set("taskId", "abc")
	r.Set("already_snake", "yes")

	var buf bytes.Buffer
	f := &JSONFormatter{Pretty: false}
	if err := f.Format(&buf, []*Record{r}); err != nil {
		t.Fatal(err)
	}

	var items []map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &items)

	if _, ok := items[0]["agent_name"]; !ok {
		t.Errorf("expected snake_case key 'agent_name', got keys: %v", mapKeys(items[0]))
	}
	if _, ok := items[0]["task_id"]; !ok {
		t.Errorf("expected snake_case key 'task_id', got keys: %v", mapKeys(items[0]))
	}
	if _, ok := items[0]["already_snake"]; !ok {
		t.Errorf("expected key 'already_snake', got keys: %v", mapKeys(items[0]))
	}
}

func TestJSONFormatterTimeISO8601(t *testing.T) {
	ts := time.Date(2026, 2, 24, 14, 30, 0, 0, time.UTC)
	r := NewRecord()
	r.Set("created", ts)

	var buf bytes.Buffer
	f := &JSONFormatter{Pretty: false}
	if err := f.Format(&buf, []*Record{r}); err != nil {
		t.Fatal(err)
	}

	var items []map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &items)

	if items[0]["created"] != "2026-02-24T14:30:00Z" {
		t.Errorf("time = %v, want ISO 8601", items[0]["created"])
	}
}

func TestJSONFormatterDurationSeconds(t *testing.T) {
	r := NewRecord()
	r.Set("uptime", 2*time.Hour+15*time.Minute)

	var buf bytes.Buffer
	f := &JSONFormatter{Pretty: false}
	if err := f.Format(&buf, []*Record{r}); err != nil {
		t.Fatal(err)
	}

	var items []map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &items)

	// Duration becomes uptime_seconds.
	secs, ok := items[0]["uptime_seconds"]
	if !ok {
		t.Fatalf("expected 'uptime_seconds' key, got: %v", mapKeys(items[0]))
	}
	// JSON numbers are float64.
	if secs.(float64) != 8100 {
		t.Errorf("uptime_seconds = %v, want 8100", secs)
	}
}

func TestJSONFormatterEmpty(t *testing.T) {
	var buf bytes.Buffer
	f := &JSONFormatter{Pretty: false}
	if err := f.Format(&buf, nil); err != nil {
		t.Fatal(err)
	}

	var items []map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &items); err != nil {
		t.Fatalf("invalid JSON for empty: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected empty array, got %d items", len(items))
	}
}

// --- Text formatter ---

func TestTextFormatterBasic(t *testing.T) {
	records := makeTestRecords()
	var buf bytes.Buffer
	f := &TextFormatter{}
	if err := f.Format(&buf, records); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "Marble") {
		t.Errorf("line 0 missing Marble: %q", lines[0])
	}
	if !strings.Contains(lines[0], "active") {
		t.Errorf("line 0 missing active: %q", lines[0])
	}
	// No header.
	if strings.Contains(lines[0], "NAME") {
		t.Error("text format should not have headers")
	}
}

func TestTextFormatterNilValue(t *testing.T) {
	r := NewRecord()
	r.Set("name", "Marble")
	r.Set("task", nil)

	var buf bytes.Buffer
	f := &TextFormatter{}
	if err := f.Format(&buf, []*Record{r}); err != nil {
		t.Fatal(err)
	}

	line := strings.TrimSpace(buf.String())
	if line != "Marble -" {
		t.Errorf("got %q, want %q", line, "Marble -")
	}
}

func TestTextFormatterEmpty(t *testing.T) {
	var buf bytes.Buffer
	f := &TextFormatter{}
	if err := f.Format(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty output, got %q", buf.String())
	}
}

// --- Error output ---

func TestWriteErrorTable(t *testing.T) {
	var buf bytes.Buffer
	WriteError(&buf, FormatTable, fmt.Errorf("something broke"))
	if !strings.Contains(buf.String(), "error: something broke") {
		t.Errorf("got %q", buf.String())
	}
}

func TestWriteErrorJSON(t *testing.T) {
	var buf bytes.Buffer
	WriteError(&buf, FormatJSON, fmt.Errorf("something broke"))

	var obj map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &obj); err != nil {
		t.Fatalf("invalid JSON error: %v\n%s", err, buf.String())
	}
	errObj, ok := obj["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected error object, got: %v", obj)
	}
	if errObj["message"] != "something broke" {
		t.Errorf("error.message = %v", errObj["message"])
	}
}

// --- Notification output ---

func TestWriteNotification(t *testing.T) {
	var buf bytes.Buffer
	WriteNotification(&buf, "New task assigned")
	if got := buf.String(); got != "[notification] New task assigned\n" {
		t.Errorf("got %q", got)
	}
}

// --- RelativeDuration ---

func TestRelativeDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{30 * time.Second, "30s"},
		{90 * time.Second, "1m 30s"},
		{5 * time.Minute, "5m"},
		{2*time.Hour + 15*time.Minute, "2h 15m"},
		{3 * time.Hour, "3h"},
		{25 * time.Hour, "1d 1h"},
		{48 * time.Hour, "2d"},
		{72*time.Hour + 2*time.Hour, "3d 2h"},
		{-5 * time.Minute, "5m"}, // negative treated as positive
	}
	for _, tc := range cases {
		got := RelativeDuration(tc.d)
		if got != tc.want {
			t.Errorf("RelativeDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// --- NewFormatter ---

func TestNewFormatter(t *testing.T) {
	cases := []struct {
		format Format
		typ    string
	}{
		{FormatTable, "*output.TableFormatter"},
		{FormatJSON, "*output.JSONFormatter"},
		{FormatJSONPretty, "*output.JSONFormatter"},
		{FormatText, "*output.TextFormatter"},
		{Format("unknown"), "*output.JSONFormatter"},
	}
	for _, tc := range cases {
		f := NewFormatter(tc.format)
		got := fmt.Sprintf("%T", f)
		if got != tc.typ {
			t.Errorf("NewFormatter(%q) type = %s, want %s", tc.format, got, tc.typ)
		}
	}
}

// --- toSnakeCase ---

func TestToSnakeCase(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"name", "name"},
		{"AgentName", "agent_name"},
		{"taskID", "task_i_d"},
		{"already_snake", "already_snake"},
		{"ABC", "a_b_c"},
		{"", ""},
	}
	for _, tc := range cases {
		got := toSnakeCase(tc.input)
		if got != tc.want {
			t.Errorf("toSnakeCase(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// --- helpers ---

func makeTestRecords() []*Record {
	r1 := NewRecord()
	r1.Set("name", "Marble")
	r1.Set("role", "cub")
	r1.Set("status", "active")
	r1.Set("task", "procyon-park-3dk")

	r2 := NewRecord()
	r2.Set("name", "Sprocket")
	r2.Set("role", "cub")
	r2.Set("status", "active")
	r2.Set("task", "procyon-park-709")

	return []*Record{r1, r2}
}

func mapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

