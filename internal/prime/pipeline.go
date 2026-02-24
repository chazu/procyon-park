// Package prime provides the middleware pipeline for generating agent priming
// instructions. The pipeline steps are: loadTemplate → applyUserOverride →
// injectAddendum → injectContext → render. Each step is independently testable.
package prime

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"text/template"

	"github.com/chazu/procyon-park/internal/tuplestore"
)

// DefaultContextBudget is the default approximate token limit for the context
// section. When exceeded, the context is trimmed with an omission notice.
const DefaultContextBudget = 4000

// PipelineConfig configures the prime middleware pipeline.
type PipelineConfig struct {
	// Role is the agent role (e.g., "cub", "king").
	Role string

	// Data holds template rendering data.
	Data TemplateData

	// Scope is the BBS tuplespace scope (typically the repo name).
	Scope string

	// TaskID is the current task identifier.
	TaskID string

	// Store is the tuplespace store for building context. If nil, context
	// injection is skipped.
	Store *tuplestore.TupleStore

	// InstructionsDir overrides the user instructions directory. If empty,
	// defaults to ~/.procyon-park/instructions.
	InstructionsDir string

	// ContextBudget is the approximate token limit for the context section.
	// If zero, DefaultContextBudget is used.
	ContextBudget int
}

// instructionsDir returns the resolved instructions directory path.
func (c *PipelineConfig) instructionsDir() string {
	if c.InstructionsDir != "" {
		return c.InstructionsDir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".procyon-park", "instructions")
}

// contextBudget returns the configured budget or the default.
func (c *PipelineConfig) contextBudget() int {
	if c.ContextBudget > 0 {
		return c.ContextBudget
	}
	return DefaultContextBudget
}

// RunPipeline executes the full prime middleware pipeline:
// loadTemplate → applyUserOverride → injectAddendum → injectContext → render.
// Returns the fully assembled priming text.
func RunPipeline(cfg PipelineConfig) (string, error) {
	// Step 1: Load the embedded template.
	templateText, err := loadTemplateText(cfg.Role)
	if err != nil {
		return "", fmt.Errorf("pipeline: load template: %w", err)
	}

	// Step 2: Apply user override if present.
	templateText = applyUserOverride(templateText, cfg.Role, cfg.instructionsDir())

	// Step 3: Render the template with data.
	rendered, err := renderText(templateText, cfg.Data)
	if err != nil {
		return "", fmt.Errorf("pipeline: render: %w", err)
	}

	// Step 4: Inject BBS addendum.
	rendered = injectAddendum(rendered, cfg.Scope, cfg.TaskID)

	// Step 5: Inject tuplespace context.
	rendered, err = injectContext(rendered, cfg.Store, cfg.Scope, cfg.TaskID, cfg.contextBudget())
	if err != nil {
		return "", fmt.Errorf("pipeline: inject context: %w", err)
	}

	return rendered, nil
}

// loadTemplateText loads the raw template text for a role from the embedded FS.
// Falls back to cub.txt if the role template is not found.
func loadTemplateText(role string) (string, error) {
	name := role + ".txt"
	data, err := templateFS.ReadFile("templates/" + name)
	if err != nil {
		data, err = templateFS.ReadFile("templates/cub.txt")
		if err != nil {
			return "", fmt.Errorf("no template for role %q and fallback cub.txt not found", role)
		}
	}
	return string(data), nil
}

// applyUserOverride checks for a user-customized template at
// <instructionsDir>/<role>.txt. If found, it replaces the embedded template.
// The override template still gets addendum and context appended downstream.
func applyUserOverride(templateText, role, instructionsDir string) string {
	if instructionsDir == "" {
		return templateText
	}
	path := filepath.Join(instructionsDir, role+".txt")
	data, err := os.ReadFile(path)
	if err != nil {
		return templateText
	}
	return string(data)
}

// renderText parses and executes a Go text/template string with the given data.
func renderText(templateText string, data TemplateData) (string, error) {
	tmpl, err := parseTemplate(templateText)
	if err != nil {
		return "", err
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}

// parseTemplate parses a template string into a *template.Template.
func parseTemplate(text string) (*template.Template, error) {
	return template.New("prime").Parse(text)
}

// injectAddendum appends the BBS protocol addendum to the rendered text.
func injectAddendum(rendered, scope, taskID string) string {
	if scope == "" {
		return rendered
	}
	addendum := AgentPromptAddendum(scope, taskID)
	return rendered + "\n" + addendum
}

// injectContext appends the tuplespace context snapshot to the rendered text,
// respecting the token budget. If the context exceeds the budget, it is trimmed
// with an omission notice.
func injectContext(rendered string, store *tuplestore.TupleStore, scope, taskID string, budget int) (string, error) {
	if store == nil {
		return rendered, nil
	}

	context, err := BuildAgentContext(store, scope, taskID)
	if err != nil {
		return "", err
	}

	context = trimToTokenBudget(context, budget)
	return rendered + "\n" + context, nil
}

// estimateTokens returns a rough token count for text. Uses the common
// approximation of ~4 characters per token for English text.
func estimateTokens(text string) int {
	return (len(text) + 3) / 4
}

// trimToTokenBudget trims text to fit within the approximate token budget.
// If trimming is needed, it truncates at line boundaries and appends a notice.
func trimToTokenBudget(text string, budget int) string {
	if budget <= 0 || estimateTokens(text) <= budget {
		return text
	}

	// Target character count (leave room for the omission notice).
	notice := "\n... (context trimmed to fit token budget)\n"
	targetChars := budget*4 - len(notice)
	if targetChars <= 0 {
		return notice
	}

	// Truncate at line boundaries.
	lines := strings.Split(text, "\n")
	var buf strings.Builder
	for _, line := range lines {
		if buf.Len()+len(line)+1 > targetChars {
			break
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	buf.WriteString(notice)
	return buf.String()
}

// ExportTemplates copies all embedded role templates to the user instructions
// directory for customization. Creates the directory if it doesn't exist.
// Does not overwrite existing files.
func ExportTemplates(instructionsDir string) ([]string, error) {
	if instructionsDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cannot determine home directory: %w", err)
		}
		instructionsDir = filepath.Join(home, ".procyon-park", "instructions")
	}

	if err := os.MkdirAll(instructionsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create instructions dir: %w", err)
	}

	entries, err := fs.ReadDir(templateFS, "templates")
	if err != nil {
		return nil, fmt.Errorf("read embedded templates: %w", err)
	}

	var exported []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		destPath := filepath.Join(instructionsDir, e.Name())

		// Don't overwrite existing customizations.
		if _, err := os.Stat(destPath); err == nil {
			continue
		}

		data, err := templateFS.ReadFile("templates/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("read embedded %s: %w", e.Name(), err)
		}

		if err := os.WriteFile(destPath, data, 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", destPath, err)
		}
		exported = append(exported, destPath)
	}

	return exported, nil
}
