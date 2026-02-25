// Package bbs provides BBS (Bulletin Board System) higher-level operations
// on top of the tuplestore, including LLM-powered knowledge synthesis.
package bbs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/chazu/procyon-park/internal/tuplestore"
)

// SynthesisConfig holds the configuration for LLM-powered knowledge extraction.
type SynthesisConfig struct {
	// Enabled toggles synthesis on/off. Zero value disables.
	Enabled bool

	// Provider is "anthropic" (default) or "openai".
	Provider string

	// Model is the model identifier to use.
	// Default: "claude-sonnet-4-5-20250929" for Anthropic, "gpt-4o" for OpenAI.
	Model string

	// APIKey is the provider API key. If empty, falls back to
	// ANTHROPIC_API_KEY or OPENAI_API_KEY environment variables.
	APIKey string

	// Timeout for LLM API calls. Default: 90 seconds.
	Timeout time.Duration
}

// KnowledgeTuple represents an extracted knowledge tuple from the LLM.
type KnowledgeTuple struct {
	Category string `json:"category"`
	Scope    string `json:"scope"`
	Identity string `json:"identity"`
	Payload  struct {
		Content string `json:"content"`
	} `json:"payload"`
}

// defaultModel returns the default model for the given provider.
func defaultModel(provider string) string {
	if provider == "openai" {
		return "gpt-4o"
	}
	return "claude-sonnet-4-5-20250929"
}

// resolveAPIKey returns the configured or environment-based API key.
func resolveAPIKey(cfg SynthesisConfig) string {
	if cfg.APIKey != "" {
		return cfg.APIKey
	}
	if cfg.Provider == "openai" {
		return os.Getenv("OPENAI_API_KEY")
	}
	return os.Getenv("ANTHROPIC_API_KEY")
}

// Synthesize runs LLM-powered knowledge extraction on the given tuples.
// It presents the tuple history to an LLM, parses extracted knowledge,
// and writes the results as furniture tuples with instance=synthesized.
//
// Synthesis errors are logged but never returned — this is best-effort.
// The function returns the number of knowledge tuples written.
func Synthesize(ctx context.Context, cfg SynthesisConfig, store *tuplestore.TupleStore,
	tuples []map[string]interface{}, scope string) int {

	if !cfg.Enabled {
		return 0
	}

	if len(tuples) == 0 {
		return 0
	}

	// Resolve provider and model defaults.
	provider := cfg.Provider
	if provider == "" {
		provider = "anthropic"
	}
	model := cfg.Model
	if model == "" {
		model = defaultModel(provider)
	}
	apiKey := resolveAPIKey(cfg)
	if apiKey == "" {
		log.Printf("synthesis: no API key for provider %s, skipping", provider)
		return 0
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 90 * time.Second
	}

	// Build the prompt.
	prompt := BuildPrompt(tuples)

	// Call the LLM.
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	response, err := callLLMStandalone(callCtx, provider, model, apiKey, prompt)
	if err != nil {
		log.Printf("synthesis: LLM call failed: %v", err)
		return 0
	}

	// Parse the response.
	extracted, err := ParseResponse(response)
	if err != nil {
		log.Printf("synthesis: parse response failed: %v", err)
		return 0
	}

	// Write extracted tuples as furniture with instance=synthesized.
	written := 0
	for _, kt := range extracted {
		// Validate: only allow fact or convention categories.
		if kt.Category != "fact" && kt.Category != "convention" {
			continue
		}
		if kt.Identity == "" || kt.Payload.Content == "" {
			continue
		}

		tupleScope := kt.Scope
		if tupleScope == "" {
			tupleScope = scope
		}

		payload, err := json.Marshal(map[string]string{"content": kt.Payload.Content})
		if err != nil {
			continue
		}

		_, err = store.Upsert(
			kt.Category,
			tupleScope,
			kt.Identity,
			"synthesized",
			string(payload),
			"furniture",
			nil, nil, nil,
		)
		if err != nil {
			log.Printf("synthesis: upsert failed for %q: %v", kt.Identity, err)
			continue
		}
		written++
	}

	return written
}

