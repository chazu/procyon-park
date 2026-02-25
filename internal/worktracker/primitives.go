// primitives.go registers WorkTracker primitives on the Maggie VM.
//
// Maggie API:
//
//	WorkTracker beads: '/path/to/repo'         "class method — creates beads-backed tracker"
//	WorkTracker noop                           "class method — creates noop tracker"
//
//	tracker getTask: 'id'                      "returns Dictionary or nil"
//	tracker closeTask: 'id'                    "closes task, returns true or Failure"
//	tracker updateTask:opts: 'id' aDictionary  "update fields, returns true or Failure"
//	tracker listReady                          "returns Array of Dictionaries"
//	tracker listByStatus: 'status'             "returns Array of Dictionaries"
//	tracker listByParent: 'epicId'             "returns Array of Dictionaries"
//	tracker addDependency:on: 'id' 'depId'     "add dependency, returns true or Failure"
//	tracker createTask: aDictionary            "create task from Dictionary, returns Dictionary or Failure"
package worktracker

import (
	"reflect"

	"github.com/chazu/maggie/vm"
)

// Register registers the WorkTracker class and its primitives on the given VM.
func Register(vmInst *vm.VM) {
	// We register a wrapper that holds the interface so the VM can
	// store a reference to any implementation.
	wrapperType := reflect.TypeOf((*trackerWrapper)(nil))
	trackerClass := vmInst.RegisterGoType("WorkTracker", wrapperType)

	registerTrackerClassMethods(vmInst, trackerClass)
	registerTrackerInstanceMethods(vmInst, trackerClass)
}

