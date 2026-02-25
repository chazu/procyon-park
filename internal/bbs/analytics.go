// Package bbs analytics.go implements 6 analytical queries over warm-tier
// Parquet files using ephemeral in-memory DuckDB instances.
//
// Each query opens a fresh DuckDB, builds a read_parquet(glob) expression,
// executes SQL, and returns typed Go structs. No persistent state is held.
package bbs

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"

	_ "github.com/marcboeker/go-duckdb"
)

// isNoFilesError returns true if the error is DuckDB's "No files found" error,
// which happens when the glob pattern matches nothing.
func isNoFilesError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "No files found")
}

// ---------------------------------------------------------------------------
// Result Types
// ---------------------------------------------------------------------------

// AgentPerformance holds per-scope agent activity metrics.
type AgentPerformance struct {
	Scope                string  `json:"scope"`
	ObstacleCount        int64   `json:"obstacle_count"`
	ArtifactCount        int64   `json:"artifact_count"`
	DistinctAgents       int64   `json:"distinct_agents"`
	ArtifactObstacleRate float64 `json:"artifact_obstacle_rate"` // artifacts / obstacles, 0 if no obstacles
}

// TimeToFirstObstacle holds the average seconds between task start and first
// obstacle per scope.
type TimeToFirstObstacle struct {
	Scope      string  `json:"scope"`
	AvgSeconds float64 `json:"avg_seconds"`
	TaskCount  int64   `json:"task_count"`
}

// ConventionEffectiveness holds before/after success rates around a convention
// introduction date.
type ConventionEffectiveness struct {
	ConventionID    string  `json:"convention_id"`
	BeforeRate      float64 `json:"before_rate"`      // artifacts / (artifacts + obstacles) before
	AfterRate       float64 `json:"after_rate"`        // artifacts / (artifacts + obstacles) after
	BeforeTaskCount int64   `json:"before_task_count"`
	AfterTaskCount  int64   `json:"after_task_count"`
}

// ObstacleCluster holds a group of similar obstacles.
type ObstacleCluster struct {
	Description    string `json:"description"`
	Occurrences    int64  `json:"occurrences"`
	DistinctAgents int64  `json:"distinct_agents"`
	FirstSeen      string `json:"first_seen"`
	LastSeen       string `json:"last_seen"`
}

// WorkflowSignature holds the dominant tuple emission pattern for a task.
type WorkflowSignature struct {
	TaskID   string `json:"task_id"`
	Pattern  string `json:"pattern"`  // ordered category sequence, e.g. "claim,fact,artifact,event"
	Outcome  string `json:"outcome"`  // "success" if task_done event exists, "incomplete" otherwise
	AgentID  string `json:"agent_id"`
}

// KnowledgeFlow traces cross-agent knowledge propagation.
type KnowledgeFlow struct {
	SourceAgent string `json:"source_agent"`
	TargetAgent string `json:"target_agent"`
	Category    string `json:"category"`
	Identity    string `json:"identity"`
	SourceTask  string `json:"source_task"`
	TargetTask  string `json:"target_task"`
}

// ---------------------------------------------------------------------------
// DuckDB Helpers
// ---------------------------------------------------------------------------

// openAnalyticsDuckDB returns an ephemeral in-memory DuckDB for analytics.
func openAnalyticsDuckDB() (*sql.DB, error) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("analytics: open duckdb: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("analytics: ping duckdb: %w", err)
	}
	return db, nil
}

// analyticsGlob builds a Parquet glob for the warm tier base directory.
// If scope is empty, wildcards across all scopes and months.
func analyticsGlob(baseDir, scope string) string {
	if scope == "" {
		return filepath.Join(baseDir, "*", "*", "*.parquet")
	}
	return filepath.Join(baseDir, "*", scope, "*.parquet")
}

// ---------------------------------------------------------------------------
// Query 1: Agent Performance
// ---------------------------------------------------------------------------

