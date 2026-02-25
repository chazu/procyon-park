// Package bbs gc.go implements the garbage collection engine for the BBS tuplespace.
// The GC loop runs periodically and performs: expired ephemeral cleanup, stale claim
// removal, convention promotion, and completed task archival with synthesis.
package bbs

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/chazu/procyon-park/internal/tuplestore"
)

// GCConfig controls the GC engine behavior.
type GCConfig struct {
	// Interval between GC cycles. Default: 60s.
	Interval time.Duration

	// StaleClaimAge is how old a claim must be (with a corresponding task_done)
	// to be considered stale and eligible for cleanup. Default: 1h.
	StaleClaimAge time.Duration

	// AbandonedClaimAge is how old a claim must be (without a task_done)
	// to be considered abandoned. Default: 2h.
	AbandonedClaimAge time.Duration

	// ConventionQuorum is the number of distinct agents required to
	// promote a convention proposal to a furniture convention. Default: 2.
	ConventionQuorum int

	// WarmBaseDir is the base directory for Parquet warm-tier archives.
	WarmBaseDir string

	// Synthesis configures LLM-powered knowledge extraction before archival.
	Synthesis SynthesisConfig
}

// gcDefaults fills in zero-value fields with defaults.
func gcDefaults(cfg *GCConfig) {
	if cfg.Interval == 0 {
		cfg.Interval = 60 * time.Second
	}
	if cfg.StaleClaimAge == 0 {
		cfg.StaleClaimAge = 1 * time.Hour
	}
	if cfg.AbandonedClaimAge == 0 {
		cfg.AbandonedClaimAge = 2 * time.Hour
	}
	if cfg.ConventionQuorum == 0 {
		cfg.ConventionQuorum = 2
	}
}

// GCResult holds the results of a single GC cycle.
type GCResult struct {
	ExpiredEphemeral  int
	StaleClaims       int
	AbandonedClaims   int
	PromotedConventions int
	ArchivedTasks     int
	SynthesizedTuples int
}

// RunGCLoop starts the periodic GC loop. It blocks until ctx is cancelled.
func RunGCLoop(ctx context.Context, store *tuplestore.TupleStore, cfg GCConfig) {
	gcDefaults(&cfg)

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			result := RunGCCycle(ctx, store, cfg)
			if result.ExpiredEphemeral > 0 || result.StaleClaims > 0 ||
				result.AbandonedClaims > 0 || result.PromotedConventions > 0 ||
				result.ArchivedTasks > 0 {
				log.Printf("gc: expired=%d stale=%d abandoned=%d promoted=%d archived=%d synthesized=%d",
					result.ExpiredEphemeral, result.StaleClaims, result.AbandonedClaims,
					result.PromotedConventions, result.ArchivedTasks, result.SynthesizedTuples)
			}
		}
	}
}

// RunGCCycle runs all GC passes once and returns the results.
func RunGCCycle(ctx context.Context, store *tuplestore.TupleStore, cfg GCConfig) GCResult {
	gcDefaults(&cfg)
	var result GCResult

	result.ExpiredEphemeral = collectExpiredEphemeral(store)
	stale, abandoned := cleanupStaleClaims(store, cfg)
	result.StaleClaims = stale
	result.AbandonedClaims = abandoned
	result.PromotedConventions = promoteConventions(store, cfg.ConventionQuorum)
	archived, synthesized := archiveCompletedTasks(ctx, store, cfg)
	result.ArchivedTasks = archived
	result.SynthesizedTuples = synthesized

	return result
}

// ---------------------------------------------------------------------------
// Pass 1: collectExpiredEphemeral
// ---------------------------------------------------------------------------

// collectExpiredEphemeral deletes tuples past their TTL.
// Returns the number of tuples deleted.
func collectExpiredEphemeral(store *tuplestore.TupleStore) int {
	expired, err := store.FindExpiredEphemeral()
	if err != nil {
		log.Printf("gc: findExpiredEphemeral: %v", err)
		return 0
	}

	if len(expired) == 0 {
		return 0
	}

	// Log unclaimed expired tuples (those with no agent_id) for diagnostics.
	for _, t := range expired {
		agentID, _ := t["agent_id"].(*string)
		if agentID == nil {
			identity, _ := t["identity"].(string)
			category, _ := t["category"].(string)
			log.Printf("gc: unclaimed expired tuple: category=%s identity=%s", category, identity)
		}
	}

	n, err := store.DeleteExpiredEphemeral()
	if err != nil {
		log.Printf("gc: deleteExpiredEphemeral: %v", err)
		return 0
	}
	return int(n)
}

