package daemon

import (
	"testing"

	"github.com/chazu/procyon-park/internal/tuplestore"
)

func newTestStore(t *testing.T) *tuplestore.TupleStore {
	t.Helper()
	store, err := tuplestore.NewMemoryStore()
	if err != nil {
		t.Fatalf("create test store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}
