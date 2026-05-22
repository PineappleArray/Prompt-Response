package types

import (
	"time"
)

type ModelTier struct {
	Name       string
	Priority   int
	ScoreRange [2]float64
}

type Replica struct {
	ID    string
	URL   string
	Model string
	Tier  ModelTier
}

type ReplicaHealth struct {
	ReplicaID  string
	Healthy    bool
	KVUsage    float64
	QueueDepth int
	LastPoll   time.Time
}

type ReplicaList struct {
	Replicas []Replica
}

func (t ModelTier) String() string {
	return t.Name
}

func (r ReplicaList) ValidTier(t ModelTier) bool {
	for _, replica := range r.Replicas {
		if replica.Tier.Name == t.Name {
			return true
		}
	}
	return false
}

func compareTiers(a, b ModelTier) int {
	if a.Priority < b.Priority {
		return -1
	} else if a.Priority > b.Priority {
		return 1
	}
	return 0
}
