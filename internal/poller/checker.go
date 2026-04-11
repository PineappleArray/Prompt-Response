package poller

import (
	"context"
	"log"
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
	if err != nil || resp.StatusCode != http.StatusOK {
		p.recordFailure(r.ID)
		log.Printf("health check failed: replica=%s", r.ID)
		return
	}

	// success — reset counter and mark healthy
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failures[r.ID] = 0
	s := p.states[r.ID]
	s.Healthy = true
	p.states[r.ID] = s
	log.Printf("health check ok: replica=%s", r.ID)
}

func (p *Poller) recordFailure(replicaID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failures[replicaID]++
	if p.failures[replicaID] >= 3 {
		s := p.states[replicaID]
		s.Healthy = false
		p.states[replicaID] = s
		log.Printf("replica marked unhealthy: id=%s", replicaID)
	}
}
