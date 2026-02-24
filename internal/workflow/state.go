package workflow

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// GateState persists the state of an in-progress gate step for crash recovery.
type GateState struct {
	InstanceID string    `json:"instance_id"`
	StepIndex  int       `json:"step_index"`
	StartedAt  time.Time `json:"started_at"`
	State      string    `json:"state"` // "waiting", "approved", "rejected", "timed_out"
	PromptSent bool      `json:"prompt_sent"`
}

// Store provides SQLite-backed CRUD for workflow instances and gate state.
type Store struct {
	db *sql.DB
	mu sync.Mutex
}

// NewStore opens (or creates) a SQLite database at dbPath with the workflow schema.
func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("workflow/state: open %s: %w", dbPath, err)
	}
	return initStore(db)
}

// NewMemoryStore creates an in-memory Store for testing.
func NewMemoryStore() (*Store, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("workflow/state: open memory: %w", err)
	}
	return initStore(db)
}

func initStore(db *sql.DB) (*Store, error) {
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
			return nil, fmt.Errorf("workflow/state: %s: %w", p, err)
		}
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("workflow/state: migrate: %w", err)
	}
	return nil
}

const schema = `
CREATE TABLE IF NOT EXISTS workflow_instances (
	repo_name     TEXT NOT NULL,
	instance_id   TEXT NOT NULL,
	data          TEXT NOT NULL CHECK(json_valid(data)),
	created_at    DATETIME NOT NULL DEFAULT (datetime('now')),
	updated_at    DATETIME NOT NULL DEFAULT (datetime('now')),
	PRIMARY KEY (repo_name, instance_id)
);

CREATE TABLE IF NOT EXISTS workflow_instances_idx (
	status        TEXT NOT NULL,
	repo_name     TEXT NOT NULL,
	instance_id   TEXT NOT NULL,
	PRIMARY KEY (status, repo_name, instance_id),
	FOREIGN KEY (repo_name, instance_id) REFERENCES workflow_instances(repo_name, instance_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS workflow_gate_state (
	instance_id   TEXT NOT NULL,
	step_index    INTEGER NOT NULL,
	started_at    DATETIME NOT NULL,
	state         TEXT NOT NULL DEFAULT 'waiting',
	prompt_sent   INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (instance_id, step_index)
);
`

// CreateInstance inserts a new workflow instance. The instance ID and repo_name
// are used as the composite primary key.
func (s *Store) CreateInstance(inst *Instance) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(inst)
	if err != nil {
		return fmt.Errorf("workflow/state: marshal instance: %w", err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("workflow/state: begin create: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.Exec(
		`INSERT INTO workflow_instances (repo_name, instance_id, data, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)`,
		inst.RepoName, inst.ID, string(data), inst.StartedAt.UTC(), time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("workflow/state: insert instance: %w", err)
	}

	_, err = tx.Exec(
		`INSERT INTO workflow_instances_idx (status, repo_name, instance_id)
		 VALUES (?, ?, ?)`,
		string(inst.Status), inst.RepoName, inst.ID,
	)
	if err != nil {
		return fmt.Errorf("workflow/state: insert index: %w", err)
	}

	return tx.Commit()
}

// UpdateInstance updates an existing workflow instance. The status index is
// maintained atomically within the same transaction.
func (s *Store) UpdateInstance(inst *Instance) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(inst)
	if err != nil {
		return fmt.Errorf("workflow/state: marshal instance: %w", err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("workflow/state: begin update: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.Exec(
		`UPDATE workflow_instances SET data = ?, updated_at = ? WHERE repo_name = ? AND instance_id = ?`,
		string(data), time.Now().UTC(), inst.RepoName, inst.ID,
	)
	if err != nil {
		return fmt.Errorf("workflow/state: update instance: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("workflow/state: instance not found: %s/%s", inst.RepoName, inst.ID)
	}

	// Atomic index maintenance: delete old status entry, insert new one.
	_, err = tx.Exec(
		`DELETE FROM workflow_instances_idx WHERE repo_name = ? AND instance_id = ?`,
		inst.RepoName, inst.ID,
	)
	if err != nil {
		return fmt.Errorf("workflow/state: delete old index: %w", err)
	}

	_, err = tx.Exec(
		`INSERT INTO workflow_instances_idx (status, repo_name, instance_id) VALUES (?, ?, ?)`,
		string(inst.Status), inst.RepoName, inst.ID,
	)
	if err != nil {
		return fmt.Errorf("workflow/state: insert new index: %w", err)
	}

	return tx.Commit()
}

// GetInstance retrieves a workflow instance by repo name and instance ID.
// Returns nil, nil if not found.
func (s *Store) GetInstance(repoName, instanceID string) (*Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var data string
	err := s.db.QueryRow(
		`SELECT data FROM workflow_instances WHERE repo_name = ? AND instance_id = ?`,
		repoName, instanceID,
	).Scan(&data)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("workflow/state: get instance: %w", err)
	}

	var inst Instance
	if err := json.Unmarshal([]byte(data), &inst); err != nil {
		return nil, fmt.Errorf("workflow/state: unmarshal instance: %w", err)
	}
	return &inst, nil
}

// ListInstances returns workflow instances matching the optional filters.
// If status is empty, all statuses are returned. If repoName is empty, all repos.
func (s *Store) ListInstances(status InstanceStatus, repoName string) ([]*Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var query string
	var args []interface{}

	if status != "" {
		// Use the status index for filtered queries.
		query = `SELECT wi.data FROM workflow_instances wi
			JOIN workflow_instances_idx idx
			ON wi.repo_name = idx.repo_name AND wi.instance_id = idx.instance_id
			WHERE idx.status = ?`
		args = append(args, string(status))
		if repoName != "" {
			query += " AND idx.repo_name = ?"
			args = append(args, repoName)
		}
		query += " ORDER BY wi.created_at"
	} else {
		query = `SELECT data FROM workflow_instances`
		if repoName != "" {
			query += " WHERE repo_name = ?"
			args = append(args, repoName)
		}
		query += " ORDER BY created_at"
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("workflow/state: list instances: %w", err)
	}
	defer rows.Close()

	var instances []*Instance
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, fmt.Errorf("workflow/state: scan instance: %w", err)
		}
		var inst Instance
		if err := json.Unmarshal([]byte(data), &inst); err != nil {
			return nil, fmt.Errorf("workflow/state: unmarshal instance: %w", err)
		}
		instances = append(instances, &inst)
	}
	return instances, nil
}

