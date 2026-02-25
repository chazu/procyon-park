// Package bbs feedback.go implements the feedback loop that writes warm-tier
// analytics results back into the hot tuplespace as furniture tuples.
//
// The feedback loop runs on a configurable interval (default 24h) and performs:
//   1. Convention pruning: delete conventions that hurt outcomes.
//   2. Obstacle surfacing: write obstacle_cluster facts.
//   3. Repo health: write repo_health facts per scope.
//   4. Workflow signature caching: cache failed signatures for GC warnings.
//   5. Knowledge flow surfacing: write knowledge_flow facts.
package bbs

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/chazu/procyon-park/internal/tuplestore"
)

// FeedbackConfig controls the feedback loop behavior.
type FeedbackConfig struct {
	// Interval between feedback cycles. Default: 24h.
	Interval time.Duration

	// WarmBaseDir is the base directory for Parquet warm-tier archives.
	WarmBaseDir string

	// Scope filters analytics to a specific scope. Empty means all scopes.
	Scope string

	// ConventionMinTasks is the minimum number of tasks in both before/after
	// periods to evaluate a convention. Default: 3.
	ConventionMinTasks int

	// ObstacleMinOccurrences is the minimum occurrences for an obstacle
	// cluster to be surfaced. Default: 2.
	ObstacleMinOccurrences int
}

// feedbackDefaults fills in zero-value fields with defaults.
func feedbackDefaults(cfg *FeedbackConfig) {
	if cfg.Interval == 0 {
		cfg.Interval = 24 * time.Hour
	}
	if cfg.ConventionMinTasks == 0 {
		cfg.ConventionMinTasks = 3
	}
	if cfg.ObstacleMinOccurrences == 0 {
		cfg.ObstacleMinOccurrences = 2
	}
}

// FeedbackResult holds the counts of actions taken in a feedback cycle.
type FeedbackResult struct {
	ConventionsPruned  int
	ConventionsKept    int
	ObstaclesSurfaced  int
	RepoHealthUpdated  int
	SignaturesCached   int
	KnowledgeFlows     int
	Errors             []error
}

// RunFeedbackLoop starts the periodic feedback loop. It blocks until ctx is cancelled.
func RunFeedbackLoop(ctx context.Context, store *tuplestore.TupleStore, cfg FeedbackConfig) {
	feedbackDefaults(&cfg)

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			result := RunFeedbackCycle(store, cfg)
			if result.ConventionsPruned > 0 || result.ObstaclesSurfaced > 0 ||
				result.RepoHealthUpdated > 0 || result.SignaturesCached > 0 ||
				result.KnowledgeFlows > 0 {
				log.Printf("feedback: pruned=%d kept=%d obstacles=%d health=%d sigs=%d flows=%d errors=%d",
					result.ConventionsPruned, result.ConventionsKept,
					result.ObstaclesSurfaced, result.RepoHealthUpdated,
					result.SignaturesCached, result.KnowledgeFlows, len(result.Errors))
			}
		}
	}
}

// RunFeedbackCycle runs all feedback actions once and returns the results.
func RunFeedbackCycle(store *tuplestore.TupleStore, cfg FeedbackConfig) FeedbackResult {
	feedbackDefaults(&cfg)
	var result FeedbackResult

	if cfg.WarmBaseDir == "" {
		return result
	}

	pruned, kept, err := feedbackConventionPruning(store, cfg)
	result.ConventionsPruned = pruned
	result.ConventionsKept = kept
	if err != nil {
		result.Errors = append(result.Errors, err)
	}

	surfaced, err := feedbackObstacleSurfacing(store, cfg)
	result.ObstaclesSurfaced = surfaced
	if err != nil {
		result.Errors = append(result.Errors, err)
	}

	health, err := feedbackRepoHealth(store, cfg)
	result.RepoHealthUpdated = health
	if err != nil {
		result.Errors = append(result.Errors, err)
	}

	cached, err := feedbackWorkflowSignatures(store, cfg)
	result.SignaturesCached = cached
	if err != nil {
		result.Errors = append(result.Errors, err)
	}

	flows, err := feedbackKnowledgeFlow(store, cfg)
	result.KnowledgeFlows = flows
	if err != nil {
		result.Errors = append(result.Errors, err)
	}

	return result
}

