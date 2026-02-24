// Package tuplestore implements a SQLite-backed tuple store with FTS5 full-text
// search for the procyon-park tuplespace. This is the persistence backbone for
// the BBS (Bulletin Board System) coordination layer.
package tuplestore

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

// TupleStore provides SQLite CRUD operations for tuples with FTS5 payload search.
type TupleStore struct {
	db    *sql.DB
	mu    sync.Mutex
	stmts map[string]*sql.Stmt // prepared statement cache
}

// NewStore opens (or creates) a SQLite database at dbPath, runs schema migrations,
// and returns a ready-to-use TupleStore.
func NewStore(dbPath string) (*TupleStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("tuplestore: open %s: %w", dbPath, err)
	}
	return initStore(db)
}

// NewMemoryStore creates an in-memory TupleStore.
func NewMemoryStore() (*TupleStore, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("tuplestore: open memory: %w", err)
	}
	return initStore(db)
}

func initStore(db *sql.DB) (*TupleStore, error) {
	db.SetMaxOpenConns(1)

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("tuplestore: %s: %w", p, err)
		}
	}

	s := &TupleStore{db: db, stmts: make(map[string]*sql.Stmt)}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes all prepared statements and the database connection.
func (s *TupleStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, stmt := range s.stmts {
		stmt.Close()
	}
	s.stmts = nil
	return s.db.Close()
}

// ---------------------------------------------------------------------------
// Schema Migrations
// ---------------------------------------------------------------------------

func (s *TupleStore) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("tuplestore: create migrations table: %w", err)
	}

	migrations := []struct {
		version int
		sql     string
	}{
		{1, migrationV1},
		{2, migrationV2},
	}

	for _, m := range migrations {
		var count int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = ?", m.version).Scan(&count); err != nil {
			return fmt.Errorf("tuplestore: check migration %d: %w", m.version, err)
		}
		if count > 0 {
			continue
		}

		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("tuplestore: begin migration %d: %w", m.version, err)
		}
		if _, err := tx.Exec(m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("tuplestore: migration %d: %w", m.version, err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations (version) VALUES (?)", m.version); err != nil {
			tx.Rollback()
			return fmt.Errorf("tuplestore: record migration %d: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("tuplestore: commit migration %d: %w", m.version, err)
		}
	}
	return nil
}

const migrationV1 = `
CREATE TABLE IF NOT EXISTS tuples (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  category    TEXT NOT NULL,
  scope       TEXT NOT NULL DEFAULT '',
  identity    TEXT NOT NULL DEFAULT '',
  instance    TEXT NOT NULL DEFAULT 'local',
  payload     TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(payload)),
  lifecycle   TEXT NOT NULL DEFAULT 'session'
              CHECK(lifecycle IN ('furniture','session','ephemeral')),
  task_id     TEXT,
  agent_id    TEXT,
  created_at  DATETIME NOT NULL DEFAULT (datetime('now')),
  updated_at  DATETIME NOT NULL DEFAULT (datetime('now')),
  ttl_seconds INTEGER
);
CREATE INDEX IF NOT EXISTS idx_tuples_category ON tuples(category);
CREATE INDEX IF NOT EXISTS idx_tuples_scope ON tuples(scope);
CREATE INDEX IF NOT EXISTS idx_tuples_identity ON tuples(identity);
CREATE INDEX IF NOT EXISTS idx_tuples_instance ON tuples(instance);
CREATE INDEX IF NOT EXISTS idx_tuples_lifecycle ON tuples(lifecycle);
CREATE INDEX IF NOT EXISTS idx_tuples_task_id ON tuples(task_id);
CREATE INDEX IF NOT EXISTS idx_tuples_created_at ON tuples(created_at);
CREATE INDEX IF NOT EXISTS idx_tuples_cat_scope ON tuples(category, scope);
`