// DeleteInstance removes a workflow instance and its index entry.
func (s *Store) DeleteInstance(repoName, instanceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Foreign key cascade handles the index deletion.
	result, err := s.db.Exec(
		`DELETE FROM workflow_instances WHERE repo_name = ? AND instance_id = ?`,
		repoName, instanceID,
	)
	if err != nil {
		return fmt.Errorf("workflow/state: delete instance: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("workflow/state: instance not found: %s/%s", repoName, instanceID)
	}
	return nil
}

// SaveGateState persists gate state for crash recovery. Uses INSERT OR REPLACE
// to handle both initial creation and updates.
func (s *Store) SaveGateState(gs *GateState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	promptSent := 0
	if gs.PromptSent {
		promptSent = 1
	}

	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO workflow_gate_state (instance_id, step_index, started_at, state, prompt_sent)
		 VALUES (?, ?, ?, ?, ?)`,
		gs.InstanceID, gs.StepIndex, gs.StartedAt.UTC(), gs.State, promptSent,
	)
	if err != nil {
		return fmt.Errorf("workflow/state: save gate state: %w", err)
	}
	return nil
}

// GetGateState retrieves gate state for a specific instance and step.
// Returns nil, nil if not found.
func (s *Store) GetGateState(instanceID string, stepIndex int) (*GateState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var (
		startedAt  string
		state      string
		promptSent int
	)
	err := s.db.QueryRow(
		`SELECT started_at, state, prompt_sent FROM workflow_gate_state
		 WHERE instance_id = ? AND step_index = ?`,
		instanceID, stepIndex,
	).Scan(&startedAt, &state, &promptSent)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("workflow/state: get gate state: %w", err)
	}

	var t time.Time
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05Z"} {
		t, err = time.Parse(layout, startedAt)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("workflow/state: parse gate started_at: %w", err)
	}

	return &GateState{
		InstanceID: instanceID,
		StepIndex:  stepIndex,
		StartedAt:  t,
		State:      state,
		PromptSent: promptSent != 0,
	}, nil
}

// DeleteGateState removes gate state for a specific instance and step.
func (s *Store) DeleteGateState(instanceID string, stepIndex int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(
		`DELETE FROM workflow_gate_state WHERE instance_id = ? AND step_index = ?`,
		instanceID, stepIndex,
	)
	if err != nil {
		return fmt.Errorf("workflow/state: delete gate state: %w", err)
	}
	return nil
}