// trackerWrapper wraps a WorkTracker so the VM can hold it as a Go object.
type trackerWrapper struct {
	tracker WorkTracker
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func getTracker(vmInst *vm.VM, v vm.Value) WorkTracker {
	goVal, ok := vmInst.GetGoObject(v)
	if !ok {
		return nil
	}
	w, ok := goVal.(*trackerWrapper)
	if !ok {
		return nil
	}
	return w.tracker
}

func wtToString(vmInst *vm.VM, v vm.Value) string {
	if vm.IsStringValue(v) {
		return vmInst.Registry().GetStringContent(v)
	}
	if v.IsSymbol() {
		return vmInst.SymbolName(v.SymbolID())
	}
	return ""
}

func wtFailure(vmInst *vm.VM, reason string) vm.Value {
	reasonVal := vmInst.Registry().NewStringValue(reason)
	failureClassVal := vmInst.ClassValue(vmInst.FailureClass)
	return vmInst.Send(failureClassVal, "with:", []vm.Value{reasonVal})
}

func wtDictGetString(vmInst *vm.VM, dict vm.Value, key string) string {
	k := vmInst.Registry().NewStringValue(key)
	val := vmInst.DictionaryAt(dict, k)
	if val == vm.Nil {
		return ""
	}
	return wtToString(vmInst, val)
}

func wtDictGetOptString(vmInst *vm.VM, dict vm.Value, key string) *string {
	k := vmInst.Registry().NewStringValue(key)
	val := vmInst.DictionaryAt(dict, k)
	if val == vm.Nil {
		return nil
	}
	s := wtToString(vmInst, val)
	return &s
}

// taskToDict converts a Task to a Maggie Dictionary value.
func taskToDict(vmInst *vm.VM, t *Task) vm.Value {
	if t == nil {
		return vm.Nil
	}
	dict := vmInst.NewDictionary()

	set := func(key, val string) {
		if val == "" {
			return
		}
		k := vmInst.Registry().NewStringValue(key)
		v := vmInst.Registry().NewStringValue(val)
		vmInst.DictionaryAtPut(dict, k, v)
	}

	set("id", t.ID)
	set("title", t.Title)
	set("description", t.Description)
	set("status", t.Status)
	set("type", t.Type)
	set("parent", t.Parent)
	set("assignee", t.Assignee)
	set("notes", t.Notes)

	if t.Priority != 0 {
		k := vmInst.Registry().NewStringValue("priority")
		vmInst.DictionaryAtPut(dict, k, vm.FromSmallInt(int64(t.Priority)))
	}

	return dict
}

// tasksToArray converts a slice of Tasks to a Maggie Array value.
func tasksToArray(vmInst *vm.VM, tasks []Task) vm.Value {
	if len(tasks) == 0 {
		return vmInst.NewArrayWithElements(nil)
	}
	vals := make([]vm.Value, len(tasks))
	for i := range tasks {
		vals[i] = taskToDict(vmInst, &tasks[i])
	}
	return vmInst.NewArrayWithElements(vals)
}

// ---------------------------------------------------------------------------
// Class Methods
// ---------------------------------------------------------------------------

func registerTrackerClassMethods(vmInst *vm.VM, trackerClass *vm.Class) {
	// beads: aPath — Create a beads-backed WorkTracker.
	trackerClass.AddClassMethod1(vmInst.Selectors, "beads:", func(vmPtr interface{}, recv vm.Value, pathVal vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		dir := wtToString(v, pathVal)
		if dir == "" {
			return wtFailure(v, "WorkTracker beads: requires a directory path")
		}
		bt := NewBeadsTracker(dir)
		w := &trackerWrapper{tracker: bt}
		val, err := v.RegisterGoObject(w)
		if err != nil {
			return wtFailure(v, "WorkTracker beads: cannot register: "+err.Error())
		}
		return val
	})

	// noop — Create a noop WorkTracker.
	trackerClass.AddClassMethod0(vmInst.Selectors, "noop", func(vmPtr interface{}, recv vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		w := &trackerWrapper{tracker: &NoopTracker{}}
		val, err := v.RegisterGoObject(w)
		if err != nil {
			return wtFailure(v, "WorkTracker noop: cannot register: "+err.Error())
		}
		return val
	})
}

// ---------------------------------------------------------------------------
// Instance Methods
// ---------------------------------------------------------------------------

func registerTrackerInstanceMethods(vmInst *vm.VM, trackerClass *vm.Class) {
	// getTask: id — Returns a Dictionary or nil.
	trackerClass.AddMethod1(vmInst.Selectors, "getTask:", func(vmPtr interface{}, recv vm.Value, idVal vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		tracker := getTracker(v, recv)
		if tracker == nil {
			return wtFailure(v, "Not a WorkTracker")
		}
		id := wtToString(v, idVal)
		task, err := tracker.GetTask(id)
		if err != nil {
			return wtFailure(v, "getTask: "+err.Error())
		}
		return taskToDict(v, task)
	})

	// closeTask: id — Closes a task.
	trackerClass.AddMethod1(vmInst.Selectors, "closeTask:", func(vmPtr interface{}, recv vm.Value, idVal vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		tracker := getTracker(v, recv)
		if tracker == nil {
			return wtFailure(v, "Not a WorkTracker")
		}
		id := wtToString(v, idVal)
		if err := tracker.CloseTask(id); err != nil {
			return wtFailure(v, "closeTask: "+err.Error())
		}
		return vm.True
	})

	// updateTask:opts: id aDictionary — Update task fields.
	trackerClass.AddMethod2(vmInst.Selectors, "updateTask:opts:", func(vmPtr interface{}, recv vm.Value, idVal vm.Value, optsVal vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		tracker := getTracker(v, recv)
		if tracker == nil {
			return wtFailure(v, "Not a WorkTracker")
		}
		id := wtToString(v, idVal)
		opts := UpdateTaskOpts{
			Status:      wtDictGetOptString(v, optsVal, "status"),
			Assignee:    wtDictGetOptString(v, optsVal, "assignee"),
			Notes:       wtDictGetOptString(v, optsVal, "notes"),
			Title:       wtDictGetOptString(v, optsVal, "title"),
			Description: wtDictGetOptString(v, optsVal, "description"),
		}
		if err := tracker.UpdateTask(id, opts); err != nil {
			return wtFailure(v, "updateTask:opts: "+err.Error())
		}
		return vm.True
	})

	// listReady — Returns Array of task Dictionaries.
	trackerClass.AddMethod0(vmInst.Selectors, "listReady", func(vmPtr interface{}, recv vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		tracker := getTracker(v, recv)
		if tracker == nil {
			return wtFailure(v, "Not a WorkTracker")
		}
		tasks, err := tracker.ListReady()
		if err != nil {
			return wtFailure(v, "listReady: "+err.Error())
		}
		return tasksToArray(v, tasks)
	})

	// listByStatus: status — Returns Array of task Dictionaries.
	trackerClass.AddMethod1(vmInst.Selectors, "listByStatus:", func(vmPtr interface{}, recv vm.Value, statusVal vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		tracker := getTracker(v, recv)
		if tracker == nil {
			return wtFailure(v, "Not a WorkTracker")
		}
		status := wtToString(v, statusVal)
		tasks, err := tracker.ListByStatus(status)
		if err != nil {
			return wtFailure(v, "listByStatus: "+err.Error())
		}
		return tasksToArray(v, tasks)
	})

	// listByParent: epicId — Returns Array of task Dictionaries.
	trackerClass.AddMethod1(vmInst.Selectors, "listByParent:", func(vmPtr interface{}, recv vm.Value, epicVal vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		tracker := getTracker(v, recv)
		if tracker == nil {
			return wtFailure(v, "Not a WorkTracker")
		}
		epicID := wtToString(v, epicVal)
		tasks, err := tracker.ListByParent(epicID)
		if err != nil {
			return wtFailure(v, "listByParent: "+err.Error())
		}
		return tasksToArray(v, tasks)
	})

	// addDependency:on: taskId dependsOnId — Add dependency.
	trackerClass.AddMethod2(vmInst.Selectors, "addDependency:on:", func(vmPtr interface{}, recv vm.Value, taskVal vm.Value, depVal vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		tracker := getTracker(v, recv)
		if tracker == nil {
			return wtFailure(v, "Not a WorkTracker")
		}
		taskID := wtToString(v, taskVal)
		depID := wtToString(v, depVal)
		if err := tracker.AddDependency(taskID, depID); err != nil {
			return wtFailure(v, "addDependency:on: "+err.Error())
		}
		return vm.True
	})

	// createTask: aDictionary — Create a new task.
	// Dictionary keys: title, description, type, priority, parent.
	trackerClass.AddMethod1(vmInst.Selectors, "createTask:", func(vmPtr interface{}, recv vm.Value, dictVal vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		tracker := getTracker(v, recv)
		if tracker == nil {
			return wtFailure(v, "Not a WorkTracker")
		}

		title := wtDictGetString(v, dictVal, "title")
		if title == "" {
			return wtFailure(v, "createTask: title is required")
		}

		opts := CreateTaskOpts{
			Title:       title,
			Description: wtDictGetString(v, dictVal, "description"),
			TaskType:    wtDictGetString(v, dictVal, "type"),
			Parent:      wtDictGetString(v, dictVal, "parent"),
		}

		// Parse priority from dictionary.
		priKey := v.Registry().NewStringValue("priority")
		priVal := v.DictionaryAt(dictVal, priKey)
		if priVal != vm.Nil && priVal.IsSmallInt() {
			opts.Priority = int(priVal.SmallInt())
		}

		task, err := tracker.CreateTask(opts)
		if err != nil {
			return wtFailure(v, "createTask: "+err.Error())
		}
		return taskToDict(v, task)
	})
}
