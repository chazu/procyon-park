// Package tuplestore archive.go implements warm-tier Parquet archival via DuckDB.
// Tuples are exported from SQLite to Parquet files, then deleted from SQLite.
// The archive-then-delete pattern ensures crash safety: worst case is duplicate
// data (Parquet written but SQLite not yet deleted), never data loss.
package tuplestore

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/marcboeker/go-duckdb"
)

// WarmBaseDir returns the default warm-tier storage root.
// Layout: <base>/warm/<YYYY-MM>/<scope>/<groupKey>.parquet
func WarmBaseDir(homeDir string) string {
	return filepath.Join(homeDir, ".procyon-park", "bbs", "warm")
}

// openDuckDB returns an ephemeral in-memory DuckDB connection.
// Callers must Close() when done.
func openDuckDB() (*sql.DB, error) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("archive: open duckdb: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("archive: ping duckdb: %w", err)
	}
	return db, nil
}

// parquetGlob builds a glob expression for DuckDB's read_parquet() function.
// If scope is empty, it wildcards across all scopes.
// The returned string is suitable for: SELECT * FROM read_parquet('<glob>')
func parquetGlob(baseDir, scope, month string) string {
	if scope == "" {
		// Wildcard across all scopes
		return filepath.Join(baseDir, month, "*", "*.parquet")
	}
	return filepath.Join(baseDir, month, scope, "*.parquet")
}

// excludedEventIdentities are event types that must NOT be archived until the
// king has acted on them. Archiving these prematurely would lose coordination signals.
var excludedEventIdentities = map[string]bool{
	"task_done":       true,
	"dismiss_request": true,
}

// isExcludedEvent returns true if the tuple is an event that should be excluded
// from archival (task_done and dismiss_request events).
func isExcludedEvent(row map[string]interface{}) bool {
	cat, _ := row["category"].(string)
	if cat != "event" {
		return false
	}
	ident, _ := row["identity"].(string)
	return excludedEventIdentities[ident]
}

