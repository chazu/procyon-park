# Procyon Park — TODO

Based on observations from orchestrating agents in production (2026-03-31).

## Critical Bugs

### 1. Worktree scoping is broken for cross-repo work

The foreman dispatched agents to procyon-park worktrees, but the tasks targeted alto source files. Agents had to work directly in `~/dev/maggie/alto` instead. The workflow engine needs to support a `repo` field on the template or transition level that controls which repo the worktree is created in — not just inherit from the workflow's registered repo.

### 2. Merge fails silently at workflow end

The `merge-worktree` action tried to checkout `feature/full-pipeline-...` which didn't exist (agents worked outside the worktree). The workflow completed anyway with a "merge-checkout-failed" observation but no actual code integration. The merge step should fail the workflow or at least block completion when it can't merge.

## High-Leverage Improvements

### 3. Make workflow/task categories durable

When the server crashed from the map race, all workflow state was lost — only observations/decisions/conventions survived. Workflows and tasks should persist so a server restart can resume in-flight work. This is a one-line fix in `isDurableCategory:` — add `'workflow'` and `'task'` and `'token'` to the list.

### 4. Repo-aware task dispatch

Right now the `--repo` param on workflow start sets a repo name in params, but `create-worktree` always creates the worktree from `cwd` or `PP_REPO_PATH`. There's no way to say "this workflow operates on the alto repo, create worktrees there." The foreman worked around this by telling agents to edit alto directly, which defeated the worktree isolation model entirely.

### 5. Foreman needs structured sub-task protocol

The foreman agent currently uses `pp task` to create ad-hoc tasks, but there's no formal way to express "these 6 tasks are wave 1 (parallel), then these 2 are wave 2 (sequential after wave 1)." The foreman had to improvise. A `pp dispatch` command or sub-workflow instantiation primitive would make this reliable.

## Medium-Priority

### 6. Agent session capture

We had no visibility into what agents were doing until they finished. Adding `--output-file` to the Claude harness would let you inspect or tail agent sessions. The observation you get at the end is a lossy summary.

### 7. Template scoping for non-system repos

The two-tier CUE template loading works, but the workflow engine always looks up templates with `scope: 'system'`. Repo-scoped templates can be loaded but never matched. `instantiate:` should fall back to repo-scoped templates when system ones aren't found.

### 8. Review cycle count should be capped

The full-pipeline went through 2 review cycles. There's no maximum — a stubborn reviewer/evaluator could loop forever. The `review_cycle` signal exists but nothing enforces a limit. The `exhausted` precondition on the evaluate transition is the escape hatch but relies on the foreman agent choosing it.

### 9. History log grows unbounded

`appendHistory:` appends to `history.jsonl` on every BBS operation with no rotation. A long-running server doing real work will accumulate a large file. Add log rotation or a max-size cap.

## Quick Wins

### 10. `pp log --no-follow` hangs

It sends `wait=0` but still blocks waiting for the HTTP response due to long-poll server behavior. Should return immediately when there are no new notifications.

### 11. Clean up stale worktrees after workflow completion

The `notify-head` action doesn't clean up the worktree directory at `~/.pp/worktrees/<instance>/`. These accumulate across runs. Add cleanup to `completeWorkflow:` or the housekeeping tick.
