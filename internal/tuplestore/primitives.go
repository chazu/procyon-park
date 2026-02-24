package tuplestore

import (
	"reflect"

	"github.com/chazu/maggie/vm"
)

// Register registers the TupleStore class and its primitives on the given VM.
// Call this after the VM is created and the image is loaded.
//
// Maggie API:
//
//	TupleStore open: '/path/to/db.sqlite'     "class method — opens or creates a DB"
//	TupleStore openMemory                     "class method — in-memory DB"
//	store close                               "close the store"
//
//	store insert: aDictionary                 "insert tuple from Dictionary, returns row ID"
//	store findOne: aDictionary                "find oldest match, returns Dictionary or nil"
//	store findAndDelete: aDictionary          "atomic find+delete, returns Dictionary or nil"
//	store findAll: aDictionary                "all matches, returns Array of Dictionaries"
//	store delete: anInteger                   "delete by ID, returns true/false"
//	store deleteByPattern: aDictionary        "delete matching tuples, returns count"
//	store count: aDictionary                  "count matching tuples, returns integer"
func Register(vmInst *vm.VM) {
	storeType := reflect.TypeOf((*TupleStore)(nil))
	storeClass := vmInst.RegisterGoType("TupleStore", storeType)

	registerClassMethods(vmInst, storeClass)
	registerInstanceMethods(vmInst, storeClass)
}

// ---------------------------------------------------------------------------
// Helpers: extract TupleStore from GoObject, string from Value, failure result
// ---------------------------------------------------------------------------

func getStore(vmInst *vm.VM, v vm.Value) *TupleStore {
	goVal, ok := vmInst.GetGoObject(v)
	if !ok {
		return nil
	}
	s, ok := goVal.(*TupleStore)
	if !ok {
		return nil
	}
	return s
}

func toString(vmInst *vm.VM, v vm.Value) string {
	if vm.IsStringValue(v) {
		return vmInst.Registry().GetStringContent(v)
	}
	if v.IsSymbol() {
		return vmInst.SymbolName(v.SymbolID())
	}
	return ""
}

func toOptionalString(vmInst *vm.VM, v vm.Value) *string {
	if v == vm.Nil {
		return nil
	}
	s := toString(vmInst, v)
	return &s
}

func toOptionalInt(v vm.Value) *int {
	if v == vm.Nil {
		return nil
	}
	if v.IsSmallInt() {
		n := int(v.SmallInt())
		return &n
	}
	return nil
}

// failureResult creates a Failure result via the VM's Failure class.
func failureResult(vmInst *vm.VM, reason string) vm.Value {
	reasonVal := vmInst.Registry().NewStringValue(reason)
	failureClassVal := vmInst.ClassValue(vmInst.FailureClass)
	return vmInst.Send(failureClassVal, "with:", []vm.Value{reasonVal})
}

// tupleRowToDict converts a tuple row map to a Maggie Dictionary value.
func tupleRowToDict(vmInst *vm.VM, row map[string]interface{}) vm.Value {
	dict := vmInst.NewDictionary()
	for k, v := range row {
		key := vmInst.Registry().NewStringValue(k)
		val := vmInst.GoToValue(v)
		vmInst.DictionaryAtPut(dict, key, val)
	}
	return dict
}

// dictToPattern extracts pattern fields from a Maggie Dictionary.
// Expected keys: "category", "scope", "identity", "instance", "payloadSearch".
// Missing or nil keys are treated as wildcards.
func dictToPattern(vmInst *vm.VM, dict vm.Value) (category, scope, identity, instance, payloadSearch *string) {
	fields := []string{"category", "scope", "identity", "instance", "payloadSearch"}
	ptrs := []*(*string){&category, &scope, &identity, &instance, &payloadSearch}

	for i, field := range fields {
		key := vmInst.Registry().NewStringValue(field)
		v := vmInst.DictionaryAt(dict, key)
		if v != vm.Nil {
			s := toString(vmInst, v)
			*ptrs[i] = &s
		}
	}
	return
}

