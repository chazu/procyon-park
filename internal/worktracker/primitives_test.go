package worktracker

import (
	"testing"

	"github.com/chazu/maggie/compiler"
	"github.com/chazu/maggie/vm"
)

func testVM(t *testing.T) *vm.VM {
	t.Helper()
	v := vm.NewVM()
	v.UseGoCompiler(compiler.Compile)
	Register(v)
	return v
}

func TestRegister(t *testing.T) {
	// Registration should not panic.
	v := testVM(t)
	_ = v
}

func TestGetTrackerHelper(t *testing.T) {
	v := testVM(t)

	// Register a mock tracker as a Go object.
	mock := NewMockTracker()
	mock.AddTask(Task{ID: "test-1", Title: "Hello", Status: "open"})

	w := &trackerWrapper{tracker: mock}
	val, err := v.RegisterGoObject(w)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// getTracker should extract the mock.
	tracker := getTracker(v, val)
	if tracker == nil {
		t.Fatal("getTracker returned nil")
	}

	task, err := tracker.GetTask("test-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task == nil || task.Title != "Hello" {
		t.Fatalf("unexpected task: %v", task)
	}
}

func TestTaskToDict(t *testing.T) {
	v := testVM(t)

	task := &Task{
		ID:       "t-1",
		Title:    "Test Task",
		Status:   "open",
		Priority: 2,
	}

	dict := taskToDict(v, task)
	if dict == vm.Nil {
		t.Fatal("taskToDict returned nil")
	}

	// Verify we can read back the id field.
	idKey := v.Registry().NewStringValue("id")
	idVal := v.DictionaryAt(dict, idKey)
	if idVal == vm.Nil {
		t.Fatal("id field is nil in dict")
	}
	if !vm.IsStringValue(idVal) {
		t.Fatal("id field is not a string")
	}
	if v.Registry().GetStringContent(idVal) != "t-1" {
		t.Fatalf("id field: got %q, want %q", v.Registry().GetStringContent(idVal), "t-1")
	}
}

func TestTaskToDictNil(t *testing.T) {
	v := testVM(t)
	result := taskToDict(v, nil)
	if result != vm.Nil {
		t.Fatal("expected nil for nil task")
	}
}

func TestTasksToArray(t *testing.T) {
	v := testVM(t)

	tasks := []Task{
		{ID: "a", Title: "First", Status: "open"},
		{ID: "b", Title: "Second", Status: "closed"},
	}

	arr := tasksToArray(v, tasks)
	if arr == vm.Nil {
		t.Fatal("tasksToArray returned nil")
	}
}

func TestTasksToArrayEmpty(t *testing.T) {
	v := testVM(t)
	arr := tasksToArray(v, nil)
	if arr == vm.Nil {
		t.Fatal("tasksToArray(nil) returned nil, expected empty array")
	}
}
