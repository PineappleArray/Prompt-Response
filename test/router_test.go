package test

import (
	"math"
	"testing"
	"time"
)

const (
	testConfigPath  = "testdata/model_profiles.json"
	testMTBenchPath = "testdata/question.jsonl"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func mustLoadModels(t *testing.T) map[string]*MockModel {
	t.Helper()
	models, err := BuildMockModels(testConfigPath)
	if err != nil {
		t.Fatalf("loading model profiles: %v", err)
	}
	return models
}

func mustLoadMTBench(t *testing.T) []Request {
	t.Helper()
	prompts, err := LoadMTBench(testMTBenchPath)
	if err != nil {
		t.Fatalf("loading MT-Bench: %v", err)
	}
	return MTBenchToRequests(prompts)
}

// ---------------------------------------------------------------------------
// Config loading
// ---------------------------------------------------------------------------

func TestLoadConfig(t *testing.T) {
	cfg, err := LoadConfig(testConfigPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"small", "code", "reasoning", "large"}
	for _, key := range expected {
		if _, ok := cfg.Models[key]; !ok {
			t.Errorf("missing model key: %s", key)
		}
	}

	small := cfg.Models["small"]
	if small.ParamsB != 1.5 {
		t.Errorf("small params_b = %v, want 1.5", small.ParamsB)
	}
	if small.MaxConcurrent != 64 {
		t.Errorf("small max_concurrent = %d, want 64", small.MaxConcurrent)
	}
}

func TestLoadConfigMissing(t *testing.T) {
	_, err := LoadConfig("nonexistent.json")
	if err == nil {
		t.Fatal("expected error for missing config")
	}
}

// ---------------------------------------------------------------------------
// MT-Bench loading
// ---------------------------------------------------------------------------

func TestLoadMTBench(t *testing.T) {
	prompts, err := LoadMTBench(testMTBenchPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(prompts) == 0 {
		t.Fatal("no prompts loaded")
	}

	// verify structure
	for i, p := range prompts {
		if p.QuestionID == 0 {
			t.Errorf("prompt %d: question_id is 0", i)
		}
		if p.Category == "" {
			t.Errorf("prompt %d (q%d): empty category", i, p.QuestionID)
		}
		if len(p.Turns) == 0 {
			t.Errorf("prompt %d (q%d): no turns", i, p.QuestionID)
		}
	}
}

func TestMTBenchToRequests(t *testing.T) {
	prompts, err := LoadMTBench(testMTBenchPath)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}

	requests := MTBenchToRequests(prompts)
	if len(requests) != len(prompts) {
		t.Errorf("request count %d != prompt count %d", len(requests), len(prompts))
	}

	for _, r := range requests {
		if r.Text == "" {
			t.Errorf("q%d: empty text", r.QuestionID)
		}
		if r.IdealModel == "" {
			t.Errorf("q%d: no ideal model assigned", r.QuestionID)
		}
		if r.InputTokens <= 0 {
			t.Errorf("q%d: input tokens %d <= 0", r.QuestionID, r.InputTokens)
		}
		if r.MaxOutputTokens <= 0 {
			t.Errorf("q%d: max output tokens %d <= 0", r.QuestionID, r.MaxOutputTokens)
		}
	}
}

func TestMTBenchCategories(t *testing.T) {
	prompts, err := LoadMTBench(testMTBenchPath)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}

	categories := make(map[string]int)
	for _, p := range prompts {
		categories[p.Category]++
	}

	t.Logf("loaded %d prompts across %d categories:", len(prompts), len(categories))
	for cat, count := range categories {
		t.Logf("  %-12s %d", cat, count)
	}

	// every category should map to a known model
	for cat := range categories {
		if _, ok := CategoryModelMap[cat]; !ok {
			t.Errorf("category %q has no entry in CategoryModelMap", cat)
		}
	}
}

// ---------------------------------------------------------------------------
// Mock model
// ---------------------------------------------------------------------------

func TestMockModelCapacity(t *testing.T) {
	profile := ModelProfile{
		Name:              "test-model",
		ParamsB:           1,
		VRAMWeightsGB:     1,
		KVCachePerTokenMB: 0.01,
		PrefillTPS:        100000, // fast so tests don't sleep long
		DecodeTPS:         100000,
		MaxConcurrent:     1,
	}
	model := NewMockModel("test", profile)

	// first request should succeed
	result, err := model.Generate(5, 5)
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	if result == nil {
		t.Fatal("nil result")
	}
}

// ---------------------------------------------------------------------------
// Keyword router
// ---------------------------------------------------------------------------

