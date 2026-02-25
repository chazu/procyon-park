package telemetry

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func mustNewMemoryStore(t *testing.T) *TelemetryStore {
	t.Helper()
	s, err := NewMemoryStore()
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func makeEvent(category EventCategory, eventType, source, nodeID string) *TelemetryEvent {
	return NewEvent(category, eventType, source, nodeID)
}

func TestSaveAndGetEvent(t *testing.T) {
	s := mustNewMemoryStore(t)

	event := makeEvent(CategoryAgent, "spawned", "king", "node-1").
		WithTarget("agent-Lark").
		WithData(map[string]string{"branch": "feature/test"})

	if err := s.SaveEvent(event); err != nil {
		t.Fatalf("SaveEvent: %v", err)
	}

	got, err := s.GetEvent(event.ID)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}

	if got.ID != event.ID {
		t.Errorf("ID = %q, want %q", got.ID, event.ID)
	}
	if got.EventType != "spawned" {
		t.Errorf("EventType = %q, want %q", got.EventType, "spawned")
	}
	if got.Category != CategoryAgent {
		t.Errorf("Category = %q, want %q", got.Category, CategoryAgent)
	}
	if got.Source != "king" {
		t.Errorf("Source = %q, want %q", got.Source, "king")
	}
	if got.Target != "agent-Lark" {
		t.Errorf("Target = %q, want %q", got.Target, "agent-Lark")
	}
	if got.NodeID != "node-1" {
		t.Errorf("NodeID = %q, want %q", got.NodeID, "node-1")
	}

	var data map[string]string
	if err := json.Unmarshal(got.Data, &data); err != nil {
		t.Fatalf("unmarshal Data: %v", err)
	}
	if data["branch"] != "feature/test" {
		t.Errorf("Data[branch] = %q, want %q", data["branch"], "feature/test")
	}
}

func TestGetEventNotFound(t *testing.T) {
	s := mustNewMemoryStore(t)

	_, err := s.GetEvent("nonexistent")
	if err != ErrEventNotFound {
		t.Errorf("GetEvent = %v, want ErrEventNotFound", err)
	}
}

func TestSaveEventValidation(t *testing.T) {
	s := mustNewMemoryStore(t)

	tests := []struct {
		name  string
		event *TelemetryEvent
	}{
		{"empty ID", &TelemetryEvent{Timestamp: time.Now(), EventType: "x", Category: "x", Source: "x", NodeID: "x"}},
		{"zero timestamp", &TelemetryEvent{ID: "x", EventType: "x", Category: "x", Source: "x", NodeID: "x"}},
		{"empty event type", &TelemetryEvent{ID: "x", Timestamp: time.Now(), Category: "x", Source: "x", NodeID: "x"}},
		{"empty category", &TelemetryEvent{ID: "x", Timestamp: time.Now(), EventType: "x", Source: "x", NodeID: "x"}},
		{"empty source", &TelemetryEvent{ID: "x", Timestamp: time.Now(), EventType: "x", Category: "x", NodeID: "x"}},
		{"empty node ID", &TelemetryEvent{ID: "x", Timestamp: time.Now(), EventType: "x", Category: "x", Source: "x"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.SaveEvent(tc.event); err == nil {
				t.Error("SaveEvent should have failed")
			}
		})
	}
}

func TestListEventsFilterByCategory(t *testing.T) {
	s := mustNewMemoryStore(t)

	s.SaveEvent(makeEvent(CategoryAgent, "spawned", "king", "n1"))
	s.SaveEvent(makeEvent(CategoryTask, "claimed", "lark", "n1"))
	s.SaveEvent(makeEvent(CategoryAgent, "dismissed", "king", "n1"))

	events, err := s.ListEvents(ListOptions{Category: CategoryAgent})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	for _, e := range events {
		if e.Category != CategoryAgent {
			t.Errorf("Category = %q, want %q", e.Category, CategoryAgent)
		}
	}
}

func TestListEventsFilterBySource(t *testing.T) {
	s := mustNewMemoryStore(t)

	s.SaveEvent(makeEvent(CategoryAgent, "spawned", "king", "n1"))
	s.SaveEvent(makeEvent(CategoryTask, "claimed", "lark", "n1"))

	events, err := s.ListEvents(ListOptions{Source: "lark"})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Source != "lark" {
		t.Errorf("Source = %q, want %q", events[0].Source, "lark")
	}
}

func TestListEventsTimeRange(t *testing.T) {
	s := mustNewMemoryStore(t)

	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		e := &TelemetryEvent{
			ID:        generateEventID(),
			Timestamp: base.Add(time.Duration(i) * 24 * time.Hour),
			EventType: "test",
			Category:  CategorySession,
			Source:    "test",
			NodeID:    "n1",
		}
		s.SaveEvent(e)
	}

	// Since = day 1 (exclusive), Until = day 3 (inclusive) => days 2, 3
	events, err := s.ListEvents(ListOptions{
		TimeFilter: TimeFilter{
			Since: base.Add(1 * 24 * time.Hour),
			Until: base.Add(3 * 24 * time.Hour),
		},
	})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
}

func TestListEventsLimit(t *testing.T) {
	s := mustNewMemoryStore(t)

	for i := 0; i < 10; i++ {
		s.SaveEvent(makeEvent(CategoryTask, "test", "src", "n1"))
	}

	events, err := s.ListEvents(ListOptions{Limit: 3})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}
}