// BuildPrompt constructs the LLM prompt from a slice of tuple rows.
func BuildPrompt(tuples []map[string]interface{}) string {
	var b strings.Builder

	b.WriteString(`You are analyzing a tuplespace history from a multi-agent coordination system.
Each tuple represents a coordination signal between agents working on software tasks.

Below is the complete tuple history for a completed task. Extract durable knowledge
that would help future agents working on similar tasks.

For each piece of knowledge, output a JSON object with:
- "category": either "fact" or "convention"
- "scope": the repository/project scope (use the scope from the tuples)
- "identity": a one-sentence summary (this is the key for deduplication)
- "payload": {"content": "detailed explanation"}

Output ONLY a JSON array of extracted knowledge tuples. No other text.

---
TUPLE HISTORY:
`)

	for _, t := range tuples {
		category, _ := t["category"].(string)
		scope, _ := t["scope"].(string)
		identity, _ := t["identity"].(string)
		agentID, _ := t["agent_id"].(*string)
		lifecycle, _ := t["lifecycle"].(string)
		payload, _ := t["payload"].(string)

		agent := ""
		if agentID != nil {
			agent = *agentID
		}

		fmt.Fprintf(&b, "[%s/%s] %s (agent=%s, lifecycle=%s) -- %s\n",
			category, scope, identity, agent, lifecycle, payload)
	}

	b.WriteString("---\n")
	return b.String()
}

// ParseResponse extracts a JSON array of KnowledgeTuple from an LLM response.
// Handles markdown code fences (```json ... ```) wrapping.
func ParseResponse(response string) ([]KnowledgeTuple, error) {
	text := strings.TrimSpace(response)

	// Strip markdown code fences if present.
	if strings.HasPrefix(text, "```") {
		// Remove opening fence (```json or ```)
		idx := strings.Index(text, "\n")
		if idx >= 0 {
			text = text[idx+1:]
		}
		// Remove closing fence
		if i := strings.LastIndex(text, "```"); i >= 0 {
			text = text[:i]
		}
		text = strings.TrimSpace(text)
	}

	var result []KnowledgeTuple
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("synthesis: parse JSON: %w", err)
	}
	return result, nil
}

// callLLMStandalone dispatches to the appropriate provider.
func callLLMStandalone(ctx context.Context, provider, model, apiKey, prompt string) (string, error) {
	switch provider {
	case "anthropic":
		return callAnthropicStandalone(ctx, model, apiKey, prompt)
	case "openai":
		return callOpenAIStandalone(ctx, model, apiKey, prompt)
	default:
		return "", fmt.Errorf("synthesis: unknown provider %q", provider)
	}
}

// callAnthropicStandalone calls the Anthropic Messages API.
func callAnthropicStandalone(ctx context.Context, model, apiKey, prompt string) (string, error) {
	body := map[string]interface{}{
		"model":      model,
		"max_tokens": 4096,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("synthesis: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("synthesis: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("synthesis: anthropic request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("synthesis: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("synthesis: anthropic status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("synthesis: parse anthropic response: %w", err)
	}
	if len(result.Content) == 0 {
		return "", fmt.Errorf("synthesis: empty anthropic response")
	}
	return result.Content[0].Text, nil
}

// callOpenAIStandalone calls the OpenAI Chat Completions API.
func callOpenAIStandalone(ctx context.Context, model, apiKey, prompt string) (string, error) {
	body := map[string]interface{}{
		"model":      model,
		"max_tokens": 4096,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("synthesis: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("synthesis: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("synthesis: openai request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("synthesis: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("synthesis: openai status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("synthesis: parse openai response: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("synthesis: empty openai response")
	}
	return result.Choices[0].Message.Content, nil
}