func TestKeywordRouterCode(t *testing.T) {
	router := &KeywordRouter{}
	cases := []struct {
		text string
		want string
	}{
		{"implement a binary search", "code"},
		{"debug this function", "code"},
		{"write a parser for JSON", "code"},
		{"refactor the handler", "code"},
	}
	for _, tc := range cases {
		got := router.Route(Request{Text: tc.text, InputTokens: 50, MaxOutputTokens: 200})
		if got != tc.want {
			t.Errorf("Route(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestKeywordRouterReasoning(t *testing.T) {
	router := &KeywordRouter{}
	cases := []struct {
		text string
		want string
	}{
		{"explain how transformers work", "reasoning"},
		{"why does gravity exist", "reasoning"},
		{"compare TCP and UDP pros and cons", "reasoning"},
		{"analyze this architecture", "reasoning"},
	}
	for _, tc := range cases {
		got := router.Route(Request{Text: tc.text, InputTokens: 50, MaxOutputTokens: 200})
		if got != tc.want {
			t.Errorf("Route(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestKeywordRouterLarge(t *testing.T) {
	router := &KeywordRouter{}
	cases := []struct {
		text string
		want string
	}{
		{"write a comprehensive research paper", "large"},
		{"produce an in depth review", "large"},
	}
	for _, tc := range cases {
		got := router.Route(Request{Text: tc.text, InputTokens: 200, MaxOutputTokens: 2000})
		if got != tc.want {
			t.Errorf("Route(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestKeywordRouterSmall(t *testing.T) {
	router := &KeywordRouter{}
	got := router.Route(Request{Text: "hello", InputTokens: 5, MaxOutputTokens: 10})
	if got != "small" {
		t.Errorf("Route(hello) = %q, want small", got)
	}
}

// ---------------------------------------------------------------------------
// Routing accuracy on MT-Bench (no mock execution, just classification)
// ---------------------------------------------------------------------------

func TestRoutingAccuracyMTBench(t *testing.T) {
	requests := mustLoadMTBench(t)
	router := &KeywordRouter{}

	correct := 0
	var misrouted []Request
	for _, r := range requests {
		got := router.Route(r)
		if got == r.IdealModel {
			correct++
		} else {
			misrouted = append(misrouted, r)
		}
	}

	accuracy := float64(correct) / float64(len(requests)) * 100
	t.Logf("routing accuracy: %d/%d (%.1f%%)", correct, len(requests), accuracy)

	for _, m := range misrouted {
		preview := m.Text
		if len(preview) > 60 {
			preview = preview[:60] + "..."
		}
		t.Logf("  MISS [%s] q%d: routed=%s ideal=%s  %q",
			m.Category, m.QuestionID, router.Route(m), m.IdealModel, preview)
	}

	// fail if accuracy drops below a threshold
	minAccuracy := 50.0 // adjust as your router improves
	if accuracy < minAccuracy {
		t.Errorf("accuracy %.1f%% below minimum %.1f%%", accuracy, minAccuracy)
	}
}

func TestRoutingAccuracyByCategory(t *testing.T) {
	requests := mustLoadMTBench(t)
	router := &KeywordRouter{}

	type stats struct{ correct, total int }
	cats := make(map[string]*stats)

	for _, r := range requests {
		if cats[r.Category] == nil {
			cats[r.Category] = &stats{}
		}
		cats[r.Category].total++
		if router.Route(r) == r.IdealModel {
			cats[r.Category].correct++
		}
	}

	for cat, s := range cats {
		pct := float64(s.correct) / float64(s.total) * 100
		t.Logf("  %-12s  %d/%d (%.0f%%)", cat, s.correct, s.total, pct)
	}
}

// ---------------------------------------------------------------------------
// Full benchmark with mock execution (long test)
// ---------------------------------------------------------------------------

func TestBenchmarkSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping benchmark in short mode")
	}

	models := mustLoadModels(t)
	router := &KeywordRouter{}

	// use a small subset so the test finishes fast
	requests := []Request{
		{Text: "hello", InputTokens: 5, MaxOutputTokens: 10, IdealModel: "small"},
		{Text: "implement a parser", InputTokens: 30, MaxOutputTokens: 100, IdealModel: "code"},
		{Text: "explain gravity step by step", InputTokens: 20, MaxOutputTokens: 150, IdealModel: "reasoning"},
	}

	results := RunBenchmark(models, router, requests)

	if len(results.Errors) > 0 {
		for _, e := range results.Errors {
			t.Errorf("error: %s (routed to %s): %s", e.RequestText, e.RoutedTo, e.Reason)
		}
	}

	if len(results.Successes) != len(requests) {
		t.Errorf("successes = %d, want %d", len(results.Successes), len(requests))
	}

	accuracy := results.Accuracy()
	t.Logf("accuracy: %.0f%%, wall time: %s", accuracy*100, results.TotalTime.Round(time.Millisecond))
}

func TestBenchmarkMTBench(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full MT-Bench benchmark in short mode")
	}

	models := mustLoadModels(t)
	router := &KeywordRouter{}
	requests := mustLoadMTBench(t)

	results := RunBenchmark(models, router, requests)

	// overall
	t.Logf("total: %d  success: %d  errors: %d  wall: %s",
		results.RequestCount, len(results.Successes), len(results.Errors),
		results.TotalTime.Round(time.Millisecond))
	t.Logf("accuracy: %.1f%%", results.Accuracy()*100)

	// per-model latency
	for _, key := range []string{"small", "code", "reasoning", "large"} {
		lat := results.LatenciesMS(key)
		if len(lat) == 0 {
			continue
		}
		t.Logf("  [%s] n=%d  p50=%.0fms  p95=%.0fms  p99=%.0fms",
			key, len(lat),
			Percentile(lat, 50), Percentile(lat, 95), Percentile(lat, 99))
	}

	// per-category accuracy
	for cat, pair := range results.AccuracyByCategory() {
		t.Logf("  %-12s %d/%d", cat, pair[0], pair[1])
	}

	// misrouted
	for _, m := range results.Misrouted() {
		preview := m.RequestText
		if len(preview) > 60 {
			preview = preview[:60] + "..."
		}
		t.Logf("  MISS q%d [%s] → %s (ideal: %s)  %q",
			m.QuestionID, m.Category, m.RoutedTo, m.IdealModel, preview)
	}

	if len(results.Errors) > 0 {
		t.Logf("--- errors ---")
		for _, e := range results.Errors {
			t.Logf("  %s → %s: %s", e.RequestText, e.RoutedTo, e.Reason)
		}
	}
}

// ---------------------------------------------------------------------------
// Latency budget tests
// ---------------------------------------------------------------------------

func TestSmallModelLatencyBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	models := mustLoadModels(t)
	router := &KeywordRouter{}

	requests := []Request{
		{Text: "what is 2+2", InputTokens: 10, MaxOutputTokens: 20, IdealModel: "small"},
		{Text: "hi", InputTokens: 5, MaxOutputTokens: 10, IdealModel: "small"},
		{Text: "define gravity", InputTokens: 8, MaxOutputTokens: 30, IdealModel: "small"},
	}

	results := RunBenchmark(models, router, requests)
	lat := results.LatenciesMS("small")

	p99 := Percentile(lat, 99)
	maxP99 := 500.0 // ms
	t.Logf("small model p99: %.0f ms (budget: %.0f ms)", p99, maxP99)

	if p99 > maxP99 {
		t.Errorf("small model p99 %.0f ms exceeds budget %.0f ms", p99, maxP99)
	}
}

// ---------------------------------------------------------------------------
// Load balance / capacity tests
// ---------------------------------------------------------------------------

func TestLargeModelCapacityUnderLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	models := mustLoadModels(t)
	router := &KeywordRouter{}

	// fire more requests than the large model can handle concurrently (max=4)
	var requests []Request
	for i := 0; i < 6; i++ {
		requests = append(requests, Request{
			Text:            "write a comprehensive research paper",
			InputTokens:     200,
			MaxOutputTokens: 2000,
			IdealModel:      "large",
		})
	}

	results := RunBenchmark(models, router, requests)

	t.Logf("large model: %d success, %d errors out of %d",
		len(results.Successes), len(results.Errors), len(requests))

	if len(results.Errors) == 0 {
		t.Log("warning: no capacity errors — large model may need lower max_concurrent for realistic testing")
	}
}

// ---------------------------------------------------------------------------
// Percentile helper
// ---------------------------------------------------------------------------

func TestPercentile(t *testing.T) {
	vals := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

	cases := []struct {
		p    float64
		want float64
	}{
		{0, 1},
		{50, 5.5},
		{100, 10},
	}
	for _, tc := range cases {
		got := Percentile(vals, tc.p)
		if math.Abs(got-tc.want) > 0.01 {
			t.Errorf("Percentile(p%.0f) = %v, want %v", tc.p, got, tc.want)
		}
	}
}

func TestPercentileEmpty(t *testing.T) {
	got := Percentile(nil, 50)
	if got != 0 {
		t.Errorf("Percentile(nil) = %v, want 0", got)
	}
}
