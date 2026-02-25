// Package bbs escalation.go implements escalation detection for the BBS tuplespace.
// It runs each GC cycle to detect systemic obstacles, unclaimed needs,
// repeated failures, and workflow warnings.
package bbs

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/chazu/procyon-park/internal/tuplestore"
)

// EscalationConfig controls escalation detection behavior.
type EscalationConfig struct {
	// ObstacleThreshold is the minimum number of obstacles in the same scope
	// to trigger a systemic_obstacle event. Default: 2.
	ObstacleThreshold int

	// UnclaimedNeedAge is how long a need must go unclaimed before
	// escalation to the king (in seconds). Default: 600 (10 minutes).
	UnclaimedNeedAge int

	// FailureThreshold is the number of task_failed events in the same scope
	// to trigger a repo_health_warning. Default: 3.
	FailureThreshold int

	// WorkflowMatchThreshold is the percentage of active tasks matching
	// a failed workflow signature to trigger an early_warning. Default: 0.6.
	WorkflowMatchThreshold float64
}

// escalationDefaults fills in zero-value fields with defaults.
// Use -1 for UnclaimedNeedAge to explicitly request "check immediately" (0 seconds).
func escalationDefaults(cfg *EscalationConfig) {
	if cfg.ObstacleThreshold == 0 {
		cfg.ObstacleThreshold = 2
	}
	if cfg.UnclaimedNeedAge < 0 {
		cfg.UnclaimedNeedAge = 0
	} else if cfg.UnclaimedNeedAge == 0 {
		cfg.UnclaimedNeedAge = 600
	}
	if cfg.FailureThreshold == 0 {
		cfg.FailureThreshold = 3
	}
	if cfg.WorkflowMatchThreshold == 0 {
		cfg.WorkflowMatchThreshold = 0.6
	}
}

// EscalationResult holds the results of a single escalation detection cycle.
type EscalationResult struct {
	SystemicObstacles int
	UnclaimedNeeds    int
	RepoHealthWarnings int
	WorkflowWarnings  int
}

// RunEscalationDetection runs all escalation detection passes.
func RunEscalationDetection(store *tuplestore.TupleStore, cfg EscalationConfig) EscalationResult {
	escalationDefaults(&cfg)
	var result EscalationResult

	result.SystemicObstacles = detectSystemicObstacles(store, cfg.ObstacleThreshold)
	result.UnclaimedNeeds = detectUnclaimedNeeds(store, cfg.UnclaimedNeedAge)
	result.RepoHealthWarnings = detectRepeatedFailures(store, cfg.FailureThreshold)
	result.WorkflowWarnings = detectWorkflowWarnings(store, cfg.WorkflowMatchThreshold)

	return result
}

// ---------------------------------------------------------------------------
// Pass 5: detectSystemicObstacles
// ---------------------------------------------------------------------------

// detectSystemicObstacles finds scopes with 2+ obstacle tuples and writes
// a systemic_obstacle event + king notification for each.
// Returns the number of systemic obstacles detected.
func detectSystemicObstacles(store *tuplestore.TupleStore, threshold int) int {
	scopeCounts, err := store.GroupByScope("obstacle")
	if err != nil {
		log.Printf("escalation: groupByScope obstacle: %v", err)
		return 0
	}

	detected := 0
	for scope, count := range scopeCounts {
		if int(count) < threshold {
			continue
		}

		// Check if we already wrote a systemic_obstacle event for this scope.
		cat := "event"
		ident := "systemic_obstacle"
		existing, err := store.FindOne(&cat, &scope, &ident, nil, nil)
		if err != nil {
			log.Printf("escalation: check existing systemic_obstacle: %v", err)
			continue
		}
		if existing != nil {
			continue // Already escalated.
		}

		// Write systemic_obstacle event.
		payload, _ := json.Marshal(map[string]interface{}{
			"scope":          scope,
			"obstacle_count": count,
			"type":           "systemic_obstacle",
		})
		_, err = store.Insert("event", scope, "systemic_obstacle", "local",
			string(payload), "session", nil, nil, nil)
		if err != nil {
			log.Printf("escalation: write systemic_obstacle: %v", err)
			continue
		}

		// Write king notification.
		notifPayload, _ := json.Marshal(map[string]interface{}{
			"type":    "systemic_obstacle",
			"scope":   scope,
			"count":   count,
			"message": fmt.Sprintf("%d obstacles detected in scope %q", count, scope),
		})
		_, err = store.Insert("notification", "king", "systemic_obstacle:"+scope,
			"local", string(notifPayload), "session", nil, nil, nil)
		if err != nil {
			log.Printf("escalation: write king notification: %v", err)
		}

		detected++
	}

	return detected
}