// ---------------------------------------------------------------------------
// Pass 2: cleanupStaleClaims
// ---------------------------------------------------------------------------

// cleanupStaleClaims removes stale and abandoned claim tuples.
// Stale: older than StaleClaimAge AND has a corresponding task_done event.
// Abandoned: older than AbandonedClaimAge AND no task_done event.
// Returns (stale count, abandoned count).
func cleanupStaleClaims(store *tuplestore.TupleStore, cfg GCConfig) (int, int) {
	// Pre-scan task_done events to build the doneTasks set.
	doneTasks := buildDoneTaskSet(store)

	// Find claims old enough to potentially be stale (use the shorter threshold).
	staleAge := int(cfg.StaleClaimAge.Seconds())
	claims, err := store.FindStaleClaims(staleAge)
	if err != nil {
		log.Printf("gc: findStaleClaims: %v", err)
		return 0, 0
	}

	var staleCount, abandonedCount int
	abandonedAge := int(cfg.AbandonedClaimAge.Seconds())

	for _, claim := range claims {
		taskID, _ := claim["identity"].(string)
		id := claim["id"].(int64)
		createdAt, _ := claim["created_at"].(string)

		if doneTasks[taskID] {
			// Stale: task is done, claim can be cleaned up.
			if _, err := store.Delete(id); err != nil {
				log.Printf("gc: delete stale claim %d: %v", id, err)
				continue
			}
			staleCount++
		} else {
			// Check if old enough to be abandoned (stricter threshold).
			claimTime, err := time.Parse("2006-01-02 15:04:05", createdAt)
			if err != nil {
				continue
			}
			age := int(time.Since(claimTime).Seconds())
			if age >= abandonedAge {
				if _, err := store.Delete(id); err != nil {
					log.Printf("gc: delete abandoned claim %d: %v", id, err)
					continue
				}
				abandonedCount++
			}
		}
	}

	return staleCount, abandonedCount
}

// buildDoneTaskSet scans task_done events and returns a set of task identities.
func buildDoneTaskSet(store *tuplestore.TupleStore) map[string]bool {
	cat := "event"
	ident := "task_done"
	events, err := store.FindAll(&cat, nil, &ident, nil, nil)
	if err != nil {
		log.Printf("gc: scan task_done events: %v", err)
		return nil
	}

	doneTasks := make(map[string]bool, len(events))
	for _, evt := range events {
		payload, _ := evt["payload"].(string)
		var p struct {
			Task string `json:"task"`
		}
		if json.Unmarshal([]byte(payload), &p) == nil && p.Task != "" {
			doneTasks[p.Task] = true
		}
	}
	return doneTasks
}

// ---------------------------------------------------------------------------
// Pass 3: promoteConventions
// ---------------------------------------------------------------------------

// promoteConventions groups convention proposals by identity and promotes
// those with enough distinct agents to furniture conventions.
// Returns the number of promoted conventions.
func promoteConventions(store *tuplestore.TupleStore, quorum int) int {
	dups, err := store.FindDuplicateConventionProposals()
	if err != nil {
		log.Printf("gc: findDuplicateConventionProposals: %v", err)
		return 0
	}

	promoted := 0
	for _, d := range dups {
		identity, _ := d["identity"].(string)
		agentCount, _ := d["agent_count"].(int64)

		if int(agentCount) < quorum {
			continue
		}

		// Fetch one of the proposals to get scope and payload.
		cat := "conventionProposal"
		proposals, err := store.FindAll(&cat, nil, &identity, nil, nil)
		if err != nil || len(proposals) == 0 {
			continue
		}

		scope, _ := proposals[0]["scope"].(string)
		payload, _ := proposals[0]["payload"].(string)
		if scope == "" {
			scope = "system"
		}

		// Promote: write as furniture convention in global scope.
		_, err = store.Upsert("convention", scope, identity, "local",
			payload, "furniture", nil, nil, nil)
		if err != nil {
			log.Printf("gc: promote convention %q: %v", identity, err)
			continue
		}

		// Clean up the proposals.
		store.DeleteByPattern(&cat, nil, &identity, nil)
		promoted++
	}

	return promoted
}