// QueryAgentPerformance groups tuples by scope, counts obstacles and artifacts,
// counts distinct agents, and computes artifact-to-obstacle ratio.
func QueryAgentPerformance(baseDir, scope string) ([]AgentPerformance, error) {
	duck, err := openAnalyticsDuckDB()
	if err != nil {
		return nil, err
	}
	defer duck.Close()

	glob := analyticsGlob(baseDir, scope)
	query := fmt.Sprintf(`
		SELECT
			scope,
			COUNT(*) FILTER (WHERE category = 'obstacle') AS obstacle_count,
			COUNT(*) FILTER (WHERE category = 'artifact') AS artifact_count,
			COUNT(DISTINCT agent_id) AS distinct_agents
		FROM read_parquet('%s')
		GROUP BY scope
		ORDER BY scope
	`, glob)

	rows, err := duck.Query(query)
	if isNoFilesError(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("analytics: agent performance: %w", err)
	}
	defer rows.Close()

	var results []AgentPerformance
	for rows.Next() {
		var r AgentPerformance
		if err := rows.Scan(&r.Scope, &r.ObstacleCount, &r.ArtifactCount, &r.DistinctAgents); err != nil {
			return nil, fmt.Errorf("analytics: scan agent performance: %w", err)
		}
		if r.ObstacleCount > 0 {
			r.ArtifactObstacleRate = float64(r.ArtifactCount) / float64(r.ObstacleCount)
		}
		results = append(results, r)
	}
	return results, nil
}

// ---------------------------------------------------------------------------
// Query 2: Time To First Obstacle
// ---------------------------------------------------------------------------

// QueryTimeToFirstObstacle computes the average seconds between the first tuple
// for a task (task start) and the first obstacle tuple per scope.
// Only tasks that have at least one obstacle are included.
func QueryTimeToFirstObstacle(baseDir, scope string) ([]TimeToFirstObstacle, error) {
	duck, err := openAnalyticsDuckDB()
	if err != nil {
		return nil, err
	}
	defer duck.Close()

	glob := analyticsGlob(baseDir, scope)
	query := fmt.Sprintf(`
		WITH task_starts AS (
			SELECT task_id, scope, MIN(created_at) AS start_time
			FROM read_parquet('%s')
			WHERE task_id IS NOT NULL AND task_id != ''
			GROUP BY task_id, scope
		),
		first_obstacles AS (
			SELECT task_id, scope, MIN(created_at) AS obstacle_time
			FROM read_parquet('%s')
			WHERE category = 'obstacle'
			  AND task_id IS NOT NULL AND task_id != ''
			GROUP BY task_id, scope
		)
		SELECT
			ts.scope,
			AVG(EPOCH(CAST(fo.obstacle_time AS TIMESTAMP) - CAST(ts.start_time AS TIMESTAMP))) AS avg_seconds,
			COUNT(*) AS task_count
		FROM task_starts ts
		JOIN first_obstacles fo ON ts.task_id = fo.task_id AND ts.scope = fo.scope
		GROUP BY ts.scope
		ORDER BY ts.scope
	`, glob, glob)

	rows, err := duck.Query(query)
	if isNoFilesError(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("analytics: time to first obstacle: %w", err)
	}
	defer rows.Close()

	var results []TimeToFirstObstacle
	for rows.Next() {
		var r TimeToFirstObstacle
		if err := rows.Scan(&r.Scope, &r.AvgSeconds, &r.TaskCount); err != nil {
			return nil, fmt.Errorf("analytics: scan time to first obstacle: %w", err)
		}
		results = append(results, r)
	}
	return results, nil
}

// ---------------------------------------------------------------------------
// Query 3: Convention Effectiveness
// ---------------------------------------------------------------------------