// ---------------------------------------------------------------------------
// Pass 6: detectUnclaimedNeeds
// ---------------------------------------------------------------------------

// detectUnclaimedNeeds finds need tuples older than maxAge with no agent
// response and escalates to the king (once per need).
// Returns the number of escalated needs.
func detectUnclaimedNeeds(store *tuplestore.TupleStore, maxAgeSeconds int) int {
	needs, err := store.FindUnclaimedNeeds(maxAgeSeconds)
	if err != nil {
		log.Printf("escalation: findUnclaimedNeeds: %v", err)
		return 0
	}

	escalated := 0
	for _, need := range needs {
		id := need["id"].(int64)
		identity, _ := need["identity"].(string)
		scope, _ := need["scope"].(string)

		// Check if we already escalated this specific need (by its ID).
		escalationIdent := fmt.Sprintf("unclaimed_need:%d", id)
		cat := "notification"
		kingScope := "king"
		existing, err := store.FindOne(&cat, &kingScope, &escalationIdent, nil, nil)
		if err != nil {
			log.Printf("escalation: check existing need escalation: %v", err)
			continue
		}
		if existing != nil {
			continue // Already escalated.
		}

		// Escalate to king.
		payload, _ := json.Marshal(map[string]interface{}{
			"type":      "unclaimed_need",
			"need_id":   id,
			"scope":     scope,
			"identity":  identity,
			"message":   fmt.Sprintf("unclaimed need %q in scope %q for >%ds", identity, scope, maxAgeSeconds),
		})
		_, err = store.Insert("notification", "king", escalationIdent,
			"local", string(payload), "session", nil, nil, nil)
		if err != nil {
			log.Printf("escalation: write need escalation: %v", err)
			continue
		}

		escalated++
	}

	return escalated
}

// ---------------------------------------------------------------------------
// Pass 7: detectRepeatedFailures
// ---------------------------------------------------------------------------

// detectRepeatedFailures finds scopes with 3+ task_failed events and writes
// a repo_health_warning furniture fact.
// Returns the number of warnings written.
func detectRepeatedFailures(store *tuplestore.TupleStore, threshold int) int {
	scopeCounts, err := store.GroupByScope("event")
	if err != nil {
		log.Printf("escalation: groupByScope event: %v", err)
		return 0
	}

	// GroupByScope counts ALL events per scope — we need task_failed specifically.
	// Query task_failed events and group manually.
	cat := "event"
	ident := "task_failed"
	failures, err := store.FindAll(&cat, nil, &ident, nil, nil)
	if err != nil {
		log.Printf("escalation: find task_failed events: %v", err)
		return 0
	}

	// Group by scope.
	failureCounts := make(map[string]int)
	for _, f := range failures {
		scope, _ := f["scope"].(string)
		failureCounts[scope]++
	}
	_ = scopeCounts // unused from groupByScope — we needed the per-event grouping

	warnings := 0
	for scope, count := range failureCounts {
		if count < threshold {
			continue
		}

		// Check if warning already exists for this scope.
		factCat := "fact"
		warnIdent := "repo_health_warning:" + scope
		existing, err := store.FindOne(&factCat, &scope, &warnIdent, nil, nil)
		if err != nil {
			log.Printf("escalation: check existing health warning: %v", err)
			continue
		}
		if existing != nil {
			continue
		}

		// Write furniture fact.
		payload, _ := json.Marshal(map[string]interface{}{
			"type":          "repo_health_warning",
			"scope":         scope,
			"failure_count": count,
			"message":       fmt.Sprintf("%d task failures in scope %q", count, scope),
		})
		_, err = store.Insert("fact", scope, warnIdent, "local",
			string(payload), "furniture", nil, nil, nil)
		if err != nil {
			log.Printf("escalation: write health warning: %v", err)
			continue
		}

		warnings++
	}

	return warnings
}