// ---------------------------------------------------------------------------
// Action 1: Convention Pruning
// ---------------------------------------------------------------------------

// feedbackConventionPruning queries ConventionEffectiveness and:
//   - Deletes conventions where AfterRate < BeforeRate (convention hurt outcomes).
//   - Writes a fact for conventions that helped (AfterRate >= BeforeRate).
//
// Returns (pruned, kept, error).
func feedbackConventionPruning(store *tuplestore.TupleStore, cfg FeedbackConfig) (int, int, error) {
	results, err := QueryConventionEffectiveness(cfg.WarmBaseDir, cfg.Scope, cfg.ConventionMinTasks)
	if err != nil {
		return 0, 0, err
	}

	pruned, kept := 0, 0
	for _, ce := range results {
		if ce.AfterRate < ce.BeforeRate {
			// Convention hurt outcomes — delete it.
			cat := "convention"
			_, delErr := store.DeleteByPattern(&cat, nil, &ce.ConventionID, nil)
			if delErr != nil {
				log.Printf("feedback: delete convention %q: %v", ce.ConventionID, delErr)
				continue
			}
			pruned++
		} else {
			// Convention helped — write a fact recording effectiveness.
			payload, _ := json.Marshal(map[string]interface{}{
				"convention_id":    ce.ConventionID,
				"before_rate":      ce.BeforeRate,
				"after_rate":       ce.AfterRate,
				"before_tasks":     ce.BeforeTaskCount,
				"after_tasks":      ce.AfterTaskCount,
				"type":             "convention_effective",
			})

			scope := cfg.Scope
			if scope == "" {
				scope = "system"
			}
			_, upsertErr := store.Upsert("fact", scope,
				"convention_effective:"+ce.ConventionID, "analytics",
				string(payload), "furniture", nil, nil, nil)
			if upsertErr != nil {
				log.Printf("feedback: upsert convention fact %q: %v", ce.ConventionID, upsertErr)
				continue
			}
			kept++
		}
	}

	return pruned, kept, nil
}

// ---------------------------------------------------------------------------
// Action 2: Obstacle Surfacing
// ---------------------------------------------------------------------------

// feedbackObstacleSurfacing queries ObstacleClusters and writes obstacle_cluster
// facts as furniture.
func feedbackObstacleSurfacing(store *tuplestore.TupleStore, cfg FeedbackConfig) (int, error) {
	clusters, err := QueryObstacleClusters(cfg.WarmBaseDir, cfg.Scope, cfg.ObstacleMinOccurrences)
	if err != nil {
		return 0, err
	}

	surfaced := 0
	for _, oc := range clusters {
		payload, _ := json.Marshal(map[string]interface{}{
			"description":    oc.Description,
			"occurrences":    oc.Occurrences,
			"distinct_agents": oc.DistinctAgents,
			"first_seen":     oc.FirstSeen,
			"last_seen":      oc.LastSeen,
			"type":           "obstacle_cluster",
		})

		scope := cfg.Scope
		if scope == "" {
			scope = "system"
		}
		_, err := store.Upsert("fact", scope,
			"obstacle_cluster:"+oc.Description, "analytics",
			string(payload), "furniture", nil, nil, nil)
		if err != nil {
			log.Printf("feedback: upsert obstacle cluster %q: %v", oc.Description, err)
			continue
		}
		surfaced++
	}

	return surfaced, nil
}

// ---------------------------------------------------------------------------
// Action 3: Repo Health
// ---------------------------------------------------------------------------

// feedbackRepoHealth queries AgentPerformance and writes repo_health facts
// per scope as furniture.
func feedbackRepoHealth(store *tuplestore.TupleStore, cfg FeedbackConfig) (int, error) {
	perfs, err := QueryAgentPerformance(cfg.WarmBaseDir, cfg.Scope)
	if err != nil {
		return 0, err
	}

	updated := 0
	for _, ap := range perfs {
		var ratio interface{}
		if ap.ObstacleCount > 0 {
			ratio = ap.ArtifactObstacleRate
		}

		payload, _ := json.Marshal(map[string]interface{}{
			"obstacle_count":         ap.ObstacleCount,
			"artifacts_produced":     ap.ArtifactCount,
			"unique_agents_blocked":  ap.DistinctAgents,
			"artifact_obstacle_ratio": ratio,
			"type":                   "repo_health",
			"content": repoHealthSummary(ap),
		})

		_, err := store.Upsert("fact", ap.Scope,
			"repo-health", "analytics",
			string(payload), "furniture", nil, nil, nil)
		if err != nil {
			log.Printf("feedback: upsert repo health %q: %v", ap.Scope, err)
			continue
		}
		updated++
	}

	return updated, nil
}

