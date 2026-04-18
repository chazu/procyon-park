# Git Workflow Standards

This document defines conventions for commits, changelog maintenance, and release tagging. Follow these conventions consistently across all changes.

## Semantic Commits

Use the [Conventional Commits](https://www.conventionalcommits.org/) specification for all commit messages.

### Format

```
<type>(<scope>): <subject>

<body>

<footer>
```

### Types

- **feat**: A new feature (correlates with MINOR in semver)
- **fix**: A bug fix (correlates with PATCH in semver)
- **docs**: Documentation-only changes
- **style**: Formatting, whitespace, missing semicolons — no code change
- **refactor**: Code change that neither fixes a bug nor adds a feature
- **perf**: Performance improvement
- **test**: Adding or correcting tests
- **build**: Changes to build system or dependencies
- **chore**: Maintenance tasks, tooling, config — no production code change
- **revert**: Reverts a previous commit

### Rules

- **Subject line**: Imperative mood ("add" not "added"), lowercase, no trailing period, ≤72 characters.
- **Scope** (optional): A noun describing the affected area (e.g., `auth`, `parser`, `api`). Use consistently across the project.
- **Body** (optional): Wrap at 72 characters. Explain *what* and *why*, not *how*. Separate from subject with a blank line.
- **Footer** (optional): Reference issues (`Refs: #123`, `Closes: #456`) or note breaking changes.

### Breaking Changes

Indicate a breaking change in one of two ways (either is sufficient; both is fine):

1. Append `!` after the type/scope: `feat(api)!: remove deprecated endpoints`
2. Add a `BREAKING CHANGE:` footer describing the change and migration path.

Breaking changes trigger a MAJOR version bump regardless of the commit type.

### Examples

```
feat(parser): support YAML frontmatter in markdown files

fix(auth): prevent token expiry race condition on refresh

Tokens were being validated before the refresh completed, causing
spurious 401 responses under concurrent load.

Closes: #214
```

```
refactor(storage)!: replace synchronous API with promises

BREAKING CHANGE: All storage.get/set/delete calls now return promises.
Callers must be updated to await or .then() these calls.
```

## Changelog

Maintain a `CHANGELOG.md` at the repository root following the [Keep a Changelog](https://keepachangelog.com/) format.

### Structure

```markdown
# Changelog

All notable changes to this project are documented in this file.

The format is based on Keep a Changelog, and this project adheres to
Semantic Versioning.

## [Unreleased]

### Added
- New features landed since the last release.

### Changed
- Changes to existing functionality.

### Deprecated
- Features scheduled for removal.

### Removed
- Features removed in this release.

### Fixed
- Bug fixes.

### Security
- Vulnerability fixes and security-relevant changes.

## [1.2.0] - 2026-03-15

### Added
- Support for YAML frontmatter in markdown files.

### Fixed
- Token expiry race condition on refresh.
```

### Maintenance Rules

- **Always update `[Unreleased]` in the same commit as the change it describes.** Do not defer changelog edits to a separate commit.
- Omit section headers (Added, Changed, etc.) that have no entries in a given release.
- Write entries for humans reading release notes — not as a mirror of commit subjects. Summarize user-visible impact.
- Do not include `chore`, `style`, `refactor`, `test`, or internal `build` changes unless they affect users.
- Reference issue or PR numbers where useful: `- Fixed token expiry race condition (#214).`
- Keep entries in reverse chronological order within each section (newest first).

## Version Tagging

Follow [Semantic Versioning](https://semver.org/): `MAJOR.MINOR.PATCH`.

- **MAJOR**: Incompatible API changes (any commit with `!` or `BREAKING CHANGE:`).
- **MINOR**: Backwards-compatible new functionality (any `feat` commit).
- **PATCH**: Backwards-compatible bug fixes (`fix`, `perf`, and similar).

Pre-1.0 projects may treat MINOR bumps as potentially breaking; document this explicitly in the README if so.

### Release Procedure

When cutting a release, perform these steps in a single release commit:

1. **Determine the new version** by scanning commits since the last tag:
   - Any breaking change → MAJOR bump.
   - Any `feat` without breaking change → MINOR bump.
   - Only `fix`/`perf`/etc. → PATCH bump.

2. **Update `CHANGELOG.md`**:
   - Rename the `[Unreleased]` heading to `[X.Y.Z] - YYYY-MM-DD` (ISO 8601 date).
   - Add a fresh empty `[Unreleased]` section above it.

3. **Update version references** in any manifest files (`package.json`, `Cargo.toml`, `pyproject.toml`, `VERSION`, etc.).

4. **Commit** with message: `chore(release): vX.Y.Z`

5. **Tag** the release commit:
   ```
   git tag -a vX.Y.Z -m "Release vX.Y.Z"
   ```
   - Use annotated tags (`-a`), not lightweight tags — they carry metadata and are signable.
   - Prefix with lowercase `v` (e.g., `v1.4.0`).
   - For pre-releases, use SemVer suffixes: `v1.4.0-rc.1`, `v2.0.0-beta.2`.

6. **Push** the commit and tag:
   ```
   git push origin <branch>
   git push origin vX.Y.Z
   ```

### Signed Tags

If the project uses GPG/SSH signing, use `git tag -s vX.Y.Z -m "..."` instead of `-a`. Verify with `git tag -v vX.Y.Z`.

### Hotfix Releases

For urgent patches against an older release line:

1. Branch from the tag: `git checkout -b hotfix/X.Y.(Z+1) vX.Y.Z`
2. Apply the fix, update the changelog, and follow the release procedure above.
3. Merge or cherry-pick the fix forward into the main branch as appropriate.

## Summary Checklist

Before committing:
- [ ] Commit message follows Conventional Commits format.
- [ ] Breaking changes are marked with `!` or `BREAKING CHANGE:`.
- [ ] `CHANGELOG.md` `[Unreleased]` section updated if user-visible.

Before tagging a release:
- [ ] Version bump matches the nature of changes (MAJOR/MINOR/PATCH).
- [ ] Changelog `[Unreleased]` promoted to versioned section with date.
- [ ] Fresh empty `[Unreleased]` section added.
- [ ] Manifest file versions updated.
- [ ] Annotated (or signed) tag created with `v` prefix.
- [ ] Commit and tag pushed.

<!-- gitnexus:start -->

# PUDL Observation Gathering

On startup, run "nous guide observations" to understand the observation protocol. For more information you MAY run "nous guide" to see what other info is available. You are expected to observe the observation protocol.

# GitNexus — Code Intelligence

This project is indexed by GitNexus as **procyon-park** (365 symbols, 344 relationships, 0 execution flows). Use the GitNexus MCP tools to understand code, assess impact, and navigate safely.

> If any GitNexus tool warns the index is stale, run `npx gitnexus analyze` in terminal first.

## Always Do

- **MUST run impact analysis before editing any symbol.** Before modifying a function, class, or method, run `gitnexus_impact({target: "symbolName", direction: "upstream"})` and report the blast radius (direct callers, affected processes, risk level) to the user.
- **MUST run `gitnexus_detect_changes()` before committing** to verify your changes only affect expected symbols and execution flows.
- **MUST warn the user** if impact analysis returns HIGH or CRITICAL risk before proceeding with edits.
- When exploring unfamiliar code, use `gitnexus_query({query: "concept"})` to find execution flows instead of grepping. It returns process-grouped results ranked by relevance.
- When you need full context on a specific symbol — callers, callees, which execution flows it participates in — use `gitnexus_context({name: "symbolName"})`.

## When Debugging

1. `gitnexus_query({query: "<error or symptom>"})` — find execution flows related to the issue
2. `gitnexus_context({name: "<suspect function>"})` — see all callers, callees, and process participation
3. `READ gitnexus://repo/procyon-park/process/{processName}` — trace the full execution flow step by step
4. For regressions: `gitnexus_detect_changes({scope: "compare", base_ref: "main"})` — see what your branch changed

## When Refactoring

- **Renaming**: MUST use `gitnexus_rename({symbol_name: "old", new_name: "new", dry_run: true})` first. Review the preview — graph edits are safe, text_search edits need manual review. Then run with `dry_run: false`.
- **Extracting/Splitting**: MUST run `gitnexus_context({name: "target"})` to see all incoming/outgoing refs, then `gitnexus_impact({target: "target", direction: "upstream"})` to find all external callers before moving code.
- After any refactor: run `gitnexus_detect_changes({scope: "all"})` to verify only expected files changed.

## Never Do

- NEVER edit a function, class, or method without first running `gitnexus_impact` on it.
- NEVER ignore HIGH or CRITICAL risk warnings from impact analysis.
- NEVER rename symbols with find-and-replace — use `gitnexus_rename` which understands the call graph.
- NEVER commit changes without running `gitnexus_detect_changes()` to check affected scope.

## Tools Quick Reference

| Tool | When to use | Command |
|------|-------------|---------|
| `query` | Find code by concept | `gitnexus_query({query: "auth validation"})` |
| `context` | 360-degree view of one symbol | `gitnexus_context({name: "validateUser"})` |
| `impact` | Blast radius before editing | `gitnexus_impact({target: "X", direction: "upstream"})` |
| `detect_changes` | Pre-commit scope check | `gitnexus_detect_changes({scope: "staged"})` |
| `rename` | Safe multi-file rename | `gitnexus_rename({symbol_name: "old", new_name: "new", dry_run: true})` |
| `cypher` | Custom graph queries | `gitnexus_cypher({query: "MATCH ..."})` |

## Impact Risk Levels

| Depth | Meaning | Action |
|-------|---------|--------|
| d=1 | WILL BREAK — direct callers/importers | MUST update these |
| d=2 | LIKELY AFFECTED — indirect deps | Should test |
| d=3 | MAY NEED TESTING — transitive | Test if critical path |

## Resources

| Resource | Use for |
|----------|---------|
| `gitnexus://repo/procyon-park/context` | Codebase overview, check index freshness |
| `gitnexus://repo/procyon-park/clusters` | All functional areas |
| `gitnexus://repo/procyon-park/processes` | All execution flows |
| `gitnexus://repo/procyon-park/process/{name}` | Step-by-step execution trace |

## Self-Check Before Finishing

Before completing any code modification task, verify:
1. `gitnexus_impact` was run for all modified symbols
2. No HIGH/CRITICAL risk warnings were ignored
3. `gitnexus_detect_changes()` confirms changes match expected scope
4. All d=1 (WILL BREAK) dependents were updated

## Keeping the Index Fresh

After committing code changes, the GitNexus index becomes stale. Re-run analyze to update it:

```bash
npx gitnexus analyze
```

If the index previously included embeddings, preserve them by adding `--embeddings`:

```bash
npx gitnexus analyze --embeddings
```

To check whether embeddings exist, inspect `.gitnexus/meta.json` — the `stats.embeddings` field shows the count (0 means no embeddings). **Running analyze without `--embeddings` will delete any previously generated embeddings.**

> Claude Code users: A PostToolUse hook handles this automatically after `git commit` and `git merge`.

## CLI

| Task | Read this skill file |
|------|---------------------|
| Understand architecture / "How does X work?" | `.claude/skills/gitnexus/gitnexus-exploring/SKILL.md` |
| Blast radius / "What breaks if I change X?" | `.claude/skills/gitnexus/gitnexus-impact-analysis/SKILL.md` |
| Trace bugs / "Why is X failing?" | `.claude/skills/gitnexus/gitnexus-debugging/SKILL.md` |
| Rename / extract / split / refactor | `.claude/skills/gitnexus/gitnexus-refactoring/SKILL.md` |
| Tools, resources, schema reference | `.claude/skills/gitnexus/gitnexus-guide/SKILL.md` |
| Index, status, clean, wiki CLI commands | `.claude/skills/gitnexus/gitnexus-cli/SKILL.md` |

<!-- gitnexus:end -->
