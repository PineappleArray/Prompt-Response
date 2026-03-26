package poller

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"prompt-response/internal/config"
)

type State struct {
	QueueDepth  int
	Running     int
	KVCacheUtil float64
	Healthy     bool
}

type Poller struct {
	replicas []config.Replica
	states   map[string]State
	mu       sync.RWMutex
	interval time.Duration
	failures map[string]int
}

func New(replicas []config.Replica) *Poller {
	states := make(map[string]State)
	for _, r := range replicas {
		states[r.ID] = State{Healthy: true}
	}
	return &Poller{
		replicas: replicas,
		states:   states,
		interval: 2 * time.Second,
	}
}

func (p *Poller) Start() {
	for _, r := range p.replicas {
		go p.pollLoop(r)
	}
}

func (p *Poller) Snapshot() map[string]State {
	p.mu.RLock()
	defer p.mu.RUnlock()
	copy := make(map[string]State, len(p.states))
	for k, v := range p.states {
		copy[k] = v
	}
	return copy
}

func (p *Poller) pollLoop(r config.Replica) {
	failures := 0
	for {
		state, err := p.scrape(r)
		p.mu.Lock()
		if err != nil {
			failures++
			if failures >= 3 {
				s := p.states[r.ID]
				s.Healthy = false
				p.states[r.ID] = s
			}
		} else {
			failures = 0
			state.Healthy = true
			p.states[r.ID] = state
		}
		p.mu.Unlock()
		time.Sleep(p.interval)
	}
}

func (p *Poller) scrape(r config.Replica) (State, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", r.URL+"/metrics", nil)
	if err != nil {
		return State{}, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return State{}, err
	}
	defer resp.Body.Close()

	return parseMetrics(resp.Body), nil
}

func parseMetrics(r io.Reader) State {
	var s State
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "vllm:num_requests_waiting") {
			s.QueueDepth = int(parseFloat(line))
		}
		if strings.HasPrefix(line, "vllm:num_requests_running") {
			s.Running = int(parseFloat(line))
		}
		if strings.HasPrefix(line, "vllm:gpu_cache_usage_perc") {
			s.KVCacheUtil = parseFloat(line)
		}
	}
	return s
}

func parseFloat(line string) float64 {
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return 0
	}
	v, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil {
		return 0
	}
	return v
}