// QueryConventionEffectiveness computes before/after success rates around
// a convention's introduction date. Success rate = artifacts / (artifacts + obstacles).
// Only conventions with at least minTasks tasks in both periods are included.
func QueryConventionEffectiveness(baseDir, scope string, minTasks int) ([]ConventionEffectiveness, error) {
	duck, err := openAnalyticsDuckDB()
	if err != nil {
		return nil, err
	}
	defer duck.Close()

	glob := analyticsGlob(baseDir, scope)
	query := fmt.Sprintf(`
		WITH conventions AS (
			SELECT identity AS convention_id, MIN(created_at) AS intro_date
			FROM read_parquet('%s')
			WHERE category = 'convention'
			GROUP BY identity
		),
		task_outcomes AS (
			SELECT
				task_id,
				scope,
				MIN(created_at) AS task_start,
				COUNT(*) FILTER (WHERE category = 'artifact') AS artifacts,
				COUNT(*) FILTER (WHERE category = 'obstacle') AS obstacles
			FROM read_parquet('%s')
			WHERE task_id IS NOT NULL AND task_id != ''
			GROUP BY task_id, scope
		)
		SELECT
			c.convention_id,
			COALESCE(
				SUM(CASE WHEN t.task_start < c.intro_date THEN t.artifacts ELSE 0 END) * 1.0 /
				NULLIF(SUM(CASE WHEN t.task_start < c.intro_date THEN t.artifacts + t.obstacles ELSE 0 END), 0),
				0
			) AS before_rate,
			COALESCE(
				SUM(CASE WHEN t.task_start >= c.intro_date THEN t.artifacts ELSE 0 END) * 1.0 /
				NULLIF(SUM(CASE WHEN t.task_start >= c.intro_date THEN t.artifacts + t.obstacles ELSE 0 END), 0),
				0
			) AS after_rate,
			COUNT(DISTINCT CASE WHEN t.task_start < c.intro_date THEN t.task_id END) AS before_task_count,
			COUNT(DISTINCT CASE WHEN t.task_start >= c.intro_date THEN t.task_id END) AS after_task_count
		FROM conventions c
		CROSS JOIN task_outcomes t
		GROUP BY c.convention_id
		HAVING before_task_count >= %d AND after_task_count >= %d
		ORDER BY c.convention_id
	`, glob, glob, minTasks, minTasks)

	rows, err := duck.Query(query)
	if isNoFilesError(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("analytics: convention effectiveness: %w", err)
	}
	defer rows.Close()

	var results []ConventionEffectiveness
	for rows.Next() {
		var r ConventionEffectiveness
		if err := rows.Scan(&r.ConventionID, &r.BeforeRate, &r.AfterRate,
			&r.BeforeTaskCount, &r.AfterTaskCount); err != nil {
			return nil, fmt.Errorf("analytics: scan convention effectiveness: %w", err)
		}
		results = append(results, r)
	}
	return results, nil
}

// ---------------------------------------------------------------------------
// Query 4: Obstacle Clusters
// ---------------------------------------------------------------------------

// QueryObstacleClusters groups obstacles by their identity (description),
// returning clusters with minOccurrences or more occurrences.
func QueryObstacleClusters(baseDir, scope string, minOccurrences int) ([]ObstacleCluster, error) {
	duck, err := openAnalyticsDuckDB()
	if err != nil {
		return nil, err
	}
	defer duck.Close()

	glob := analyticsGlob(baseDir, scope)
	query := fmt.Sprintf(`
		SELECT
			identity AS description,
			COUNT(*) AS occurrences,
			COUNT(DISTINCT agent_id) AS distinct_agents,
			MIN(created_at) AS first_seen,
			MAX(created_at) AS last_seen
		FROM read_parquet('%s')
		WHERE category = 'obstacle'
		GROUP BY identity
		HAVING occurrences >= %d
		ORDER BY occurrences DESC
	`, glob, minOccurrences)

	rows, err := duck.Query(query)
	if isNoFilesError(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("analytics: obstacle clusters: %w", err)
	}
	defer rows.Close()

	var results []ObstacleCluster
	for rows.Next() {
		var r ObstacleCluster
		if err := rows.Scan(&r.Description, &r.Occurrences, &r.DistinctAgents,
			&r.FirstSeen, &r.LastSeen); err != nil {
			return nil, fmt.Errorf("analytics: scan obstacle cluster: %w", err)
		}
		results = append(results, r)
	}
	return results, nil
}

// ---------------------------------------------------------------------------
// Query 5: Workflow Signatures
// ---------------------------------------------------------------------------