func TestListEventsOrdering(t *testing.T) {
	s := mustNewMemoryStore(t)

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Insert in reverse order
	for i := 4; i >= 0; i-- {
		e := &TelemetryEvent{
			ID:        generateEventID(),
			Timestamp: base.Add(time.Duration(i) * time.Hour),
			EventType: "test",
			Category:  CategorySession,
			Source:    "test",
			NodeID:    "n1",
		}
		s.SaveEvent(e)
	}

	events, err := s.ListEvents(ListOptions{})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	for i := 1; i < len(events); i++ {
		if events[i].Timestamp.Before(events[i-1].Timestamp) {
			t.Errorf("events not in ascending order at index %d", i)
		}
	}
}

func TestPruneEvents(t *testing.T) {
	s := mustNewMemoryStore(t)

	now := time.Now().UTC()
	old := now.AddDate(0, 0, -60) // 60 days ago

	// Insert 3 old events and 2 recent events
	for i := 0; i < 3; i++ {
		e := &TelemetryEvent{
			ID:        generateEventID(),
			Timestamp: old.Add(time.Duration(i) * time.Second),
			EventType: "old",
			Category:  CategorySession,
			Source:    "test",
			NodeID:    "n1",
		}
		s.SaveEvent(e)
	}
	for i := 0; i < 2; i++ {
		e := &TelemetryEvent{
			ID:        generateEventID(),
			Timestamp: now.Add(time.Duration(i) * time.Second),
			EventType: "new",
			Category:  CategorySession,
			Source:    "test",
			NodeID:    "n1",
		}
		s.SaveEvent(e)
	}

	pruned, err := s.PruneEvents()
	if err != nil {
		t.Fatalf("PruneEvents: %v", err)
	}
	if pruned != 3 {
		t.Errorf("pruned = %d, want 3", pruned)
	}

	count, _ := s.Count()
	if count != 2 {
		t.Errorf("remaining count = %d, want 2", count)
	}
}

func TestPruneEventsBefore(t *testing.T) {
	s := mustNewMemoryStore(t)

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		e := &TelemetryEvent{
			ID:        generateEventID(),
			Timestamp: base.Add(time.Duration(i) * 24 * time.Hour),
			EventType: "test",
			Category:  CategorySession,
			Source:    "test",
			NodeID:    "n1",
		}
		s.SaveEvent(e)
	}

	cutoff := base.Add(2 * 24 * time.Hour) // prune day 0 and day 1
	pruned, err := s.PruneEventsBefore(cutoff)
	if err != nil {
		t.Fatalf("PruneEventsBefore: %v", err)
	}
	if pruned != 2 {
		t.Errorf("pruned = %d, want 2", pruned)
	}

	count, _ := s.Count()
	if count != 3 {
		t.Errorf("remaining count = %d, want 3", count)
	}
}

func TestCount(t *testing.T) {
	s := mustNewMemoryStore(t)

	count, err := s.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 0 {
		t.Errorf("empty store count = %d, want 0", count)
	}

	s.SaveEvent(makeEvent(CategoryTask, "a", "s", "n"))
	s.SaveEvent(makeEvent(CategoryTask, "b", "s", "n"))

	count, err = s.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestMigrationIdempotent(t *testing.T) {
	s := mustNewMemoryStore(t)

	// Running migrate again should not error
	if err := s.migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	// Store should still work
	s.SaveEvent(makeEvent(CategoryAgent, "test", "test", "n1"))
	count, _ := s.Count()
	if count != 1 {
		t.Errorf("count after re-migrate = %d, want 1", count)
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := mustNewMemoryStore(t)

	var wg sync.WaitGroup
	errs := make(chan error, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := makeEvent(CategoryTask, "concurrent", "goroutine", "n1")
			if err := s.SaveEvent(e); err != nil {
				errs <- err
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent SaveEvent: %v", err)
	}

	count, err := s.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 20 {
		t.Errorf("count = %d, want 20", count)
	}
}

func TestNewEventBuilder(t *testing.T) {
	e := NewEvent(CategoryGit, "commit", "agent-Lark", "node-42").
		WithTarget("main").
		WithData(map[string]string{"sha": "abc123"})

	if e.ID == "" {
		t.Error("ID should be generated")
	}
	if len(e.ID) != 16 {
		t.Errorf("ID length = %d, want 16", len(e.ID))
	}
	if e.Timestamp.IsZero() {
		t.Error("Timestamp should be set")
	}
	if e.Target != "main" {
		t.Errorf("Target = %q, want %q", e.Target, "main")
	}
	if e.Data == nil {
		t.Error("Data should be set")
	}
}

func TestSyncedAtRoundTrip(t *testing.T) {
	s := mustNewMemoryStore(t)

	syncTime := time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)
	e := makeEvent(CategoryAgent, "spawned", "king", "n1")
	e.SyncedAt = &syncTime

	s.SaveEvent(e)

	got, err := s.GetEvent(e.ID)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if got.SyncedAt == nil {
		t.Fatal("SyncedAt should not be nil")
	}
	if !got.SyncedAt.Equal(syncTime) {
		t.Errorf("SyncedAt = %v, want %v", got.SyncedAt, syncTime)
	}
}

func TestEventWithNilData(t *testing.T) {
	s := mustNewMemoryStore(t)

	e := makeEvent(CategorySession, "start", "cli", "n1")
	// No WithData call — Data is nil

	if err := s.SaveEvent(e); err != nil {
		t.Fatalf("SaveEvent: %v", err)
	}

	got, err := s.GetEvent(e.ID)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	// Data should be nil (empty "{}" not returned)
	if got.Data != nil {
		t.Errorf("Data = %s, want nil", got.Data)
	}
}
