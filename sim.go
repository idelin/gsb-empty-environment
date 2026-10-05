package gsb

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sort"
)

// Sim is a deterministic in-memory cluster simulation: engines connected
// by fault-injecting links, driven by a fair polling scheduler.
type Sim struct {
	Engines map[string]*Engine
	Links   map[string]map[string]*Link // from -> to -> link
	Rnd     *rand.Rand
}

func NewSim(nodeIDs []string, numShards int, seed int64) *Sim {
	s := &Sim{
		Engines: make(map[string]*Engine),
		Links:   make(map[string]map[string]*Link),
		Rnd:     rand.New(rand.NewSource(seed)),
	}
	for _, id := range nodeIDs {
		s.Engines[id] = NewEngine(id, NewTable(numShards), NewTrust())
	}
	for _, a := range nodeIDs {
		s.Links[a] = make(map[string]*Link)
		for _, b := range nodeIDs {
			if a != b {
				s.Links[a][b] = &Link{To: s.Engines[b]}
			}
		}
	}
	return s
}

// IDs returns the sorted node IDs.
func (s *Sim) IDs() []string {
	ids := make([]string, 0, len(s.Engines))
	for id := range s.Engines {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Sweep runs one fair polling sweep: every engine syncs once with every
// other engine, peers visited in trust-weighted order. Per-round transport
// faults are absorbed; only context cancellation aborts the sweep.
func (s *Sim) Sweep(ctx context.Context, onRound func(from string, rs RoundStat)) error {
	for _, a := range s.IDs() {
		ea := s.Engines[a]
		var peers []string
		for _, b := range s.IDs() {
			if b != a {
				peers = append(peers, b)
			}
		}
		peers = ea.Trust.Order(peers, s.Rnd)
		for _, b := range peers {
			before := ea.Stats.Len()
			err := ea.Round(ctx, b, s.Links[a][b])
			if onRound != nil && ea.Stats.Len() > before {
				if rs, ok := ea.Stats.Last(); ok {
					onRound(a, rs)
				}
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
		}
	}
	return nil
}

// Model returns the independent full-merge reference over all engines.
func (s *Sim) Model() map[string]Record {
	tables := make([]*Table, 0, len(s.Engines))
	for _, e := range s.Engines {
		tables = append(tables, e.Table)
	}
	return FullModel(tables...)
}

// Converged reports whether every engine's table equals the model.
func (s *Sim) Converged() (string, bool) {
	model := s.Model()
	for _, id := range s.IDs() {
		if msg := CheckModel(s.Engines[id].Table, model); msg != "" {
			return fmt.Sprintf("node %s: %s", id, msg), false
		}
	}
	return "", true
}