// repoHealthSummary builds a human-readable summary of agent performance.
func repoHealthSummary(ap AgentPerformance) string {
	ratioStr := "N/A (no obstacles)"
	if ap.ObstacleCount > 0 {
		ratioStr = formatFloat(ap.ArtifactObstacleRate)
	}
	return "Repo health: " + formatInt(ap.ObstacleCount) + " obstacles (" +
		formatInt(ap.DistinctAgents) + " unique agents blocked), " +
		formatInt(ap.ArtifactCount) + " artifacts produced, " +
		"artifact/obstacle ratio: " + ratioStr
}

func formatInt(n int64) string {
	return json.Number(intToStr(n)).String()
}

func intToStr(n int64) string {
	if n == 0 {
		return "0"
	}
	s := ""
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	if neg {
		s = "-" + s
	}
	return s
}

func formatFloat(f float64) string {
	// Simple 2-decimal formatting without importing strconv.
	// Use json.Marshal for reliable float formatting.
	b, _ := json.Marshal(f)
	return string(b)
}

// ---------------------------------------------------------------------------
// Action 4: Workflow Signature Caching
// ---------------------------------------------------------------------------

// feedbackWorkflowSignatures queries WorkflowSignatures and caches failed
// signatures as furniture for GC's workflow warning detection.
func feedbackWorkflowSignatures(store *tuplestore.TupleStore, cfg FeedbackConfig) (int, error) {
	sigs, err := QueryWorkflowSignatures(cfg.WarmBaseDir, cfg.Scope)
	if err != nil {
		return 0, err
	}

	cached := 0
	for _, ws := range sigs {
		if ws.Outcome != "incomplete" {
			continue // Only cache failed signatures.
		}

		payload, _ := json.Marshal(map[string]interface{}{
			"task_id":  ws.TaskID,
			"pattern":  ws.Pattern,
			"outcome":  ws.Outcome,
			"agent_id": ws.AgentID,
			"type":     "failed_workflow_signature",
		})

		scope := cfg.Scope
		if scope == "" {
			scope = "system"
		}
		_, err := store.Upsert("fact", scope,
			"failed_sig:"+ws.Pattern, "analytics",
			string(payload), "furniture", nil, nil, nil)
		if err != nil {
			log.Printf("feedback: upsert workflow sig %q: %v", ws.Pattern, err)
			continue
		}
		cached++
	}

	return cached, nil
}

// ---------------------------------------------------------------------------
// Action 5: Knowledge Flow Surfacing
// ---------------------------------------------------------------------------

// feedbackKnowledgeFlow queries KnowledgeFlow and writes knowledge_flow facts
// as furniture, summarizing cross-agent knowledge propagation.
func feedbackKnowledgeFlow(store *tuplestore.TupleStore, cfg FeedbackConfig) (int, error) {
	flows, err := QueryKnowledgeFlow(cfg.WarmBaseDir, cfg.Scope)
	if err != nil {
		return 0, err
	}

	written := 0
	for _, kf := range flows {
		payload, _ := json.Marshal(map[string]interface{}{
			"source_agent": kf.SourceAgent,
			"target_agent": kf.TargetAgent,
			"category":     kf.Category,
			"identity":     kf.Identity,
			"source_task":  kf.SourceTask,
			"target_task":  kf.TargetTask,
			"type":         "knowledge_flow",
		})

		scope := cfg.Scope
		if scope == "" {
			scope = "system"
		}
		ident := "knowledge_flow:" + kf.SourceAgent + "→" + kf.TargetAgent + ":" + kf.Identity
		_, err := store.Upsert("fact", scope,
			ident, "analytics",
			string(payload), "furniture", nil, nil, nil)
		if err != nil {
			log.Printf("feedback: upsert knowledge flow: %v", err)
			continue
		}
		written++
	}

	return written, nil
}
