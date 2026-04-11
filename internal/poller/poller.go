package poller

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"prompt-response/internal/config"
	"prompt-response/internal/metrics"
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

func New(replicas []config.Replica, interval time.Duration) *Poller {
	states := make(map[string]State)
	for _, r := range replicas {
		states[r.ID] = State{Healthy: true}
	}
	if interval == 0 {
		interval = 2 * time.Second
	}
	return &Poller{
		replicas: replicas,
		states:   states,
		interval: interval,
		failures: make(map[string]int),
	}
}

func (p *Poller) Start(ctx context.Context) {
	for _, r := range p.replicas {
		go p.pollLoop(ctx, r)
	}
}

func (p *Poller) Snapshot() map[string]State {
	p.mu.RLock()
	defer p.mu.RUnlock()
	cp := make(map[string]State, len(p.states))
	for k, v := range p.states {
		cp[k] = v
	}
	return cp
}

func (p *Poller) pollLoop(ctx context.Context, r config.Replica) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("poller stopping", "replica", r.ID)
			return
		case <-ticker.C:
			state, err := p.scrape(r)
			p.mu.Lock()
			if err != nil {
				p.failures[r.ID]++
				if p.failures[r.ID] >= 3 {
					s := p.states[r.ID]
					if s.Healthy {
						slog.Warn("replica marked unhealthy by poller", "replica", r.ID, "consecutive_failures", p.failures[r.ID])
					}
					s.Healthy = false
					p.states[r.ID] = s
				}
			} else {
				if p.failures[r.ID] >= 3 {
					slog.Info("replica recovered", "replica", r.ID)
				}
				p.failures[r.ID] = 0
				state.Healthy = true
				p.states[r.ID] = state

				metrics.ReplicaKVCacheUtil.WithLabelValues(r.ID).Set(state.KVCacheUtil)
			}
			p.mu.Unlock()
		}
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

// SimulateFailure increments the failure counter for a replica (for testing).
func (p *Poller) SimulateFailure(replicaID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failures[replicaID]++
	if p.failures[replicaID] >= 3 {
		s := p.states[replicaID]
		s.Healthy = false
		p.states[replicaID] = s
	}
}

// SetState directly sets the state of a replica (for testing).
func (p *Poller) SetState(replicaID string, state State) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.states[replicaID] = state
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
