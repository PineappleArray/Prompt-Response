package test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

type ModelProfile struct {
	Name              string  `json:"name"`
	ParamsB           float64 `json:"params_b"`
	Quantization      string  `json:"quantization"`
	BytesPerParam     float64 `json:"bytes_per_param"`
	VRAMWeightsGB     float64 `json:"vram_weights_gb"`
	KVCachePerTokenMB float64 `json:"kv_cache_per_token_mb"`
	PrefillTPS        float64 `json:"prefill_tps"`
	DecodeTPS         float64 `json:"decode_tps"`
	MaxConcurrent     int     `json:"max_concurrent"`
}

type Config struct {
	Models map[string]ModelProfile `json:"models"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return &cfg, nil
}

// ---------------------------------------------------------------------------
// Router interface — implement this with your actual routing logic
// ---------------------------------------------------------------------------

type Router interface {
	Route(req Request) string
}

type Request struct {
	Text            string
	InputTokens     int
	MaxOutputTokens int
	IdealModel      string
	Category        string
	QuestionID      int
	Turns           []string
}

// ---------------------------------------------------------------------------
// KeywordRouter — simple baseline router for comparison
// ---------------------------------------------------------------------------

type KeywordRouter struct{}

func (r *KeywordRouter) Route(req Request) string {
	text := strings.ToLower(req.Text)

	codeKeywords := []string{"code", "function", "implement", "debug", "script",
		"parser", "compile", "refactor", "api endpoint", "class", "struct"}
	for _, kw := range codeKeywords {
		if strings.Contains(text, kw) {
			return "code"
		}
	}

	reasoningKeywords := []string{"explain", "why", "step by step", "analyze",
		"compare", "reason", "think through", "evaluate", "pros and cons"}
	for _, kw := range reasoningKeywords {
		if strings.Contains(text, kw) {
			return "reasoning"
		}
	}

	largeKeywords := []string{"essay", "research", "paper", "comprehensive",
		"detailed analysis", "write a report", "in depth"}
	for _, kw := range largeKeywords {
		if strings.Contains(text, kw) {
			return "large"
		}
	}

	if req.InputTokens < 50 && req.MaxOutputTokens < 100 {
		return "small"
	}

	return "reasoning"
}

// ---------------------------------------------------------------------------
// Mock Model
// ---------------------------------------------------------------------------

type MockModel struct {
	Key            string
	Profile        ModelProfile
	activeRequests atomic.Int64
	kvCacheUsedMB  atomic.Int64
	maxKVCacheMB   float64
	mu             sync.Mutex
}

func NewMockModel(key string, p ModelProfile) *MockModel {
	return &MockModel{
		Key:          key,
		Profile:      p,
		maxKVCacheMB: p.VRAMWeightsGB * 1024 * 0.3,
	}
}

type GenerateResult struct {
	Model       string
	TTFT        time.Duration
	TotalTime   time.Duration
	OutputToks  int
	DecodeTPS   float64
	InputToks   int
	RoutedTo    string
	IdealModel  string
	IsCorrect   bool
	Category    string
	QuestionID  int
	RequestText string
}

type GenerateError struct {
	RoutedTo    string
	IdealModel  string
	Reason      string
	RequestText string
}

func (m *MockModel) Generate(inputTokens, maxOutputTokens int) (*GenerateResult, error) {
	active := m.activeRequests.Load()
	if int(active) >= m.Profile.MaxConcurrent {
		return nil, fmt.Errorf("%s at capacity: %d/%d active", m.Key, active, m.Profile.MaxConcurrent)
	}

	cacheNeeded := float64(inputTokens) * m.Profile.KVCachePerTokenMB
	m.mu.Lock()
	currentCache := float64(m.kvCacheUsedMB.Load()) / 1000.0
	if currentCache+cacheNeeded > m.maxKVCacheMB {
		m.mu.Unlock()
		return nil, fmt.Errorf("%s OOM: kv cache exhausted (%.1f + %.1f > %.1f MB)",
			m.Key, currentCache, cacheNeeded, m.maxKVCacheMB)
	}
	m.kvCacheUsedMB.Add(int64(cacheNeeded * 1000))
	m.mu.Unlock()

	m.activeRequests.Add(1)
	defer func() {
		m.activeRequests.Add(-1)
		m.kvCacheUsedMB.Add(-int64(cacheNeeded * 1000))
	}()

	prefillSec := float64(inputTokens) / m.Profile.PrefillTPS

	currentActive := float64(m.activeRequests.Load())
	bandwidthContention := 1.0 + (currentActive-1)*0.15
	effectiveDecodeTPS := m.Profile.DecodeTPS / bandwidthContention
	decodeSec := float64(maxOutputTokens) / effectiveDecodeTPS

	totalSec := prefillSec + decodeSec
	totalSec *= 0.85 + rand.Float64()*0.30

	time.Sleep(time.Duration(totalSec * float64(time.Second)))

	return &GenerateResult{
		Model:      m.Key,
		TTFT:       time.Duration(prefillSec * float64(time.Second)),
		TotalTime:  time.Duration(totalSec * float64(time.Second)),
		OutputToks: maxOutputTokens,
		DecodeTPS:  float64(maxOutputTokens) / decodeSec,
		InputToks:  inputTokens,
	}, nil
}

// ---------------------------------------------------------------------------
// MT-Bench loader
// ---------------------------------------------------------------------------

type MTBenchPrompt struct {
	QuestionID int      `json:"question_id"`
	Category   string   `json:"category"`
	Turns      []string `json:"prompt"`
	Reference  []string `json:"reference"`
}

func LoadMTBench(path string) ([]MTBenchPrompt, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening file: %w", err)
	}
	defer f.Close()

	var prompts []MTBenchPrompt
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var p MTBenchPrompt
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNum, err)
		}
		prompts = append(prompts, p)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading: %w", err)
	}
	return prompts, nil
}

// CategoryModelMap defines the expected routing target for each MT-Bench category.
// Override entries to match your router's logic.
var CategoryModelMap = map[string]string{
	"writing":    "small",
	"roleplay":   "small",
	"extraction": "small",
	"stem":       "reasoning",
	"reasoning":  "reasoning",
	"math":       "reasoning",
	"coding":     "code",
	"humanities": "reasoning",
}

func MTBenchToRequests(prompts []MTBenchPrompt) []Request {
	var out []Request
	for _, p := range prompts {
		if len(p.Turns) == 0 {
			continue
		}
		ideal := CategoryModelMap[p.Category]
		if ideal == "" {
			ideal = "reasoning"
		}
		text := p.Turns[0]
		out = append(out, Request{
			Text:            text,
			InputTokens:     estimateTokens(text),
			MaxOutputTokens: estimateOutputTokens(p.Category),
			IdealModel:      ideal,
			Category:        p.Category,
			QuestionID:      p.QuestionID,
			Turns:           p.Turns,
		})
	}
	return out
}

func estimateTokens(text string) int {
	t := len(text) / 4
	if t < 5 {
		t = 5
	}
	return t
}

func estimateOutputTokens(category string) int {
	switch category {
	case "coding":
		return 500
	case "writing":
		return 400
	case "reasoning", "math", "stem":
		return 600
	case "roleplay":
		return 300
	case "extraction":
		return 200
	case "humanities":
		return 500
	default:
		return 400
	}
}

// ---------------------------------------------------------------------------
// Benchmark harness
// ---------------------------------------------------------------------------

type BenchmarkResults struct {
	Successes    []GenerateResult
	Errors       []GenerateError
	TotalTime    time.Duration
	RequestCount int
}

func RunBenchmark(models map[string]*MockModel, router Router, requests []Request) BenchmarkResults {
	var (
		mu        sync.Mutex
		successes []GenerateResult
		errors    []GenerateError
		wg        sync.WaitGroup
	)

	start := time.Now()

	for _, req := range requests {
		wg.Add(1)
		go func(r Request) {
			defer wg.Done()

			routed := router.Route(r)
			model, ok := models[routed]
			if !ok {
				mu.Lock()
				errors = append(errors, GenerateError{
					RoutedTo:    routed,
					IdealModel:  r.IdealModel,
					Reason:      "unknown model key",
					RequestText: r.Text,
				})
				mu.Unlock()
				return
			}

			result, err := model.Generate(r.InputTokens, r.MaxOutputTokens)
			mu.Lock()
			if err != nil {
				errors = append(errors, GenerateError{
					RoutedTo:    routed,
					IdealModel:  r.IdealModel,
					Reason:      err.Error(),
					RequestText: r.Text,
				})
			} else {
				result.RoutedTo = routed
				result.IdealModel = r.IdealModel
				result.IsCorrect = routed == r.IdealModel
				result.Category = r.Category
				result.QuestionID = r.QuestionID
				result.RequestText = r.Text
				successes = append(successes, *result)
			}
			mu.Unlock()
		}(req)
	}

	wg.Wait()

	return BenchmarkResults{
		Successes:    successes,
		Errors:       errors,
		TotalTime:    time.Since(start),
		RequestCount: len(requests),
	}
}

// ---------------------------------------------------------------------------
// Metrics helpers
// ---------------------------------------------------------------------------

func (br BenchmarkResults) Accuracy() float64 {
	if len(br.Successes) == 0 {
		return 0
	}
	correct := 0
	for _, s := range br.Successes {
		if s.IsCorrect {
			correct++
		}
	}
	return float64(correct) / float64(len(br.Successes))
}

func (br BenchmarkResults) AccuracyByCategory() map[string][2]int {
	m := make(map[string][2]int) // [correct, total]
	for _, s := range br.Successes {
		entry := m[s.Category]
		entry[1]++
		if s.IsCorrect {
			entry[0]++
		}
		m[s.Category] = entry
	}
	return m
}

func (br BenchmarkResults) Misrouted() []GenerateResult {
	var out []GenerateResult
	for _, s := range br.Successes {
		if !s.IsCorrect {
			out = append(out, s)
		}
	}
	return out
}

func (br BenchmarkResults) LatenciesMS(modelKey string) []float64 {
	var out []float64
	for _, s := range br.Successes {
		if s.RoutedTo == modelKey {
			out = append(out, s.TotalTime.Seconds()*1000)
		}
	}
	return out
}

func Percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)
	idx := p / 100.0 * float64(len(sorted)-1)
	lower := int(math.Floor(idx))
	upper := int(math.Ceil(idx))
	if lower == upper {
		return sorted[lower]
	}
	frac := idx - float64(lower)
	return sorted[lower]*(1-frac) + sorted[upper]*frac
}

// BuildMockModels creates MockModel instances from a config file.
func BuildMockModels(configPath string) (map[string]*MockModel, error) {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	models := make(map[string]*MockModel, len(cfg.Models))
	for key, profile := range cfg.Models {
		models[key] = NewMockModel(key, profile)
	}
	return models, nil
}
