package prime

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/chazu/procyon-park/internal/tuplestore"
)

// maxPerCategory caps how many tuples of each category are included in the
// context snapshot. If more exist, an omission notice is appended.
const maxPerCategory = 10

// BuildAgentContext pre-reads tuplespace state and returns a formatted context
// block. It fetches furniture tuples (conventions, facts), active session tuples
// (claims, obstacles, needs), and any task-specific escalations. Each category
// is capped at maxPerCategory entries with an omission notice if truncated.
func BuildAgentContext(store *tuplestore.TupleStore, scope, taskID string) (string, error) {
	var b strings.Builder
	b.WriteString("BBS TUPLESPACE CONTEXT (pre-read at launch):\n")

	// Furniture tuples: conventions and facts (long-lived project knowledge)
	sections := []struct {
		label    string
		category string
	}{
		{"CONVENTIONS", "convention"},
		{"FACTS", "fact"},
	}
	for _, sec := range sections {
		tuples, err := findByCategory(store, sec.category, &scope)
		if err != nil {
			return "", fmt.Errorf("context: fetch %s: %w", sec.category, err)
		}
		if len(tuples) == 0 {
			continue
		}
		b.WriteString(sec.label + ":\n")
		writeTuples(&b, tuples, sec.category, maxPerCategory)
	}

	// Active session tuples: claims, obstacles, needs
	active := []struct {
		label    string
		category string
	}{
		{"ACTIVE AGENT ACTIVITY", "claim"},
		{"OBSTACLES", "obstacle"},
		{"NEEDS", "need"},
	}
	for _, sec := range active {
		tuples, err := findByCategory(store, sec.category, &scope)
		if err != nil {
			return "", fmt.Errorf("context: fetch %s: %w", sec.category, err)
		}
		if len(tuples) == 0 {
			continue
		}
		b.WriteString(sec.label + ":\n")
		writeTuples(&b, tuples, sec.category, maxPerCategory)
	}

	// Task-specific escalations: obstacles and needs referencing this task
	if taskID != "" {
		taskTuples, err := findTaskEscalations(store, scope, taskID)
		if err != nil {
			return "", fmt.Errorf("context: fetch task escalations: %w", err)
		}
		if len(taskTuples) > 0 {
			b.WriteString("TASK-SPECIFIC ESCALATIONS:\n")
			writeTuples(&b, taskTuples, "escalation", maxPerCategory)
		}
	}

	return b.String(), nil
}

// findByCategory returns all tuples with the given category and scope.
func findByCategory(store *tuplestore.TupleStore, category string, scope *string) ([]map[string]interface{}, error) {
	return store.FindAll(&category, scope, nil, nil, nil)
}

// findTaskEscalations returns obstacle and need tuples whose payload references
// the given taskID. Uses quoted FTS5 payload search to handle task IDs with
// hyphens (e.g., "task-123") which FTS5 would otherwise interpret as negation.
func findTaskEscalations(store *tuplestore.TupleStore, scope, taskID string) ([]map[string]interface{}, error) {
	var result []map[string]interface{}

	// Quote the taskID for FTS5 so hyphens are treated as literal characters.
	quoted := "\"" + taskID + "\""
	for _, cat := range []string{"obstacle", "need"} {
		tuples, err := store.FindAll(&cat, &scope, nil, nil, &quoted)
		if err != nil {
			return nil, err
		}
		result = append(result, tuples...)
	}

	// Sort by ID for deterministic output.
	sort.Slice(result, func(i, j int) bool {
		idI, _ := result[i]["id"].(int64)
		idJ, _ := result[j]["id"].(int64)
		return idI < idJ
	})

	return result, nil
}

// writeTuples formats tuples as compact key=value summaries, capped at limit.
func writeTuples(b *strings.Builder, tuples []map[string]interface{}, category string, limit int) {
	count := len(tuples)
	if count > limit {
		tuples = tuples[:limit]
	}

	for _, t := range tuples {
		cat, _ := t["category"].(string)
		scope, _ := t["scope"].(string)
		identity, _ := t["identity"].(string)

		fmt.Fprintf(b, "  [%s/%s] %s", cat, scope, identity)

		// Format payload as key=value pairs if it's valid JSON object.
		payload, _ := t["payload"].(string)
		if kvs := payloadSummary(payload); kvs != "" {
			fmt.Fprintf(b, ": %s", kvs)
		}

		b.WriteString("\n")
	}

	if count > limit {
		fmt.Fprintf(b, "  ... (%d more %s tuples omitted)\n", count-limit, category)
	}
}

// payloadSummary converts a JSON payload string into a compact key=value summary.
// Returns empty string for empty or trivial payloads.
func payloadSummary(payload string) string {
	if payload == "" || payload == "{}" {
		return ""
	}

	var m map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return ""
	}
	if len(m) == 0 {
		return ""
	}

	// Sort keys for deterministic output.
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
	}
	return strings.Join(parts, ", ")
}
