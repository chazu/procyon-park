// Package telemetry provides a SQLite-backed event store for SDLC activity recording.
// Events capture agent, task, workflow, git, session, and error activity for local
// analytics and future hub sync.
package telemetry

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// DefaultRetentionDays is the default retention period for telemetry events.
const DefaultRetentionDays = 30

// ErrEventNotFound is returned when a telemetry event is not found.
var ErrEventNotFound = errors.New("telemetry event not found")

// EventCategory represents the category of a telemetry event.
type EventCategory string

const (
	CategoryAgent    EventCategory = "agent"
	CategoryTask     EventCategory = "task"
	CategoryWorkflow EventCategory = "workflow"
	CategoryMail     EventCategory = "mail"
	CategoryGit      EventCategory = "git"
	CategorySession  EventCategory = "session"
	CategoryError    EventCategory = "error"
)

// TelemetryEvent represents a single SDLC activity event.
type TelemetryEvent struct {
	ID        string          `json:"id"`
	Timestamp time.Time       `json:"timestamp"`
	EventType string          `json:"event_type"`
	Category  EventCategory   `json:"category"`
	Source    string          `json:"source"`
	Target    string          `json:"target,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	NodeID    string          `json:"node_id"`
	SyncedAt  *time.Time      `json:"synced_at,omitempty"`
}

// Validate checks that the event has all required fields.
func (e *TelemetryEvent) Validate() error {
	if e.ID == "" {
		return errors.New("event ID is required")
	}
	if e.Timestamp.IsZero() {
		return errors.New("event timestamp is required")
	}
	if e.EventType == "" {
		return errors.New("event type is required")
	}
	if e.Category == "" {
		return errors.New("event category is required")
	}
	if e.Source == "" {
		return errors.New("event source is required")
	}
	if e.NodeID == "" {
		return errors.New("event node ID is required")
	}
	return nil
}

// generateEventID creates a random 16-character hex event ID.
func generateEventID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// NewEvent creates a new TelemetryEvent with a generated ID and current timestamp.
func NewEvent(category EventCategory, eventType, source, nodeID string) *TelemetryEvent {
	return &TelemetryEvent{
		ID:        generateEventID(),
		Timestamp: time.Now(),
		Category:  category,
		EventType: eventType,
		Source:    source,
		NodeID:    nodeID,
	}
}

// WithTarget sets the target field and returns the event for chaining.
func (e *TelemetryEvent) WithTarget(target string) *TelemetryEvent {
	e.Target = target
	return e
}

// WithData sets the data field and returns the event for chaining.
func (e *TelemetryEvent) WithData(data interface{}) *TelemetryEvent {
	if data != nil {
		jsonData, err := json.Marshal(data)
		if err == nil {
			e.Data = jsonData
		}
	}
	return e
}

// Config holds telemetry configuration.
type Config struct {
	RetentionDays int
}

// DefaultConfig returns the default telemetry configuration.
func DefaultConfig() Config {
	return Config{RetentionDays: DefaultRetentionDays}
}

// TimeFilter specifies a time range for querying events.
type TimeFilter struct {
	Since time.Time // events after this time (exclusive); zero means no lower bound
	Until time.Time // events before this time (inclusive); zero means no upper bound
}

// ListOptions configures event listing queries.
type ListOptions struct {
	TimeFilter
	Category  EventCategory
	EventType string
	Source    string
	Limit     int
}

// ---------------------------------------------------------------------------
// Store
// ---------------------------------------------------------------------------

// TelemetryStore provides SQLite CRUD operations for telemetry events.
type TelemetryStore struct {
	db     *sql.DB
	mu     sync.Mutex
	stmts  map[string]*sql.Stmt
	config Config
}

// NewStore opens (or creates) a SQLite database at dbPath and returns a ready store.
func NewStore(dbPath string, cfg Config) (*TelemetryStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("telemetry: open %s: %w", dbPath, err)
	}
	return initStore(db, cfg)
}

// NewMemoryStore creates an in-memory TelemetryStore with default config.
func NewMemoryStore() (*TelemetryStore, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("telemetry: open memory: %w", err)
	}
	return initStore(db, DefaultConfig())
}

func initStore(db *sql.DB, cfg Config) (*TelemetryStore, error) {
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
			return nil, fmt.Errorf("telemetry: %s: %w", p, err)
		}
	}

	if cfg.RetentionDays <= 0 {
		cfg.RetentionDays = DefaultRetentionDays
	}

	s := &TelemetryStore{db: db, stmts: make(map[string]*sql.Stmt), config: cfg}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes all prepared statements and the database connection.
func (s *TelemetryStore) Close() error {
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

func (s *TelemetryStore) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("telemetry: create migrations table: %w", err)
	}

	migrations := []struct {
		version int
		sql     string
	}{
		{1, migrationV1},
	}

	for _, m := range migrations {
		var count int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = ?", m.version).Scan(&count); err != nil {
			return fmt.Errorf("telemetry: check migration %d: %w", m.version, err)
		}
		if count > 0 {
			continue
		}

		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("telemetry: begin migration %d: %w", m.version, err)
		}
		if _, err := tx.Exec(m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("telemetry: migration %d: %w", m.version, err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations (version) VALUES (?)", m.version); err != nil {
			tx.Rollback()
			return fmt.Errorf("telemetry: record migration %d: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("telemetry: commit migration %d: %w", m.version, err)
		}
	}
	return nil
}

const migrationV1 = `
CREATE TABLE IF NOT EXISTS telemetry_events (
    id         TEXT PRIMARY KEY,
    timestamp  DATETIME NOT NULL,
    event_type TEXT NOT NULL,
    category   TEXT NOT NULL,
    source     TEXT NOT NULL,
    target     TEXT NOT NULL DEFAULT '',
    data       TEXT NOT NULL DEFAULT '{}',
    node_id    TEXT NOT NULL,
    synced_at  DATETIME
);
CREATE INDEX IF NOT EXISTS idx_te_timestamp  ON telemetry_events(timestamp);
CREATE INDEX IF NOT EXISTS idx_te_category   ON telemetry_events(category);
CREATE INDEX IF NOT EXISTS idx_te_event_type ON telemetry_events(event_type);
CREATE INDEX IF NOT EXISTS idx_te_source     ON telemetry_events(source);
CREATE INDEX IF NOT EXISTS idx_te_node_id    ON telemetry_events(node_id);
`

// ---------------------------------------------------------------------------
// Prepared Statement Cache
// ---------------------------------------------------------------------------

func (s *TelemetryStore) prepare(name, query string) (*sql.Stmt, error) {
	if stmt, ok := s.stmts[name]; ok {
		return stmt, nil
	}
	stmt, err := s.db.Prepare(query)
	if err != nil {
		return nil, fmt.Errorf("telemetry: prepare %s: %w", name, err)
	}
	s.stmts[name] = stmt
	return stmt, nil
}

// ---------------------------------------------------------------------------
// CRUD Operations
// ---------------------------------------------------------------------------

// SaveEvent persists a telemetry event.
func (s *TelemetryStore) SaveEvent(event *TelemetryEvent) error {
	if err := event.Validate(); err != nil {
		return fmt.Errorf("telemetry: invalid event: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	stmt, err := s.prepare("save", `INSERT INTO telemetry_events
		(id, timestamp, event_type, category, source, target, data, node_id, synced_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}

	dataStr := "{}"
	if len(event.Data) > 0 {
		dataStr = string(event.Data)
	}

	var syncedAt *string
	if event.SyncedAt != nil {
		t := event.SyncedAt.UTC().Format(time.RFC3339Nano)
		syncedAt = &t
	}

	_, err = stmt.Exec(
		event.ID,
		event.Timestamp.UTC().Format(time.RFC3339Nano),
		event.EventType,
		string(event.Category),
		event.Source,
		event.Target,
		dataStr,
		event.NodeID,
		syncedAt,
	)
	if err != nil {
		return fmt.Errorf("telemetry: save event: %w", err)
	}
	return nil
}