// exportToParquet writes the given tuples to a Parquet file via an ephemeral DuckDB.
// It creates the export table, inserts all tuples, then uses COPY TO Parquet.
// The directory for outPath is created if it does not exist.
func exportToParquet(tuples []map[string]interface{}, outPath string) error {
	if len(tuples) == 0 {
		return nil
	}

	dir := filepath.Dir(outPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("archive: mkdir %s: %w", dir, err)
	}

	duck, err := openDuckDB()
	if err != nil {
		return err
	}
	defer duck.Close()

	// Create the export table matching the SQLite tuple schema.
	_, err = duck.Exec(`CREATE TABLE export_tuples (
		id          BIGINT,
		category    VARCHAR,
		scope       VARCHAR,
		identity    VARCHAR,
		instance    VARCHAR,
		payload     VARCHAR,
		lifecycle   VARCHAR,
		task_id     VARCHAR,
		agent_id    VARCHAR,
		created_at  VARCHAR,
		updated_at  VARCHAR,
		ttl_seconds INTEGER
	)`)
	if err != nil {
		return fmt.Errorf("archive: create export table: %w", err)
	}

	// Insert tuples into DuckDB.
	stmt, err := duck.Prepare(`INSERT INTO export_tuples VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("archive: prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, t := range tuples {
		_, err := stmt.Exec(
			t["id"],
			t["category"],
			t["scope"],
			t["identity"],
			t["instance"],
			t["payload"],
			t["lifecycle"],
			nullableString(t["task_id"]),
			nullableString(t["agent_id"]),
			t["created_at"],
			t["updated_at"],
			t["ttl_seconds"],
		)
		if err != nil {
			return fmt.Errorf("archive: insert tuple id=%v: %w", t["id"], err)
		}
	}

	// COPY TO Parquet.
	copySQL := fmt.Sprintf("COPY export_tuples TO '%s' (FORMAT PARQUET)", outPath)
	if _, err := duck.Exec(copySQL); err != nil {
		return fmt.Errorf("archive: COPY TO parquet: %w", err)
	}

	return nil
}

// ReadParquet reads tuples back from Parquet files matching a glob pattern.
// Returns column maps similar to TupleStore scan results.
func ReadParquet(globPattern string) ([]map[string]interface{}, error) {
	duck, err := openDuckDB()
	if err != nil {
		return nil, err
	}
	defer duck.Close()

	query := fmt.Sprintf("SELECT * FROM read_parquet('%s')", globPattern)
	rows, err := duck.Query(query)
	if err != nil {
		return nil, fmt.Errorf("archive: read_parquet: %w", err)
	}
	defer rows.Close()

	var results []map[string]interface{}
	for rows.Next() {
		var (
			id         int64
			category   string
			scope      string
			identity   string
			instance   string
			payload    string
			lifecycle  string
			taskID     *string
			agentID    *string
			createdAt  string
			updatedAt  string
			ttlSeconds *int
		)
		if err := rows.Scan(&id, &category, &scope, &identity, &instance,
			&payload, &lifecycle, &taskID, &agentID, &createdAt, &updatedAt, &ttlSeconds); err != nil {
			return nil, fmt.Errorf("archive: scan parquet row: %w", err)
		}
		results = append(results, map[string]interface{}{
			"id":          id,
			"category":    category,
			"scope":       scope,
			"identity":    identity,
			"instance":    instance,
			"payload":     payload,
			"lifecycle":   lifecycle,
			"task_id":     taskID,
			"agent_id":    agentID,
			"created_at":  createdAt,
			"updated_at":  updatedAt,
			"ttl_seconds": ttlSeconds,
		})
	}
	return results, nil
}

// archiveKey computes the Parquet file path for a group of tuples.
// Layout: <baseDir>/<YYYY-MM>/<scope>/<groupKey>.parquet
func archiveKey(baseDir, scope, groupKey string) string {
	month := time.Now().Format("2006-01")
	return filepath.Join(baseDir, month, scope, groupKey+".parquet")
}

// ArchiveByTaskID archives all session tuples for a given task ID, excluding
// protected event types (task_done, dismiss_request). It follows the
// archive-then-delete pattern: Parquet is written first, then SQLite rows are deleted.
func (s *TupleStore) ArchiveByTaskID(baseDir, taskID string) (archived int, err error) {
	tuples, ids, err := s.collectArchivable(
		`task_id = ? AND lifecycle = 'session'`,
		taskID,
	)
	if err != nil {
		return 0, err
	}
	if len(tuples) == 0 {
		return 0, nil
	}

	scope := scopeFromTuples(tuples)
	outPath := archiveKey(baseDir, scope, "task-"+taskID)

	// Archive-then-delete: write Parquet BEFORE deleting from SQLite.
	if err := exportToParquet(tuples, outPath); err != nil {
		return 0, err
	}
	if err := s.deleteTuplesByID(ids); err != nil {
		return 0, err
	}
	return len(tuples), nil
}

// ArchiveByAgentID archives all session tuples for a given agent ID, excluding
// protected event types (task_done, dismiss_request). It follows the
// archive-then-delete pattern.
func (s *TupleStore) ArchiveByAgentID(baseDir, agentID string) (archived int, err error) {
	tuples, ids, err := s.collectArchivable(
		`agent_id = ? AND lifecycle = 'session'`,
		agentID,
	)
	if err != nil {
		return 0, err
	}
	if len(tuples) == 0 {
		return 0, nil
	}

	scope := scopeFromTuples(tuples)
	outPath := archiveKey(baseDir, scope, "agent-"+agentID)

	if err := exportToParquet(tuples, outPath); err != nil {
		return 0, err
	}
	if err := s.deleteTuplesByID(ids); err != nil {
		return 0, err
	}
	return len(tuples), nil
}

// collectArchivable queries tuples matching the given WHERE clause and arg,
// then filters out excluded events. Returns the filtered tuples and their IDs.
func (s *TupleStore) collectArchivable(whereClause string, arg interface{}) ([]map[string]interface{}, []int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cols := `id, category, scope, identity, instance, payload, lifecycle,
		task_id, agent_id, created_at, updated_at, ttl_seconds`
	query := "SELECT " + cols + " FROM tuples WHERE " + whereClause + " ORDER BY id"

	rows, err := s.db.Query(query, arg)
	if err != nil {
		return nil, nil, fmt.Errorf("archive: query tuples: %w", err)
	}
	defer rows.Close()

	var tuples []map[string]interface{}
	var ids []int64
	for rows.Next() {
		row, err := scanTupleRow(rows)
		if err != nil {
			return nil, nil, err
		}
		if isExcludedEvent(row) {
			continue
		}
		tuples = append(tuples, row)
		ids = append(ids, row["id"].(int64))
	}
	return tuples, ids, nil
}

// deleteTuplesByID deletes tuples by their IDs within a single transaction.
func (s *TupleStore) deleteTuplesByID(ids []int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(ids) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("archive: begin delete tx: %w", err)
	}

	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := "DELETE FROM tuples WHERE id IN (" + strings.Join(placeholders, ",") + ")"
	if _, err := tx.Exec(query, args...); err != nil {
		tx.Rollback()
		return fmt.Errorf("archive: delete tuples: %w", err)
	}
	return tx.Commit()
}

// scopeFromTuples extracts the most common scope from a set of tuples.
// Falls back to "unknown" if tuples are empty.
func scopeFromTuples(tuples []map[string]interface{}) string {
	counts := make(map[string]int)
	for _, t := range tuples {
		s, _ := t["scope"].(string)
		if s != "" {
			counts[s]++
		}
	}
	best := "unknown"
	bestCount := 0
	for s, c := range counts {
		if c > bestCount {
			best = s
			bestCount = c
		}
	}
	return best
}

// nullableString extracts a *string from an interface{} value,
// returning nil for SQL NULL values.
func nullableString(v interface{}) *string {
	if v == nil {
		return nil
	}
	if sp, ok := v.(*string); ok {
		return sp
	}
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}