const migrationV2 = `
CREATE VIRTUAL TABLE IF NOT EXISTS tuples_fts USING fts5(
  payload, content='tuples', content_rowid='id'
);
CREATE TRIGGER IF NOT EXISTS tuples_ai AFTER INSERT ON tuples BEGIN
  INSERT INTO tuples_fts(rowid, payload) VALUES (new.id, new.payload);
END;
CREATE TRIGGER IF NOT EXISTS tuples_ad AFTER DELETE ON tuples BEGIN
  INSERT INTO tuples_fts(tuples_fts, rowid, payload) VALUES ('delete', old.id, old.payload);
END;
CREATE TRIGGER IF NOT EXISTS tuples_au AFTER UPDATE ON tuples BEGIN
  INSERT INTO tuples_fts(tuples_fts, rowid, payload) VALUES ('delete', old.id, old.payload);
  INSERT INTO tuples_fts(rowid, payload) VALUES (new.id, new.payload);
END;
INSERT INTO tuples_fts(rowid, payload) SELECT id, payload FROM tuples;
`

// ---------------------------------------------------------------------------
// CRUD Operations
// ---------------------------------------------------------------------------

// Insert adds a tuple and returns its row ID.
func (s *TupleStore) Insert(category, scope, identity, instance, payload, lifecycle string,
	taskID, agentID *string, ttl *int) (int64, error) {

	s.mu.Lock()
	defer s.mu.Unlock()

	stmt, err := s.prepare("insert", `INSERT INTO tuples
		(category, scope, identity, instance, payload, lifecycle, task_id, agent_id, ttl_seconds)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}

	result, err := stmt.Exec(category, scope, identity, instance, payload, lifecycle,
		taskID, agentID, ttl)
	if err != nil {
		return 0, fmt.Errorf("tuplestore: insert: %w", err)
	}
	return result.LastInsertId()
}

// FindOne returns the oldest tuple matching the pattern, or nil if none found.
// Nil pattern fields are wildcards.
func (s *TupleStore) FindOne(category, scope, identity, instance, payloadSearch *string) (map[string]interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	query, args := s.buildSelect(category, scope, identity, instance, payloadSearch)
	query += " ORDER BY tuples.id LIMIT 1"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("tuplestore: findOne: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, nil
	}
	return scanTupleRow(rows)
}

// FindAndDelete atomically finds the oldest matching tuple, deletes it, and returns it.
// Returns nil if no match found.
func (s *TupleStore) FindAndDelete(category, scope, identity, instance, payloadSearch *string) (map[string]interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Find the matching tuple first
	query, args := s.buildSelect(category, scope, identity, instance, payloadSearch)
	query += " ORDER BY tuples.id LIMIT 1"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("tuplestore: findAndDelete query: %w", err)
	}

	if !rows.Next() {
		rows.Close()
		return nil, nil
	}
	row, err := scanTupleRow(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}

	// Delete it
	id := row["id"].(int64)
	delStmt, err := s.prepare("delete", "DELETE FROM tuples WHERE id = ?")
	if err != nil {
		return nil, err
	}
	if _, err := delStmt.Exec(id); err != nil {
		return nil, fmt.Errorf("tuplestore: findAndDelete delete: %w", err)
	}

	return row, nil
}

// FindAll returns all tuples matching the pattern.
func (s *TupleStore) FindAll(category, scope, identity, instance, payloadSearch *string) ([]map[string]interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	query, args := s.buildSelect(category, scope, identity, instance, payloadSearch)
	query += " ORDER BY tuples.id"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("tuplestore: findAll: %w", err)
	}
	defer rows.Close()

	var results []map[string]interface{}
	for rows.Next() {
		row, err := scanTupleRow(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	return results, nil
}

// Delete removes a tuple by ID. Returns true if a row was deleted.
func (s *TupleStore) Delete(id int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stmt, err := s.prepare("delete", "DELETE FROM tuples WHERE id = ?")
	if err != nil {
		return false, err
	}

	result, err := stmt.Exec(id)
	if err != nil {
		return false, fmt.Errorf("tuplestore: delete: %w", err)
	}
	n, _ := result.RowsAffected()
	return n > 0, nil
}

// DeleteByPattern deletes all tuples matching the pattern (excluding furniture).
// Returns the count of deleted rows.
func (s *TupleStore) DeleteByPattern(category, scope, identity, instance *string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	where, args := buildWhere(category, scope, identity, instance, nil)
	query := "DELETE FROM tuples WHERE " + where
	result, err := s.db.Exec(query, args...)
	if err != nil {
		return 0, fmt.Errorf("tuplestore: deleteByPattern: %w", err)
	}
	return result.RowsAffected()
}

// Count returns the number of tuples matching the pattern.
func (s *TupleStore) Count(category, scope, identity, instance, payloadSearch *string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	where, args := buildWhere(category, scope, identity, instance, payloadSearch)
	var query string
	if payloadSearch != nil {
		query = "SELECT COUNT(*) FROM tuples JOIN tuples_fts ON tuples.id = tuples_fts.rowid WHERE " + where
	} else {
		query = "SELECT COUNT(*) FROM tuples WHERE " + where
	}

	var count int64
	if err := s.db.QueryRow(query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("tuplestore: count: %w", err)
	}
	return count, nil
}

// ---------------------------------------------------------------------------
// Query Building
// ---------------------------------------------------------------------------

func (s *TupleStore) buildSelect(category, scope, identity, instance, payloadSearch *string) (string, []interface{}) {
	cols := `tuples.id, tuples.category, tuples.scope, tuples.identity, tuples.instance,
		tuples.payload, tuples.lifecycle, tuples.task_id, tuples.agent_id,
		tuples.created_at, tuples.updated_at, tuples.ttl_seconds`

	where, args := buildWhere(category, scope, identity, instance, payloadSearch)

	if payloadSearch != nil {
		return "SELECT " + cols + " FROM tuples JOIN tuples_fts ON tuples.id = tuples_fts.rowid WHERE " + where, args
	}
	return "SELECT " + cols + " FROM tuples WHERE " + where, args
}

func buildWhere(category, scope, identity, instance, payloadSearch *string) (string, []interface{}) {
	var conds []string
	var args []interface{}

	if payloadSearch != nil {
		conds = append(conds, "tuples_fts.payload MATCH ?")
		args = append(args, *payloadSearch)
	}
	if category != nil {
		conds = append(conds, "tuples.category = ?")
		args = append(args, *category)
	}
	if scope != nil {
		conds = append(conds, "tuples.scope = ?")
		args = append(args, *scope)
	}
	if identity != nil {
		conds = append(conds, "tuples.identity = ?")
		args = append(args, *identity)
	}
	if instance != nil {
		conds = append(conds, "tuples.instance = ?")
		args = append(args, *instance)
	}

	if len(conds) == 0 {
		return "1=1", nil
	}
	return strings.Join(conds, " AND "), args
}

// ---------------------------------------------------------------------------
// Row Scanning
// ---------------------------------------------------------------------------

func scanTupleRow(rows *sql.Rows) (map[string]interface{}, error) {
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
		return nil, fmt.Errorf("tuplestore: scan: %w", err)
	}

	row := map[string]interface{}{
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
	}
	return row, nil
}

// ---------------------------------------------------------------------------
// Prepared Statement Cache
// ---------------------------------------------------------------------------

func (s *TupleStore) prepare(name, query string) (*sql.Stmt, error) {
	if stmt, ok := s.stmts[name]; ok {
		return stmt, nil
	}
	stmt, err := s.db.Prepare(query)
	if err != nil {
		return nil, fmt.Errorf("tuplestore: prepare %s: %w", name, err)
	}
	s.stmts[name] = stmt
	return stmt, nil
}
