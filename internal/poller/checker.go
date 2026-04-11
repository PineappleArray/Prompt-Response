package poller

import (
	"context"
	"log/slog"
	"net/http"
	"prompt-response/internal/config"
	"time"
)

func (p *Poller) checkHealth(r config.Replica) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", r.URL+"/health", nil)
	if err != nil {
		p.recordFailure(r.ID)
		return
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		p.recordFailure(r.ID)
		slog.Warn("health check failed", "replica", r.ID, "err", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		p.recordFailure(r.ID)
		slog.Warn("health check failed", "replica", r.ID, "status", resp.StatusCode)
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.failures[r.ID] = 0
	s := p.states[r.ID]
	s.Healthy = true
	p.states[r.ID] = s
}

func (p *Poller) recordFailure(replicaID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failures[replicaID]++
	if p.failures[replicaID] >= 3 {
		s := p.states[replicaID]
		if s.Healthy {
			slog.Warn("replica marked unhealthy", "replica", replicaID, "consecutive_failures", p.failures[replicaID])
		}
		s.Healthy = false
		p.states[replicaID] = s
	}
}