// ---------------------------------------------------------------------------
// Pass 8: detectWorkflowWarnings
// ---------------------------------------------------------------------------

// detectWorkflowWarnings compares active tasks against failed workflow signatures.
// If 60%+ of active tasks match a failed signature, it writes an early_warning event.
// Returns the number of warnings written.
func detectWorkflowWarnings(store *tuplestore.TupleStore, matchThreshold float64) int {
	// Collect failed workflow signatures (task identities from task_failed events).
	cat := "event"
	ident := "task_failed"
	failures, err := store.FindAll(&cat, nil, &ident, nil, nil)
	if err != nil || len(failures) == 0 {
		return 0
	}

	// Build failed signature set: map of scope -> set of task patterns.
	failedSignatures := make(map[string]map[string]bool)
	for _, f := range failures {
		scope, _ := f["scope"].(string)
		payload, _ := f["payload"].(string)
		var p struct {
			Task     string `json:"task"`
			Workflow string `json:"workflow"`
		}
		if json.Unmarshal([]byte(payload), &p) != nil {
			continue
		}
		key := scope
		if key == "" {
			continue
		}
		if failedSignatures[key] == nil {
			failedSignatures[key] = make(map[string]bool)
		}
		if p.Workflow != "" {
			failedSignatures[key][p.Workflow] = true
		}
	}

	if len(failedSignatures) == 0 {
		return 0
	}

	// Collect active (in_progress) claims.
	claimCat := "claim"
	claims, err := store.FindAll(&claimCat, nil, nil, nil, nil)
	if err != nil || len(claims) == 0 {
		return 0
	}

	// Group active claims by scope.
	activeByScope := make(map[string][]map[string]interface{})
	for _, c := range claims {
		scope, _ := c["scope"].(string)
		payload, _ := c["payload"].(string)
		var p struct {
			Status string `json:"status"`
		}
		if json.Unmarshal([]byte(payload), &p) == nil && p.Status == "in_progress" {
			activeByScope[scope] = append(activeByScope[scope], c)
		}
	}

	warnings := 0
	for scope, sigs := range failedSignatures {
		active := activeByScope[scope]
		if len(active) == 0 {
			continue
		}

		// Count active tasks whose workflow matches a failed signature.
		matchCount := 0
		for _, a := range active {
			payload, _ := a["payload"].(string)
			var p struct {
				Workflow string `json:"workflow"`
			}
			if json.Unmarshal([]byte(payload), &p) == nil && p.Workflow != "" {
				if sigs[p.Workflow] {
					matchCount++
				}
			}
		}

		ratio := float64(matchCount) / float64(len(active))
		if ratio < matchThreshold {
			continue
		}

		// Check if warning already exists.
		evtCat := "event"
		warnIdent := "early_warning:" + scope
		existing, err := store.FindOne(&evtCat, &scope, &warnIdent, nil, nil)
		if err != nil || existing != nil {
			continue
		}

		// Write early_warning event.
		payload, _ := json.Marshal(map[string]interface{}{
			"type":        "early_warning",
			"scope":       scope,
			"match_ratio": ratio,
			"active":      len(active),
			"matching":    matchCount,
			"message":     fmt.Sprintf("%.0f%% of active tasks match failed workflow signatures in %q", ratio*100, scope),
		})
		_, err = store.Insert("event", scope, warnIdent, "local",
			string(payload), "session", nil, nil, nil)
		if err != nil {
			log.Printf("escalation: write early_warning: %v", err)
			continue
		}

		warnings++
	}

	return warnings
}