// ---------------------------------------------------------------------------
// Pass 4: archiveCompletedTasks
// ---------------------------------------------------------------------------

// archiveCompletedTasks finds task_done events, runs synthesis, then archives
// tuples by task_id and agent_id. Skips agents with pending dismiss_request.
// Returns (archived tuple count, synthesized tuple count).
func archiveCompletedTasks(ctx context.Context, store *tuplestore.TupleStore, cfg GCConfig) (int, int) {
	if cfg.WarmBaseDir == "" {
		return 0, 0
	}

	cat := "event"
	ident := "task_done"
	events, err := store.FindAll(&cat, nil, &ident, nil, nil)
	if err != nil {
		log.Printf("gc: scan task_done events: %v", err)
		return 0, 0
	}

	// Build set of agents with pending dismiss_requests.
	pendingDismiss := buildPendingDismissSet(store)

	totalArchived := 0
	totalSynthesized := 0

	for _, evt := range events {
		payload, _ := evt["payload"].(string)
		var p struct {
			Task  string `json:"task"`
			Agent string `json:"agent"`
		}
		if json.Unmarshal([]byte(payload), &p) != nil {
			continue
		}

		// Skip agents with pending dismiss_request.
		if p.Agent != "" && pendingDismiss[p.Agent] {
			continue
		}

		scope, _ := evt["scope"].(string)

		// Collect tuples for synthesis before archiving.
		if cfg.Synthesis.Enabled && p.Task != "" {
			taskTuples := collectTaskTuples(store, p.Task)
			if len(taskTuples) > 0 {
				n := Synthesize(ctx, cfg.Synthesis, store, taskTuples, scope)
				totalSynthesized += n
			}
		}

		// Archive by task ID.
		if p.Task != "" {
			n, err := store.ArchiveByTaskID(cfg.WarmBaseDir, p.Task)
			if err != nil {
				log.Printf("gc: archive task %s: %v", p.Task, err)
			} else {
				totalArchived += n
			}
		}

		// Archive by agent ID.
		if p.Agent != "" {
			n, err := store.ArchiveByAgentID(cfg.WarmBaseDir, p.Agent)
			if err != nil {
				log.Printf("gc: archive agent %s: %v", p.Agent, err)
			} else {
				totalArchived += n
			}
		}
	}

	return totalArchived, totalSynthesized
}

// buildPendingDismissSet scans dismiss_request events and returns a set of agent names.
func buildPendingDismissSet(store *tuplestore.TupleStore) map[string]bool {
	cat := "event"
	ident := "dismiss_request"
	events, err := store.FindAll(&cat, nil, &ident, nil, nil)
	if err != nil {
		log.Printf("gc: scan dismiss_request events: %v", err)
		return nil
	}

	pending := make(map[string]bool, len(events))
	for _, evt := range events {
		payload, _ := evt["payload"].(string)
		var p struct {
			Agent string `json:"agent"`
		}
		if json.Unmarshal([]byte(payload), &p) == nil && p.Agent != "" {
			pending[p.Agent] = true
		}
	}
	return pending
}

// collectTaskTuples gathers all tuples associated with a task for synthesis.
func collectTaskTuples(store *tuplestore.TupleStore, taskID string) []map[string]interface{} {
	// FindAll with task_id match via payload search (task_id is a column, not in payload).
	// Use the direct column by querying with a scope/identity approach.
	// Actually, task_id is a column — we need a custom query. Use FindAll with no
	// specific category filter, but we can't filter by task_id column via FindAll.
	// Workaround: scan all tuples in scope and filter by task_id.
	// A better approach: use the underlying store method.
	// For now, let's match via the tuplestore's existing interface.
	all, err := store.FindAll(nil, nil, nil, nil, nil)
	if err != nil {
		return nil
	}

	var result []map[string]interface{}
	for _, t := range all {
		tid, _ := t["task_id"].(*string)
		if tid != nil && *tid == taskID {
			result = append(result, t)
		}
	}
	return result
}
