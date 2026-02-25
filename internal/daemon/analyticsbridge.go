// analyticsbridge.go registers JSON-RPC handlers for analytics, GC, feedback,
// and synthesis operations. These handlers bridge CLI commands to the
// corresponding bbs package functions.
package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chazu/procyon-park/internal/bbs"
	"github.com/chazu/procyon-park/internal/tuplestore"
)

// RegisterAnalyticsHandlers wires the analytics.*, gc.*, feedback.*, and
// synthesis.* JSON-RPC methods. Must be called before the IPCServer is started.
func RegisterAnalyticsHandlers(srv *IPCServer, store *tuplestore.TupleStore, dataDir string) {
	srv.Handle("analytics.performance", handleAnalyticsPerformance(dataDir))
	srv.Handle("analytics.obstacles", handleAnalyticsObstacles(dataDir))
	srv.Handle("analytics.conventions", handleAnalyticsConventions(dataDir))
	srv.Handle("analytics.knowledge", handleAnalyticsKnowledge(dataDir))
	srv.Handle("analytics.signatures", handleAnalyticsSignatures(dataDir))
	srv.Handle("gc.run", handleGCRun(store, dataDir))
	srv.Handle("gc.status", handleGCStatus(store, dataDir))
	srv.Handle("feedback.run", handleFeedbackRun(store, dataDir))
	srv.Handle("synthesis.run", handleSynthesisRun(store))
}

// ---------------------------------------------------------------------------
// Shared params
// ---------------------------------------------------------------------------

type analyticsParams struct {
	Repo     string `json:"repo"`
	Scope    string `json:"scope"`
	MinCount int    `json:"min_count"`
}

// warmBaseDir returns the Parquet warm-tier directory.
func warmBaseDir(dataDir string) string {
	return dataDir + "/warm"
}

// ---------------------------------------------------------------------------
// analytics.performance
// ---------------------------------------------------------------------------

func handleAnalyticsPerformance(dataDir string) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p analyticsParams
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
			}
		}
		scope := p.Scope
		if scope == "" {
			scope = p.Repo
		}
		results, err := bbs.QueryAgentPerformance(warmBaseDir(dataDir), scope)
		if err != nil {
			return nil, fmt.Errorf("analytics.performance: %w", err)
		}
		if results == nil {
			results = []bbs.AgentPerformance{}
		}
		return results, nil
	}
}

// ---------------------------------------------------------------------------
// analytics.obstacles
// ---------------------------------------------------------------------------

func handleAnalyticsObstacles(dataDir string) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p analyticsParams
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
			}
		}
		scope := p.Scope
		if scope == "" {
			scope = p.Repo
		}
		minCount := p.MinCount
		if minCount <= 0 {
			minCount = 2
		}
		results, err := bbs.QueryObstacleClusters(warmBaseDir(dataDir), scope, minCount)
		if err != nil {
			return nil, fmt.Errorf("analytics.obstacles: %w", err)
		}
		if results == nil {
			results = []bbs.ObstacleCluster{}
		}
		return results, nil
	}
}

// ---------------------------------------------------------------------------
// analytics.conventions
// ---------------------------------------------------------------------------

func handleAnalyticsConventions(dataDir string) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p analyticsParams
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
			}
		}
		scope := p.Scope
		if scope == "" {
			scope = p.Repo
		}
		minTasks := p.MinCount
		if minTasks <= 0 {
			minTasks = 3
		}
		results, err := bbs.QueryConventionEffectiveness(warmBaseDir(dataDir), scope, minTasks)
		if err != nil {
			return nil, fmt.Errorf("analytics.conventions: %w", err)
		}
		if results == nil {
			results = []bbs.ConventionEffectiveness{}
		}
		return results, nil
	}
}

// ---------------------------------------------------------------------------
// analytics.knowledge
// ---------------------------------------------------------------------------

func handleAnalyticsKnowledge(dataDir string) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p analyticsParams
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
			}
		}
		scope := p.Scope
		if scope == "" {
			scope = p.Repo
		}
		results, err := bbs.QueryKnowledgeFlow(warmBaseDir(dataDir), scope)
		if err != nil {
			return nil, fmt.Errorf("analytics.knowledge: %w", err)
		}
		if results == nil {
			results = []bbs.KnowledgeFlow{}
		}
		return results, nil
	}
}

// ---------------------------------------------------------------------------
// analytics.signatures
// ---------------------------------------------------------------------------

func handleAnalyticsSignatures(dataDir string) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p analyticsParams
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
			}
		}
		scope := p.Scope
		if scope == "" {
			scope = p.Repo
		}
		results, err := bbs.QueryWorkflowSignatures(warmBaseDir(dataDir), scope)
		if err != nil {
			return nil, fmt.Errorf("analytics.signatures: %w", err)
		}
		if results == nil {
			results = []bbs.WorkflowSignature{}
		}
		return results, nil
	}
}

