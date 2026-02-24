package checkpoint

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chazu/maggie/compiler"
	"github.com/chazu/maggie/vm"
)

// --- helpers ---

func newTestVM(t *testing.T) *vm.VM {
	t.Helper()
	v := vm.NewVM()
	v.UseGoCompiler(compiler.Compile)
	return v
}

func writeValidImage(t *testing.T, path string) {
	t.Helper()
	v := newTestVM(t)
	if err := v.SaveImageAtomic(path); err != nil {
		t.Fatalf("writeValidImage: %v", err)
	}
}

func writeCorruptFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("CORRUPT_DATA"), 0o644); err != nil {
		t.Fatalf("writeCorruptFile: %v", err)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// --- Atomic write correctness ---

func TestAtomicWriteCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.image")
	v := newTestVM(t)

	if err := v.SaveImageAtomic(path); err != nil {
		t.Fatalf("SaveImageAtomic: %v", err)
	}
	if !fileExists(path) {
		t.Fatal("expected image file to exist")
	}
	// No .tmp should remain.
	if fileExists(path + ".tmp") {
		t.Fatal("expected .tmp to be cleaned up")
	}
	// No .prev on first write (no prior image to rotate).
	if fileExists(path + ".prev") {
		t.Fatal("expected no .prev on first write")
	}
}

func TestAtomicWriteRetainsPrev(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.image")
	v := newTestVM(t)

	// First write.
	if err := v.SaveImageAtomic(path); err != nil {
		t.Fatalf("first save: %v", err)
	}
	firstInfo, _ := os.Stat(path)
	firstSize := firstInfo.Size()

	// Second write — .prev should now exist.
	if err := v.SaveImageAtomic(path); err != nil {
		t.Fatalf("second save: %v", err)
	}
	if !fileExists(path + ".prev") {
		t.Fatal("expected .prev after second save")
	}
	prevInfo, _ := os.Stat(path + ".prev")
	if prevInfo.Size() != firstSize {
		t.Fatalf("expected .prev size %d, got %d", firstSize, prevInfo.Size())
	}
}

func TestAtomicWriteImageIsLoadable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.image")
	v := newTestVM(t)

	if err := v.SaveImageAtomic(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	v2 := vm.NewVM()
	if err := v2.LoadImage(path); err != nil {
		t.Fatalf("load: %v", err)
	}
}

// --- Crash recovery ---

func TestRecoverCleanState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.image")
	writeValidImage(t, path)

	recovered, err := RecoverImage(path)
	if err != nil {
		t.Fatalf("RecoverImage: %v", err)
	}
	if recovered != path {
		t.Fatalf("expected %s, got %s", path, recovered)
	}
}

func TestRecoverRemovesTmp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.image")
	writeValidImage(t, path)

	// Simulate a .tmp left from a crashed write.
	writeCorruptFile(t, path+".tmp")

	recovered, err := RecoverImage(path)
	if err != nil {
		t.Fatalf("RecoverImage: %v", err)
	}
	if recovered != path {
		t.Fatalf("expected %s, got %s", path, recovered)
	}
	if fileExists(path + ".tmp") {
		t.Fatal("expected .tmp to be removed")
	}
}

func TestRecoverFromPrevWhenMainMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.image")

	// Only .prev exists (simulates crash during step 4: rename .tmp → main).
	writeValidImage(t, path+".prev")

	recovered, err := RecoverImage(path)
	if err != nil {
		t.Fatalf("RecoverImage: %v", err)
	}
	if recovered != path {
		t.Fatalf("expected %s, got %s", path, recovered)
	}
	if !fileExists(path) {
		t.Fatal("expected .prev to be promoted to main")
	}
	if fileExists(path + ".prev") {
		t.Fatal("expected .prev to be gone after promotion")
	}

	// Verify the promoted image is loadable.
	v := vm.NewVM()
	if err := v.LoadImage(path); err != nil {
		t.Fatalf("load promoted image: %v", err)
	}
}

func TestRecoverFromPrevWhenMainCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.image")

	// Main is corrupt, .prev is valid.
	writeCorruptFile(t, path)
	writeValidImage(t, path+".prev")

	recovered, err := RecoverImage(path)
	if err != nil {
		t.Fatalf("RecoverImage: %v", err)
	}
	if recovered != path {
		t.Fatalf("expected %s, got %s", path, recovered)
	}
}

func TestRecoverNoImageAvailable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.image")

	_, err := RecoverImage(path)
	if err != ErrNoImage {
		t.Fatalf("expected ErrNoImage, got %v", err)
	}
}

func TestRecoverTmpAndPrevNoMain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.image")

	// Both .tmp (corrupt partial) and .prev (valid) exist, no main.
	writeCorruptFile(t, path+".tmp")
	writeValidImage(t, path+".prev")

	recovered, err := RecoverImage(path)
	if err != nil {
		t.Fatalf("RecoverImage: %v", err)
	}
	if recovered != path {
		t.Fatalf("expected %s, got %s", path, recovered)
	}
	if fileExists(path + ".tmp") {
		t.Fatal("expected .tmp to be removed")
	}
}

// --- Manager ---

func TestManagerCheckpointNow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.image")
	v := newTestVM(t)

	mgr := NewManagerForVM(v, path, DefaultInterval)

	if err := mgr.CheckpointNow(); err != nil {
		t.Fatalf("CheckpointNow: %v", err)
	}
	if !fileExists(path) {
		t.Fatal("expected image file after CheckpointNow")
	}
	if mgr.Count() != 1 {
		t.Fatalf("expected count 1, got %d", mgr.Count())
	}
	if mgr.LastSave().IsZero() {
		t.Fatal("expected LastSave to be set")
	}
	if mgr.LastError() != nil {
		t.Fatalf("expected no error, got %v", mgr.LastError())
	}
}

func TestManagerPeriodicCheckpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.image")

	var saveCount atomic.Int32
	fakeSave := func(p string) error {
		saveCount.Add(1)
		// Write a minimal valid image marker so isValidImage works.
		return os.WriteFile(p, vm.ImageMagic[:], 0o644)
	}

	mgr := NewManager(path, 20*time.Millisecond, fakeSave)
	if err := mgr.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for at least 2 ticks.
	time.Sleep(70 * time.Millisecond)
	mgr.Stop()

	count := int(saveCount.Load())
	if count < 2 {
		t.Fatalf("expected at least 2 periodic saves, got %d", count)
	}
}

func TestManagerStartStopIdempotent(t *testing.T) {
	mgr := NewManager("/tmp/unused.image", time.Hour, func(string) error { return nil })

	if err := mgr.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := mgr.Start(); err == nil {
		t.Fatal("expected error on double Start")
	}
	mgr.Stop()
	// Stop again should be safe.
	mgr.Stop()
}

func TestManagerRecordsError(t *testing.T) {
	errExpected := os.ErrPermission
	mgr := NewManager("/nonexistent/path/image", time.Hour, func(string) error {
		return errExpected
	})

	err := mgr.CheckpointNow()
	if err != errExpected {
		t.Fatalf("expected %v, got %v", errExpected, err)
	}
	if mgr.LastError() != errExpected {
		t.Fatalf("LastError: expected %v, got %v", errExpected, mgr.LastError())
	}
	if mgr.Count() != 0 {
		t.Fatalf("expected count 0 after error, got %d", mgr.Count())
	}
}

func TestManagerPath(t *testing.T) {
	mgr := NewManager("/some/path/image", time.Hour, func(string) error { return nil })
	if mgr.Path() != "/some/path/image" {
		t.Fatalf("expected /some/path/image, got %s", mgr.Path())
	}
}