// GetEvent retrieves a single event by ID.
func (s *TelemetryStore) GetEvent(id string) (*TelemetryEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stmt, err := s.prepare("get", `SELECT id, timestamp, event_type, category, source, target, data, node_id, synced_at
		FROM telemetry_events WHERE id = ?`)
	if err != nil {
		return nil, err
	}

	row := stmt.QueryRow(id)
	event, err := scanEventRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrEventNotFound
		}
		return nil, fmt.Errorf("telemetry: get event: %w", err)
	}
	return event, nil
}

// ListEvents queries events with optional filtering, ordered by timestamp ascending.
func (s *TelemetryStore) ListEvents(opts ListOptions) ([]*TelemetryEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var clauses []string
	var args []interface{}

	if !opts.Since.IsZero() {
		clauses = append(clauses, "timestamp > ?")
		args = append(args, opts.Since.UTC().Format(time.RFC3339Nano))
	}
	if !opts.Until.IsZero() {
		clauses = append(clauses, "timestamp <= ?")
		args = append(args, opts.Until.UTC().Format(time.RFC3339Nano))
	}
	if opts.Category != "" {
		clauses = append(clauses, "category = ?")
		args = append(args, string(opts.Category))
	}
	if opts.EventType != "" {
		clauses = append(clauses, "event_type = ?")
		args = append(args, opts.EventType)
	}
	if opts.Source != "" {
		clauses = append(clauses, "source = ?")
		args = append(args, opts.Source)
	}

	query := "SELECT id, timestamp, event_type, category, source, target, data, node_id, synced_at FROM telemetry_events"
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY timestamp ASC"
	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("telemetry: list events: %w", err)
	}
	defer rows.Close()

	var events []*TelemetryEvent
	for rows.Next() {
		event, err := scanEventRows(rows)
		if err != nil {
			return nil, fmt.Errorf("telemetry: scan event: %w", err)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// Count returns the total number of telemetry events.
func (s *TelemetryStore) Count() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stmt, err := s.prepare("count", "SELECT COUNT(*) FROM telemetry_events")
	if err != nil {
		return 0, err
	}

	var count int
	if err := stmt.QueryRow().Scan(&count); err != nil {
		return 0, fmt.Errorf("telemetry: count: %w", err)
	}
	return count, nil
}

// PruneEvents removes events older than the configured retention period.
func (s *TelemetryStore) PruneEvents() (int, error) {
	cutoff := time.Now().AddDate(0, 0, -s.config.RetentionDays)
	return s.PruneEventsBefore(cutoff)
}

// PruneEventsBefore removes events with timestamps before the given cutoff.
func (s *TelemetryStore) PruneEventsBefore(cutoff time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stmt, err := s.prepare("prune", "DELETE FROM telemetry_events WHERE timestamp < ?")
	if err != nil {
		return 0, err
	}

	result, err := stmt.Exec(cutoff.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("telemetry: prune events: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("telemetry: prune rows affected: %w", err)
	}
	return int(n), nil
}

// ---------------------------------------------------------------------------
// Row Scanning Helpers
// ---------------------------------------------------------------------------

// scanEventRow scans a single event from a *sql.Row.
func scanEventRow(row *sql.Row) (*TelemetryEvent, error) {
	var (
		e         TelemetryEvent
		ts        string
		cat       string
		data      string
		syncedAt  sql.NullString
	)
	if err := row.Scan(&e.ID, &ts, &e.EventType, &cat, &e.Source, &e.Target, &data, &e.NodeID, &syncedAt); err != nil {
		return nil, err
	}
	return finishScan(&e, ts, cat, data, syncedAt)
}

// scanEventRows scans a single event from *sql.Rows.
func scanEventRows(rows *sql.Rows) (*TelemetryEvent, error) {
	var (
		e        TelemetryEvent
		ts       string
		cat      string
		data     string
		syncedAt sql.NullString
	)
	if err := rows.Scan(&e.ID, &ts, &e.EventType, &cat, &e.Source, &e.Target, &data, &e.NodeID, &syncedAt); err != nil {
		return nil, err
	}
	return finishScan(&e, ts, cat, data, syncedAt)
}

func finishScan(e *TelemetryEvent, ts, cat, data string, syncedAt sql.NullString) (*TelemetryEvent, error) {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return nil, fmt.Errorf("telemetry: parse timestamp %q: %w", ts, err)
	}
	e.Timestamp = t
	e.Category = EventCategory(cat)
	if data != "" && data != "{}" {
		e.Data = json.RawMessage(data)
	}
	if syncedAt.Valid {
		st, err := time.Parse(time.RFC3339Nano, syncedAt.String)
		if err != nil {
			return nil, fmt.Errorf("telemetry: parse synced_at %q: %w", syncedAt.String, err)
		}
		e.SyncedAt = &st
	}
	return e, nil
}
