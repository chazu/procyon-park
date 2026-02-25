package bbs

import (
	"testing"
)

// ---------------------------------------------------------------------------
// feedbackConventionPruning tests
// ---------------------------------------------------------------------------

func TestFeedbackConventionPruning_NoWarmDir(t *testing.T) {
	store := newTestStore(t)
	cfg := FeedbackConfig{WarmBaseDir: ""}

	result := RunFeedbackCycle(store, cfg)
	if result.ConventionsPruned != 0 || result.ConventionsKept != 0 {
		t.Errorf("expected 0/0 without WarmBaseDir, got %d/%d",
			result.ConventionsPruned, result.ConventionsKept)
	}
}

func TestFeedbackConventionPruning_NoResults(t *testing.T) {
	store := newTestStore(t)
	cfg := FeedbackConfig{
		WarmBaseDir:        t.TempDir(), // empty dir, no parquet files
		ConventionMinTasks: 3,
	}

	pruned, kept, err := feedbackConventionPruning(store, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pruned != 0 || kept != 0 {
		t.Errorf("expected 0/0, got %d/%d", pruned, kept)
	}
}

// ---------------------------------------------------------------------------
// feedbackObstacleSurfacing tests
// ---------------------------------------------------------------------------

func TestFeedbackObstacleSurfacing_NoResults(t *testing.T) {
	store := newTestStore(t)
	cfg := FeedbackConfig{
		WarmBaseDir:            t.TempDir(),
		ObstacleMinOccurrences: 2,
	}

	surfaced, err := feedbackObstacleSurfacing(store, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if surfaced != 0 {
		t.Errorf("expected 0, got %d", surfaced)
	}
}

// ---------------------------------------------------------------------------
// feedbackRepoHealth tests
// ---------------------------------------------------------------------------

func TestFeedbackRepoHealth_NoResults(t *testing.T) {
	store := newTestStore(t)
	cfg := FeedbackConfig{
		WarmBaseDir: t.TempDir(),
	}

	updated, err := feedbackRepoHealth(store, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated != 0 {
		t.Errorf("expected 0, got %d", updated)
	}
}

// ---------------------------------------------------------------------------
// feedbackWorkflowSignatures tests
// ---------------------------------------------------------------------------

func TestFeedbackWorkflowSignatures_NoResults(t *testing.T) {
	store := newTestStore(t)
	cfg := FeedbackConfig{
		WarmBaseDir: t.TempDir(),
	}

	cached, err := feedbackWorkflowSignatures(store, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cached != 0 {
		t.Errorf("expected 0, got %d", cached)
	}
}

// ---------------------------------------------------------------------------
// feedbackKnowledgeFlow tests
// ---------------------------------------------------------------------------

func TestFeedbackKnowledgeFlow_NoResults(t *testing.T) {
	store := newTestStore(t)
	cfg := FeedbackConfig{
		WarmBaseDir: t.TempDir(),
	}

	flows, err := feedbackKnowledgeFlow(store, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if flows != 0 {
		t.Errorf("expected 0, got %d", flows)
	}
}

// ---------------------------------------------------------------------------
// RunFeedbackCycle integration tests
// ---------------------------------------------------------------------------

func TestRunFeedbackCycle_NoOp(t *testing.T) {
	store := newTestStore(t)
	cfg := FeedbackConfig{
		WarmBaseDir: t.TempDir(),
	}

	result := RunFeedbackCycle(store, cfg)
	if result.ConventionsPruned != 0 || result.ConventionsKept != 0 ||
		result.ObstaclesSurfaced != 0 || result.RepoHealthUpdated != 0 ||
		result.SignaturesCached != 0 || result.KnowledgeFlows != 0 {
		t.Errorf("expected all zeros, got %+v", result)
	}
	if len(result.Errors) != 0 {
		t.Errorf("expected no errors, got %v", result.Errors)
	}
}

func TestRunFeedbackCycle_WithoutWarmBaseDir(t *testing.T) {
	store := newTestStore(t)
	cfg := FeedbackConfig{}

	result := RunFeedbackCycle(store, cfg)
	if result.ConventionsPruned != 0 || result.ConventionsKept != 0 {
		t.Errorf("expected all zeros without WarmBaseDir, got %+v", result)
	}
}

// ---------------------------------------------------------------------------
// feedbackDefaults tests
// ---------------------------------------------------------------------------

func TestFeedbackDefaults(t *testing.T) {
	cfg := FeedbackConfig{}
	feedbackDefaults(&cfg)

	if cfg.Interval != 24*60*60*1e9 { // 24h in nanoseconds
		t.Errorf("expected 24h interval, got %v", cfg.Interval)
	}
	if cfg.ConventionMinTasks != 3 {
		t.Errorf("expected 3, got %d", cfg.ConventionMinTasks)
	}
	if cfg.ObstacleMinOccurrences != 2 {
		t.Errorf("expected 2, got %d", cfg.ObstacleMinOccurrences)
	}
}

// ---------------------------------------------------------------------------
// helper function tests
// ---------------------------------------------------------------------------

func TestRepoHealthSummary(t *testing.T) {
	ap := AgentPerformance{
		Scope:                "repo",
		ObstacleCount:        3,
		ArtifactCount:        6,
		DistinctAgents:       2,
		ArtifactObstacleRate: 2.0,
	}
	summary := repoHealthSummary(ap)
	if summary == "" {
		t.Error("expected non-empty summary")
	}
}

func TestRepoHealthSummary_NoObstacles(t *testing.T) {
	ap := AgentPerformance{
		Scope:          "repo",
		ObstacleCount:  0,
		ArtifactCount:  5,
		DistinctAgents: 1,
	}
	summary := repoHealthSummary(ap)
	if !containsScope(summary, "N/A") {
		t.Errorf("expected N/A in summary when no obstacles, got %q", summary)
	}
}
