// Package prime provides a template engine for generating role-specific
// agent instructions. Templates are embedded at compile time and rendered
// with agent-specific data (name, repo, task, etc.).
package prime

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"text/template"
)

//go:embed templates/*.txt
var templateFS embed.FS

// TemplateData holds the typed fields available to role templates.
type TemplateData struct {
	Role      string
	AgentName string
	Repo      string
	TaskID    string
	Branch    string
	Worktree  string
	EnvPrefix string
}

// LoadTemplate loads and parses the template for the given role.
// It follows a fallback chain: if "<role>.txt" is not found, it falls
// back to "cub.txt" (the default role).
func LoadTemplate(role string) (*template.Template, error) {
	name := role + ".txt"
	data, err := templateFS.ReadFile("templates/" + name)
	if err != nil {
		// Fallback to cub.txt
		data, err = templateFS.ReadFile("templates/cub.txt")
		if err != nil {
			return nil, fmt.Errorf("prime: no template for role %q and fallback cub.txt not found", role)
		}
		name = "cub.txt"
	}

	tmpl, err := template.New(name).Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("prime: parse template %s: %w", name, err)
	}
	return tmpl, nil
}

// RenderTemplate loads the template for the given role and renders it
// with the provided data. Returns the rendered string.
func RenderTemplate(role string, data TemplateData) (string, error) {
	tmpl, err := LoadTemplate(role)
	if err != nil {
		return "", err
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("prime: execute template for role %q: %w", role, err)
	}
	return buf.String(), nil
}

// ListRoles returns the sorted list of available role names derived
// from embedded template filenames (without the .txt extension).
func ListRoles() ([]string, error) {
	entries, err := fs.ReadDir(templateFS, "templates")
	if err != nil {
		return nil, fmt.Errorf("prime: read templates dir: %w", err)
	}

	var roles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".txt") {
			roles = append(roles, strings.TrimSuffix(name, ".txt"))
		}
	}
	sort.Strings(roles)
	return roles, nil
}
