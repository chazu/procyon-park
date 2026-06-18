# Scout: Where is `Categories.valid` defined?

## Answer

`Categories valid` is a **class method** defined in:

- **File:** `src/bbs/Categories.mag`
- **Lines:** 5–10

```smalltalk
classMethod: valid [
  ^#('fact' 'convention' 'observation' 'decision' 'signal'
     'task' 'template' 'workflow' 'token' 'rule' 'ingestion'
     'event' 'obstacle' 'session' 'artifact' 'link' 'notification'
     'workitem' 'identity' 'worker' 'watch' 'invite' 'case')
]
```

It returns a literal array of valid tuplespace category strings (23 categories).

## Related methods (same file)

| Method | Line | Purpose |
|--------|------|---------|
| `valid` | 5 | Returns array of all valid category strings |
| `isValid:` | 12 | `^self valid includes: aString` |
| `validate:` | 16 | Raises `Error` if category not valid |
| `pinned` | 23 | Categories always persistent (never consumed by `in:`) |
| `isPinned:` | 28 | `^self pinned includes: aString` |

Class declared: `Categories subclass: Object` (line 3).

## Consumers (tests reference it)

- `test/bbs/test_case_category.mag:58` — `Categories valid includes: 'case'`
- `test/test_worker_category.mag:112` — `Categories valid includes: 'worker'`

## Verification verdict

Confirmed: `Categories.valid` is defined as a class method in `src/bbs/Categories.mag:5`. No other definition exists in the codebase.