// QueryWorkflowSignatures identifies the dominant tuple emission pattern
// per task using ROW_NUMBER window functions. Tasks with a task_done event
// are marked "success"; others are "incomplete".
func QueryWorkflowSignatures(baseDir, scope string) ([]WorkflowSignature, error) {
	duck, err := openAnalyticsDuckDB()
	if err != nil {
		return nil, err
	}
	defer duck.Close()

	glob := analyticsGlob(baseDir, scope)
	query := fmt.Sprintf(`
		WITH ordered AS (
			SELECT
				task_id,
				category,
				agent_id,
				identity,
				ROW_NUMBER() OVER (PARTITION BY task_id ORDER BY created_at, id) AS seq
			FROM read_parquet('%s')
			WHERE task_id IS NOT NULL AND task_id != ''
		),
		patterns AS (
			SELECT
				task_id,
				STRING_AGG(category, ',' ORDER BY seq) AS pattern,
				FIRST(agent_id) AS agent_id
			FROM ordered
			GROUP BY task_id
		),
		outcomes AS (
			SELECT DISTINCT task_id
			FROM ordered
			WHERE category = 'event' AND identity = 'task_done'
		)
		SELECT
			p.task_id,
			p.pattern,
			CASE WHEN o.task_id IS NOT NULL THEN 'success' ELSE 'incomplete' END AS outcome,
			COALESCE(p.agent_id, '') AS agent_id
		FROM patterns p
		LEFT JOIN outcomes o ON p.task_id = o.task_id
		ORDER BY p.task_id
	`, glob)

	rows, err := duck.Query(query)
	if isNoFilesError(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("analytics: workflow signatures: %w", err)
	}
	defer rows.Close()

	var results []WorkflowSignature
	for rows.Next() {
		var r WorkflowSignature
		if err := rows.Scan(&r.TaskID, &r.Pattern, &r.Outcome, &r.AgentID); err != nil {
			return nil, fmt.Errorf("analytics: scan workflow signature: %w", err)
		}
		results = append(results, r)
	}
	return results, nil
}

// ---------------------------------------------------------------------------
// Query 6: Knowledge Flow
// ---------------------------------------------------------------------------

// QueryKnowledgeFlow traces cross-agent knowledge propagation by finding
// fact/convention tuples written by one agent that appear in the same scope
// as tasks completed by other agents (after the knowledge was written).
func QueryKnowledgeFlow(baseDir, scope string) ([]KnowledgeFlow, error) {
	duck, err := openAnalyticsDuckDB()
	if err != nil {
		return nil, err
	}
	defer duck.Close()

	glob := analyticsGlob(baseDir, scope)
	query := fmt.Sprintf(`
		WITH knowledge AS (
			SELECT
				agent_id AS source_agent,
				category,
				identity,
				scope,
				task_id AS source_task,
				created_at AS knowledge_time
			FROM read_parquet('%s')
			WHERE category IN ('fact', 'convention')
			  AND agent_id IS NOT NULL AND agent_id != ''
		),
		task_completions AS (
			SELECT
				agent_id AS target_agent,
				task_id AS target_task,
				scope,
				created_at AS completion_time
			FROM read_parquet('%s')
			WHERE category = 'event' AND identity = 'task_done'
			  AND agent_id IS NOT NULL AND agent_id != ''
		)
		SELECT
			k.source_agent,
			tc.target_agent,
			k.category,
			k.identity,
			COALESCE(k.source_task, '') AS source_task,
			COALESCE(tc.target_task, '') AS target_task
		FROM knowledge k
		JOIN task_completions tc
			ON k.scope = tc.scope
			AND k.source_agent != tc.target_agent
			AND CAST(k.knowledge_time AS TIMESTAMP) < CAST(tc.completion_time AS TIMESTAMP)
		ORDER BY k.source_agent, tc.target_agent, k.identity
	`, glob, glob)

	rows, err := duck.Query(query)
	if isNoFilesError(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("analytics: knowledge flow: %w", err)
	}
	defer rows.Close()

	var results []KnowledgeFlow
	for rows.Next() {
		var r KnowledgeFlow
		if err := rows.Scan(&r.SourceAgent, &r.TargetAgent, &r.Category,
			&r.Identity, &r.SourceTask, &r.TargetTask); err != nil {
			return nil, fmt.Errorf("analytics: scan knowledge flow: %w", err)
		}
		results = append(results, r)
	}
	return results, nil
}