// ---------------------------------------------------------------------------
// gc.run
// ---------------------------------------------------------------------------

func handleGCRun(store *tuplestore.TupleStore, dataDir string) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		cfg := bbs.GCConfig{
			WarmBaseDir: warmBaseDir(dataDir),
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		result := bbs.RunGCCycle(ctx, store, cfg)
		return result, nil
	}
}

// ---------------------------------------------------------------------------
// gc.status
// ---------------------------------------------------------------------------

func handleGCStatus(store *tuplestore.TupleStore, dataDir string) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		// Run a dry-read of the store to count tuples by lifecycle and category.
		// This gives a snapshot of what GC would operate on.
		allCat := ""
		tuples, err := store.FindAll(&allCat, nil, nil, nil, nil)
		if err != nil {
			// If empty category doesn't work, try without filter.
			tuples, err = store.FindAll(nil, nil, nil, nil, nil)
			if err != nil {
				return nil, fmt.Errorf("gc.status: %w", err)
			}
		}

		ephemeral, session, furniture := 0, 0, 0
		categories := map[string]int{}
		for _, t := range tuples {
			lc, _ := t["lifecycle"].(string)
			switch lc {
			case "ephemeral":
				ephemeral++
			case "session":
				session++
			case "furniture":
				furniture++
			}
			cat, _ := t["category"].(string)
			if cat != "" {
				categories[cat]++
			}
		}

		return map[string]interface{}{
			"total_tuples": len(tuples),
			"ephemeral":    ephemeral,
			"session":      session,
			"furniture":    furniture,
			"categories":   categories,
		}, nil
	}
}

// ---------------------------------------------------------------------------
// feedback.run
// ---------------------------------------------------------------------------

type feedbackParams struct {
	Repo  string `json:"repo"`
	Scope string `json:"scope"`
}

func handleFeedbackRun(store *tuplestore.TupleStore, dataDir string) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p feedbackParams
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
			}
		}
		scope := p.Scope
		if scope == "" {
			scope = p.Repo
		}
		cfg := bbs.FeedbackConfig{
			WarmBaseDir: warmBaseDir(dataDir),
			Scope:       scope,
		}
		result := bbs.RunFeedbackCycle(store, cfg)
		// Convert errors to strings for JSON serialization.
		errStrs := make([]string, 0, len(result.Errors))
		for _, e := range result.Errors {
			errStrs = append(errStrs, e.Error())
		}
		return map[string]interface{}{
			"conventions_pruned": result.ConventionsPruned,
			"conventions_kept":   result.ConventionsKept,
			"obstacles_surfaced": result.ObstaclesSurfaced,
			"repo_health_updated": result.RepoHealthUpdated,
			"signatures_cached":  result.SignaturesCached,
			"knowledge_flows":    result.KnowledgeFlows,
			"errors":             errStrs,
		}, nil
	}
}

// ---------------------------------------------------------------------------
// synthesis.run
// ---------------------------------------------------------------------------

type synthesisParams struct {
	TaskID string `json:"task_id"`
	Scope  string `json:"scope"`
}

func handleSynthesisRun(store *tuplestore.TupleStore) Handler {
	return func(params json.RawMessage) (interface{}, error) {
		var p synthesisParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "invalid params: " + err.Error()}
		}
		if p.TaskID == "" {
			return nil, &rpcError{Code: ErrCodeInvalidParams, Msg: "task_id is required"}
		}

		// Find all tuples for this task.
		cat := ""
		tuples, err := store.FindAll(&cat, nil, nil, nil, nil)
		if err != nil {
			tuples, err = store.FindAll(nil, nil, nil, nil, nil)
			if err != nil {
				return nil, fmt.Errorf("synthesis.run: find tuples: %w", err)
			}
		}

		// Filter by task_id.
		var taskTuples []map[string]interface{}
		for _, t := range tuples {
			tid, _ := t["task_id"].(string)
			if tid == p.TaskID {
				taskTuples = append(taskTuples, t)
			}
		}

		if len(taskTuples) == 0 {
			return map[string]interface{}{
				"task_id":     p.TaskID,
				"tuples":      0,
				"synthesized": 0,
			}, nil
		}

		scope := p.Scope
		if scope == "" {
			// Try to get scope from first tuple.
			if s, ok := taskTuples[0]["scope"].(string); ok {
				scope = s
			}
		}

		cfg := bbs.SynthesisConfig{
			Enabled: true,
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		written := bbs.Synthesize(ctx, cfg, store, taskTuples, scope)

		return map[string]interface{}{
			"task_id":     p.TaskID,
			"tuples":      len(taskTuples),
			"synthesized": written,
		}, nil
	}
}
