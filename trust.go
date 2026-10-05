package gsb

import (
	"math"
	"math/rand"
	"sort"
	"sync"
)

// TrustFloor guarantees every peer keeps a non-zero scheduling weight, so a
// low trust score can delay but never starve a peer. Trust is used only for
// probe scheduling: it never gates record acceptance, so a legitimate new
// incarnation can always recover regardless of the source's score.
const TrustFloor = 0.05

// Trust tracks per-peer scores in (0, 1].
type Trust struct {
	mu     sync.Mutex
	scores map[string]float64
}

func NewTrust() *Trust {
	return &Trust{scores: make(map[string]float64)}
}

func (t *Trust) Score(peer string) float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s, ok := t.scores[peer]; ok {
		return s
	}
	return 1.0
}

func (t *Trust) Reward(peer string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.scores[peer]
	if !ok {
		s = 1.0
	}
	s += 0.1
	if s > 1 {
		s = 1
	}
	t.scores[peer] = s
}

// Penalize halves a peer's score, never below TrustFloor.
func (t *Trust) Penalize(peer string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.scores[peer]
	if !ok {
		s = 1.0
	}
	s *= 0.5
	if s < TrustFloor {
		s = TrustFloor
	}
	t.scores[peer] = s
}

// Order returns peers shuffled with probability proportional to their trust
// score (Efraimidis-Spirakis weighted sampling without replacement). Every
// peer keeps weight >= TrustFloor, so scheduling remains fair.
func (t *Trust) Order(peers []string, rnd *rand.Rand) []string {
	type weighted struct {
		peer string
		key  float64
	}
	ws := make([]weighted, 0, len(peers))
	for _, p := range peers {
		w := t.Score(p)
		if w < TrustFloor {
			w = TrustFloor
		}
		u := rnd.Float64()
		if u <= 0 {
			u = 1e-12
		}
		ws = append(ws, weighted{p, math.Pow(u, 1/w)})
	}
	sort.Slice(ws, func(i, j int) bool { return ws[i].key > ws[j].key })
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.peer
	}
	return out
}
