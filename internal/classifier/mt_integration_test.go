package classifier

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// MT-Bench loader
// ---------------------------------------------------------------------------

type mtBenchPrompt struct {
	QuestionID int      `json:"question_id"`
	Category   string   `json:"category"`
	Turns      []string `json:"prompt"`
	Reference  []string `json:"reference"`
}

func loadMTBench(t *testing.T, path string) []mtBenchPrompt {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer f.Close()

	var prompts []mtBenchPrompt
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, len(buf))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var p mtBenchPrompt
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("parsing line: %v", err)
		}
		prompts = append(prompts, p)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("reading file: %v", err)
	}
	return prompts
}

// categoryToExpectedTier maps MT-Bench categories to expected routing tiers.
// Adjust these to match your routing expectations.
var categoryToExpectedTier = map[string]string{
	"writing":    "reasoning",
	"roleplay":   "reasoning",
	"extraction": "small",
	"coding":     "code",
	"math":       "reasoning",
	"reasoning":  "reasoning",
	"stem":       "reasoning",
	"humanities": "reasoning",
}

// ---------------------------------------------------------------------------
// MT-Bench integration test
// ---------------------------------------------------------------------------

func TestIntegrationMTBench(t *testing.T) {
	skipIfNotRunning(t)

	// look for the dataset in common locations
	paths := []string{
		"testdata/question.jsonl",
		"test/testdata/question.jsonl",
		"../../test/testdata/question.jsonl",
	}
	var dataPath string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			dataPath = p
			break
		}
	}
	if dataPath == "" {
		t.Skip("MT-Bench dataset not found — place question.jsonl in testdata/")
	}

	prompts := loadMTBench(t, dataPath)
	t.Logf("loaded %d MT-Bench prompts", len(prompts))

	r := NewRouter(classifierURL())

	type result struct {
		QuestionID   int
		Category     string
		Text         string
		ExpectedTier string
		ActualTier   string
		Score        float64
		BuildReason  string
		Latency      time.Duration
		Correct      bool
		Error        string
	}

	var results []result

	for _, p := range prompts {
		if len(p.Turns) == 0 {
			continue
		}

		text := p.Turns[0]
		expected := categoryToExpectedTier[p.Category]
		if expected == "" {
			expected = "reasoning"
		}

		start := time.Now()
		resp, err := r.Classify(context.Background(), Request{
			UserMessage: text,
			TokenCount:  len(text) / 4,
			HasCode:     strings.Contains(text, "```") || strings.Contains(text, "def ") || strings.Contains(text, "function "),
			ConvTurns:   0,
		})
		elapsed := time.Since(start)

		res := result{
			QuestionID:   p.QuestionID,
			Category:     p.Category,
			ExpectedTier: expected,
			Latency:      elapsed,
		}

		if len(text) > 80 {
			res.Text = text[:80] + "..."
		} else {
			res.Text = text
		}

		if err != nil {
			res.Error = err.Error()
		} else {
			res.ActualTier = string(resp.Tier)
			res.Score = resp.Score
			res.BuildReason = resp.BuildReason
			res.Correct = string(resp.Tier) == expected
		}

		results = append(results, res)
	}

	// --- Overall accuracy ---
	correct := 0
	errored := 0
	var totalLatency time.Duration
	for _, r := range results {
		if r.Error != "" {
			errored++
			continue
		}
		if r.Correct {
			correct++
		}
		totalLatency += r.Latency
	}
	successful := len(results) - errored
	accuracy := 0.0
	if successful > 0 {
		accuracy = float64(correct) / float64(successful) * 100
	}
	avgLatency := time.Duration(0)
	if successful > 0 {
		avgLatency = totalLatency / time.Duration(successful)
	}

	t.Logf("\n========================================")
	t.Logf("  MT-BENCH ROUTING RESULTS")
	t.Logf("========================================")
	t.Logf("  Total prompts:    %d", len(results))
	t.Logf("  Successful:       %d", successful)
	t.Logf("  Errors:           %d", errored)
	t.Logf("  Correct routing:  %d/%d (%.1f%%)", correct, successful, accuracy)
	t.Logf("  Avg latency:      %s", avgLatency.Round(time.Millisecond))
	t.Logf("========================================")

	// --- Per-category breakdown ---
	type catStats struct {
		correct int
		total   int
		tiers   map[string]int
	}
	cats := make(map[string]*catStats)

	for _, r := range results {
		if r.Error != "" {
			continue
		}
		if cats[r.Category] == nil {
			cats[r.Category] = &catStats{tiers: make(map[string]int)}
		}
		cats[r.Category].total++
		cats[r.Category].tiers[r.ActualTier]++
		if r.Correct {
			cats[r.Category].correct++
		}
	}

	// sort categories for consistent output
	var catNames []string
	for name := range cats {
		catNames = append(catNames, name)
	}
	sort.Strings(catNames)

	t.Logf("\n  %-12s  %s  %s  %s", "CATEGORY", "ACC", "CORRECT", "DISTRIBUTION")
	t.Logf("  %-12s  %s  %s  %s", "--------", "---", "-------", "------------")
	for _, name := range catNames {
		s := cats[name]
		pct := float64(s.correct) / float64(s.total) * 100

		// format tier distribution
		var dist []string
		for tier, count := range s.tiers {
			dist = append(dist, fmt.Sprintf("%s:%d", tier, count))
		}
		sort.Strings(dist)

		t.Logf("  %-12s  %5.1f%%  %d/%d     %s",
			name, pct, s.correct, s.total, strings.Join(dist, " "))
	}

	// --- Tier distribution overall ---
	tierCounts := make(map[string]int)
	for _, r := range results {
		if r.Error == "" {
			tierCounts[r.ActualTier]++
		}
	}
	t.Logf("\n  Overall tier distribution:")
	for _, tier := range []string{"small", "code", "reasoning", "large"} {
		count := tierCounts[tier]
		pct := float64(count) / float64(successful) * 100
		bar := strings.Repeat("█", int(pct/2))
		t.Logf("    %-10s  %3d (%5.1f%%)  %s", tier, count, pct, bar)
	}

	// --- Misrouted prompts ---
	var misrouted []result
	for _, r := range results {
		if r.Error == "" && !r.Correct {
			misrouted = append(misrouted, r)
		}
	}

	if len(misrouted) > 0 {
		t.Logf("\n  MISROUTED (%d):", len(misrouted))
		for _, m := range misrouted {
			t.Logf("    q%d [%s] expected=%s got=%s score=%.4f reason=%q",
				m.QuestionID, m.Category, m.ExpectedTier, m.ActualTier, m.Score, m.BuildReason)
			t.Logf("      %q", m.Text)
		}
	}

	// --- Errors ---
	if errored > 0 {
		t.Logf("\n  ERRORS (%d):", errored)
		for _, r := range results {
			if r.Error != "" {
				t.Logf("    q%d [%s]: %s", r.QuestionID, r.Category, r.Error)
			}
		}
	}

	// --- Save results to JSON ---
	type jsonResult struct {
		QuestionID   int     `json:"question_id"`
		Category     string  `json:"category"`
		Text         string  `json:"text"`
		ExpectedTier string  `json:"expected_tier"`
		ActualTier   string  `json:"actual_tier"`
		Score        float64 `json:"score"`
		BuildReason  string  `json:"build_reason"`
		LatencyMs    int64   `json:"latency_ms"`
		Correct      bool    `json:"correct"`
		Error        string  `json:"error,omitempty"`
	}

	var jsonResults []jsonResult
	for _, r := range results {
		jsonResults = append(jsonResults, jsonResult{
			QuestionID:   r.QuestionID,
			Category:     r.Category,
			Text:         r.Text,
			ExpectedTier: r.ExpectedTier,
			ActualTier:   r.ActualTier,
			Score:        r.Score,
			BuildReason:  r.BuildReason,
			LatencyMs:    r.Latency.Milliseconds(),
			Correct:      r.Correct,
			Error:        r.Error,
		})
	}

	summary := map[string]any{
		"total":             len(results),
		"successful":        successful,
		"errors":            errored,
		"correct":           correct,
		"accuracy_pct":      accuracy,
		"avg_latency_ms":    avgLatency.Milliseconds(),
		"categories":        cats,
		"tier_distribution": tierCounts,
		"results":           jsonResults,
	}

	outPath := "testdata/mtbench_results.json"
	out, err := json.MarshalIndent(summary, "", "  ")
	if err == nil {
		if err := os.WriteFile(outPath, out, 0644); err == nil {
			t.Logf("\n  Results saved to %s", outPath)
		}
	}
}
