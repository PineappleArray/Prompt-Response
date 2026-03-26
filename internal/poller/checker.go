package poller

import (
	"context"
	"log"
	"net/http"
	"prompt-response/internal/classifier"
	"prompt-response/internal/config"
	"time"
)

/*type Request struct {
	SystemPrompt string // extracted from messages[role=system]
	UserMessage  string // latest messages[role=user]
	TokenCount   int    // pre-counted by proxy handler
	HasCode      bool   // true if ``` or code keywords found
	ConvTurns    int    // number of prior messages in thread
}*/

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

func (p *Poller) SendRequest(req classifier.Request) bool {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()

	select {
	case <-timer.C:
		log.Printf("request timed out: %v", req)
		return false
	default:
		return true
	}
}

func (p *Poller) BackgroundRun(replica config.Replica) {
	for _, r := range p.replicas {
		go p.checkHealth(r)
	}
}