// dictToInsertParams extracts insert parameters from a Maggie Dictionary.
// Required: "category", "scope", "identity", "instance", "payload", "lifecycle".
// Optional: "taskId", "agentId", "ttl".
func dictToInsertParams(vmInst *vm.VM, dict vm.Value) (
	category, scope, identity, instance, payload, lifecycle string,
	taskID, agentID *string, ttl *int, err string,
) {
	get := func(field string) string {
		key := vmInst.Registry().NewStringValue(field)
		v := vmInst.DictionaryAt(dict, key)
		if v == vm.Nil {
			return ""
		}
		return toString(vmInst, v)
	}

	category = get("category")
	scope = get("scope")
	identity = get("identity")
	instance = get("instance")
	payload = get("payload")
	lifecycle = get("lifecycle")

	if category == "" {
		err = "insert: category is required"
		return
	}
	if payload == "" {
		payload = "{}"
	}
	if lifecycle == "" {
		lifecycle = "session"
	}
	if instance == "" {
		instance = "local"
	}

	// Optional fields
	taskKey := vmInst.Registry().NewStringValue("taskId")
	taskVal := vmInst.DictionaryAt(dict, taskKey)
	taskID = toOptionalString(vmInst, taskVal)

	agentKey := vmInst.Registry().NewStringValue("agentId")
	agentVal := vmInst.DictionaryAt(dict, agentKey)
	agentID = toOptionalString(vmInst, agentVal)

	ttlKey := vmInst.Registry().NewStringValue("ttl")
	ttlVal := vmInst.DictionaryAt(dict, ttlKey)
	ttl = toOptionalInt(ttlVal)

	return
}

// ---------------------------------------------------------------------------
// Class Methods
// ---------------------------------------------------------------------------

func registerClassMethods(vmInst *vm.VM, storeClass *vm.Class) {
	// open: path — Open or create a TupleStore at the given file path
	storeClass.AddClassMethod1(vmInst.Selectors, "open:", func(vmPtr interface{}, recv vm.Value, pathVal vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		path := toString(v, pathVal)
		if path == "" {
			return failureResult(v, "TupleStore open: requires a path string")
		}

		store, err := NewStore(path)
		if err != nil {
			return failureResult(v, "TupleStore open: "+err.Error())
		}

		val, regErr := v.RegisterGoObject(store)
		if regErr != nil {
			store.Close()
			return failureResult(v, "TupleStore open: cannot register: "+regErr.Error())
		}
		return val
	})

	// openMemory — Create an in-memory TupleStore
	storeClass.AddClassMethod0(vmInst.Selectors, "openMemory", func(vmPtr interface{}, recv vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		store, err := NewMemoryStore()
		if err != nil {
			return failureResult(v, "TupleStore openMemory: "+err.Error())
		}

		val, regErr := v.RegisterGoObject(store)
		if regErr != nil {
			store.Close()
			return failureResult(v, "TupleStore openMemory: cannot register: "+regErr.Error())
		}
		return val
	})
}

// ---------------------------------------------------------------------------
// Instance Methods
// ---------------------------------------------------------------------------

