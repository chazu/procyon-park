// Package bbs crosspoll.go implements cross-pollination detection.
//
// Cross-pollination detects when a tuple payload references another scope
// (substring match) and writes notification tuples for agents in the
// referenced scope. Rate limited: 1 per (source_agent, target_scope) per 5 min.
package bbs

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/chazu/procyon-park/internal/tuplestore"
)

// CrossPollConfig controls cross-pollination behavior.
type CrossPollConfig struct {
	// RateLimit is the minimum interval between notifications for the same
	// (source_agent, target_scope) pair. Default: 5 minutes.
	RateLimit time.Duration
}

// crossPollDefaults fills in zero-value fields with defaults.
func crossPollDefaults(cfg *CrossPollConfig) {
	if cfg.RateLimit == 0 {
		cfg.RateLimit = 5 * time.Minute
	}
}

// CrossPollinator tracks rate limits for cross-pollination notifications.
type CrossPollinator struct {
	mu       sync.Mutex
	cfg      CrossPollConfig
	lastSent map[string]time.Time // key: "sourceAgent:targetScope"
}

// NewCrossPollinator creates a new CrossPollinator.
func NewCrossPollinator(cfg CrossPollConfig) *CrossPollinator {
	crossPollDefaults(&cfg)
	return &CrossPollinator{
		cfg:      cfg,
		lastSent: make(map[string]time.Time),
	}
}

// CrossPollResult holds the counts from a cross-pollination run.
type CrossPollResult struct {
	Checked  int
	Notified int
	Skipped  int
}

// CheckTuple examines a single tuple for cross-scope references and writes
// notifications for referenced scopes. Returns true if a notification was sent.
func (cp *CrossPollinator) CheckTuple(store *tuplestore.TupleStore, tuple map[string]interface{}, knownScopes []string) bool {
	payload, _ := tuple["payload"].(string)
	tupleScope, _ := tuple["scope"].(string)

	if payload == "" {
		return false
	}

	// Extract source agent from payload or agent_id.
	sourceAgent := extractAgent(tuple)
	if sourceAgent == "" {
		return false
	}

	// Check payload for references to other scopes.
	for _, scope := range knownScopes {
		if scope == tupleScope || scope == "" {
			continue
		}

		if !containsScope(payload, scope) {
			continue
		}

		// Rate limit check.
		if !cp.allowNotification(sourceAgent, scope) {
			continue
		}

		// Write notification for agents in the referenced scope.
		identity, _ := tuple["identity"].(string)
		category, _ := tuple["category"].(string)

		notifPayload, _ := json.Marshal(map[string]interface{}{
			"type":         "cross_pollination",
			"source_agent": sourceAgent,
			"source_scope": tupleScope,
			"target_scope": scope,
			"category":     category,
			"identity":     identity,
			"message":      sourceAgent + " referenced scope " + scope + " in " + category + ": " + identity,
		})

		_, err := store.Insert("notification", scope,
			"crosspoll:"+sourceAgent+":"+tupleScope+"→"+scope,
			"local", string(notifPayload), "session", nil, nil, nil)
		if err != nil {
			log.Printf("crosspoll: write notification: %v", err)
			continue
		}

		cp.recordNotification(sourceAgent, scope)
		return true
	}

	return false
}

// RunCrossPollination scans recent tuples and checks for cross-scope references.
func (cp *CrossPollinator) RunCrossPollination(store *tuplestore.TupleStore, knownScopes []string) CrossPollResult {
	var result CrossPollResult

	// Scan all session-lifecycle tuples (recent activity).
	tuples, err := store.FindAll(nil, nil, nil, nil, nil)
	if err != nil {
		log.Printf("crosspoll: scan tuples: %v", err)
		return result
	}

	for _, t := range tuples {
		lifecycle, _ := t["lifecycle"].(string)
		if lifecycle != "session" {
			continue
		}

		result.Checked++
		if cp.CheckTuple(store, t, knownScopes) {
			result.Notified++
		}
	}

	result.Skipped = result.Checked - result.Notified
	return result
}

// allowNotification checks the rate limit for a (sourceAgent, targetScope) pair.
func (cp *CrossPollinator) allowNotification(sourceAgent, targetScope string) bool {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	key := sourceAgent + ":" + targetScope
	last, ok := cp.lastSent[key]
	if ok && time.Since(last) < cp.cfg.RateLimit {
		return false
	}
	return true
}

// recordNotification updates the rate limit timestamp.
func (cp *CrossPollinator) recordNotification(sourceAgent, targetScope string) {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	key := sourceAgent + ":" + targetScope
	cp.lastSent[key] = time.Now()
}

// extractAgent pulls the agent identifier from a tuple.
func extractAgent(tuple map[string]interface{}) string {
	// Try agent_id column first.
	if agentID, ok := tuple["agent_id"].(*string); ok && agentID != nil && *agentID != "" {
		return *agentID
	}

	// Fallback: parse agent from payload JSON.
	payload, _ := tuple["payload"].(string)
	if payload == "" {
		return ""
	}
	var p struct {
		Agent string `json:"agent"`
	}
	if json.Unmarshal([]byte(payload), &p) == nil && p.Agent != "" {
		return p.Agent
	}
	return ""
}

// containsScope checks if the payload string contains a reference to the scope.
func containsScope(payload, scope string) bool {
	// Simple substring match as specified in the design.
	for i := 0; i <= len(payload)-len(scope); i++ {
		if payload[i:i+len(scope)] == scope {
			return true
		}
	}
	return false
}
