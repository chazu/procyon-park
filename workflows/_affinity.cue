// Shared affinity schema used by workflow templates (F4.1, F4.2).
//
// Affinity expresses routing preferences for task tuples: which workers
// are eligible to claim, which model/repo is required, whether the
// launcher is the only valid claimant, or whether any team worker may
// claim (the default).
//
// This file declares the closed `#Affinity` struct in the `workflows`
// package. To validate a workflow file against this schema, run:
//
//     cue vet workflows/full-pipeline.cue workflows/_affinity.cue
//
// The filename starts with `_` so the Maggie TemplateLoader skips it
// during directory scanning (schema-only — no runnable template).

package workflows

#Affinity: close({
	launcher_only?: bool
	workers?: [...string]
	model?:  string
	repo?:   string
	team?:   bool
})