func registerInstanceMethods(vmInst *vm.VM, storeClass *vm.Class) {
	// close — Close the store and release resources
	storeClass.AddMethod0(vmInst.Selectors, "close", func(vmPtr interface{}, recv vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		s := getStore(v, recv)
		if s == nil {
			return failureResult(v, "Not a TupleStore")
		}
		if err := s.Close(); err != nil {
			return failureResult(v, "TupleStore close: "+err.Error())
		}
		return vm.True
	})

	// insert: aDictionary — Insert a tuple. Dictionary keys:
	//   category (required), scope, identity, instance, payload, lifecycle,
	//   taskId, agentId, ttl.
	// Returns the row ID (integer).
	storeClass.AddMethod1(vmInst.Selectors, "insert:", func(vmPtr interface{}, recv vm.Value, dictVal vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		s := getStore(v, recv)
		if s == nil {
			return failureResult(v, "Not a TupleStore")
		}

		category, scope, identity, instance, payload, lifecycle,
			taskID, agentID, ttl, errMsg := dictToInsertParams(v, dictVal)
		if errMsg != "" {
			return failureResult(v, "TupleStore "+errMsg)
		}

		id, err := s.Insert(category, scope, identity, instance, payload, lifecycle,
			taskID, agentID, ttl)
		if err != nil {
			return failureResult(v, "TupleStore insert: "+err.Error())
		}
		return vm.FromSmallInt(id)
	})

	// findOne: aDictionary — Find oldest matching tuple. Dictionary keys are pattern
	// fields (category, scope, identity, instance, payloadSearch). Nil keys = wildcard.
	// Returns a Dictionary or nil.
	storeClass.AddMethod1(vmInst.Selectors, "findOne:", func(vmPtr interface{}, recv vm.Value, dictVal vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		s := getStore(v, recv)
		if s == nil {
			return failureResult(v, "Not a TupleStore")
		}

		cat, scope, ident, inst, ps := dictToPattern(v, dictVal)
		row, err := s.FindOne(cat, scope, ident, inst, ps)
		if err != nil {
			return failureResult(v, "TupleStore findOne: "+err.Error())
		}
		if row == nil {
			return vm.Nil
		}
		return tupleRowToDict(v, row)
	})

	// findAndDelete: aDictionary — Atomic find+delete of the oldest matching tuple.
	// Returns a Dictionary or nil.
	storeClass.AddMethod1(vmInst.Selectors, "findAndDelete:", func(vmPtr interface{}, recv vm.Value, dictVal vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		s := getStore(v, recv)
		if s == nil {
			return failureResult(v, "Not a TupleStore")
		}

		cat, scope, ident, inst, ps := dictToPattern(v, dictVal)
		row, err := s.FindAndDelete(cat, scope, ident, inst, ps)
		if err != nil {
			return failureResult(v, "TupleStore findAndDelete: "+err.Error())
		}
		if row == nil {
			return vm.Nil
		}
		return tupleRowToDict(v, row)
	})

	// findAll: aDictionary — Find all matching tuples. Returns an Array of Dictionaries.
	storeClass.AddMethod1(vmInst.Selectors, "findAll:", func(vmPtr interface{}, recv vm.Value, dictVal vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		s := getStore(v, recv)
		if s == nil {
			return failureResult(v, "Not a TupleStore")
		}

		cat, scope, ident, inst, ps := dictToPattern(v, dictVal)
		rows, err := s.FindAll(cat, scope, ident, inst, ps)
		if err != nil {
			return failureResult(v, "TupleStore findAll: "+err.Error())
		}

		elems := make([]vm.Value, len(rows))
		for i, row := range rows {
			elems[i] = tupleRowToDict(v, row)
		}
		return v.NewArrayWithElements(elems)
	})

	// delete: anInteger — Delete a tuple by its ID. Returns true if deleted, false otherwise.
	storeClass.AddMethod1(vmInst.Selectors, "delete:", func(vmPtr interface{}, recv vm.Value, idVal vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		s := getStore(v, recv)
		if s == nil {
			return failureResult(v, "Not a TupleStore")
		}

		if !idVal.IsSmallInt() {
			return failureResult(v, "TupleStore delete: requires an integer ID")
		}
		id := idVal.SmallInt()

		deleted, err := s.Delete(id)
		if err != nil {
			return failureResult(v, "TupleStore delete: "+err.Error())
		}
		if deleted {
			return vm.True
		}
		return vm.False
	})

	// deleteByPattern: aDictionary — Delete all matching tuples. Returns count deleted.
	storeClass.AddMethod1(vmInst.Selectors, "deleteByPattern:", func(vmPtr interface{}, recv vm.Value, dictVal vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		s := getStore(v, recv)
		if s == nil {
			return failureResult(v, "Not a TupleStore")
		}

		cat, scope, ident, inst, _ := dictToPattern(v, dictVal)
		count, err := s.DeleteByPattern(cat, scope, ident, inst)
		if err != nil {
			return failureResult(v, "TupleStore deleteByPattern: "+err.Error())
		}
		return vm.FromSmallInt(count)
	})

	// count: aDictionary — Count matching tuples. Returns an integer.
	storeClass.AddMethod1(vmInst.Selectors, "count:", func(vmPtr interface{}, recv vm.Value, dictVal vm.Value) vm.Value {
		v := vmPtr.(*vm.VM)
		s := getStore(v, recv)
		if s == nil {
			return failureResult(v, "Not a TupleStore")
		}

		cat, scope, ident, inst, ps := dictToPattern(v, dictVal)
		count, err := s.Count(cat, scope, ident, inst, ps)
		if err != nil {
			return failureResult(v, "TupleStore count: "+err.Error())
		}
		return vm.FromSmallInt(count)
	})
}
