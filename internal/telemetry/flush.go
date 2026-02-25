package telemetry

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DefaultFlushInterval is the default interval between Parquet flushes.
const DefaultFlushInterval = 5 * time.Minute

// FlushConfig configures the Parquet flush loop.
type FlushConfig struct {
	WarmDir       string        // Base directory for Parquet output.
	FlushInterval time.Duration // Interval between flushes; zero uses DefaultFlushInterval.
}

// Flusher periodically exports DuckDB in-memory tables to time-partitioned
// Parquet files using COPY TO with ZSTD compression. It uses an export-then-purge
// strategy: data is written to Parquet first, then deleted from DuckDB.
// This is crash-safe — a crash between export and purge produces duplicates
// rather than data loss.
type Flusher struct {
	pipeline *Pipeline
	cfg      FlushConfig

	mu      sync.Mutex
	once    sync.Once
	stopCh  chan struct{}
	doneCh  chan struct{}
}

// NewFlusher creates a Flusher that will export from pipeline to Parquet files.
func NewFlusher(pipeline *Pipeline, cfg FlushConfig) *Flusher {
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = DefaultFlushInterval
	}
	return &Flusher{
		pipeline: pipeline,
		cfg:      cfg,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// signal tables to flush: DuckDB table name → Parquet file stem.
var flushTables = []struct {
	table string
	stem  string
}{
	{"otel_spans", "spans"},
	{"otel_metrics", "metrics"},
	{"otel_logs", "logs"},
}

// Start begins the periodic flush loop. It blocks until Stop() is called.
// Run in a goroutine.
func (f *Flusher) Start(ctx context.Context) {
	defer close(f.doneCh)

	ticker := time.NewTicker(f.cfg.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			f.finalFlush()
			return
		case <-f.stopCh:
			f.finalFlush()
			return
		case <-ticker.C:
			f.Flush()
		}
	}
}

// Stop signals the flush loop to perform a final flush and exit.
// It blocks until the final flush completes.
func (f *Flusher) Stop() {
	f.once.Do(func() {
		close(f.stopCh)
		<-f.doneCh
	})
}

// finalFlush performs one last flush before shutdown.
func (f *Flusher) finalFlush() {
	f.Flush()
}

// Flush exports each signal table to Parquet and purges the exported rows.
// It is safe to call concurrently (serialised via mutex).
func (f *Flusher) Flush() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Also lock the pipeline mutex to prevent concurrent inserts during export.
	f.pipeline.mu.Lock()
	defer f.pipeline.mu.Unlock()

	now := time.Now().UTC()
	partition := now.Format("2006-01")

	var firstErr error
	for _, tbl := range flushTables {
		if err := f.flushTable(tbl.table, tbl.stem, partition); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// FlushAt exports tables using the given time for partitioning (for testing).
func (f *Flusher) FlushAt(t time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.pipeline.mu.Lock()
	defer f.pipeline.mu.Unlock()

	partition := t.UTC().Format("2006-01")

	var firstErr error
	for _, tbl := range flushTables {
		if err := f.flushTable(tbl.table, tbl.stem, partition); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// flushTable exports a single table to Parquet and purges it.
// Caller must hold both f.mu and f.pipeline.mu.
func (f *Flusher) flushTable(table, stem, partition string) error {
	// Check if the table has any rows to export.
	var count int
	if err := f.pipeline.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		return fmt.Errorf("flush: count %s: %w", table, err)
	}
	if count == 0 {
		return nil
	}

	// Ensure the partition directory exists.
	dir := filepath.Join(f.cfg.WarmDir, partition)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("flush: mkdir %s: %w", dir, err)
	}

	outPath := filepath.Join(dir, stem+".parquet")

	// Export-then-purge: write Parquet first, then delete.
	// If we crash between these two steps, we get duplicates on next flush — safe.
	copySQL := fmt.Sprintf(
		"COPY %s TO '%s' (FORMAT PARQUET, COMPRESSION ZSTD)",
		table, outPath,
	)
	if _, err := f.pipeline.db.Exec(copySQL); err != nil {
		return fmt.Errorf("flush: copy %s: %w", table, err)
	}

	// Purge the exported data.
	if _, err := f.pipeline.db.Exec("DELETE FROM " + table); err != nil {
		return fmt.Errorf("flush: purge %s: %w", table, err)
	}

	return nil
}
