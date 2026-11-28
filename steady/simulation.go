package steady

import (
	"container/heap"
	"math/rand"
	"time"

	"prescreen.local/ps-9f073c0c88/retry"
)

// simulator bundles every mutable object of one DES run.
type simulator struct {
	topo   *indexedTopology
	cfg    SimConfig
	rng    *rand.Rand
	clock  *retry.SimScheduler
	events eventHeap
	seq    int64

	controllers []*retry.EdgeController
	edgeByIndex map[int]int // topology edge index -> controllers slot

	// raw counters, split into total and warmup snapshots.
	totalNormal       []int64
	totalPhysical     []int64
	totalFailure      []int64
	warmNormal        []int64
	warmPhysical      []int64
	warmFailure       []int64
	totalCalls        []int64
	totalRetries      []int64
	totalHedges       []int64
	warmCalls         []int64
	warmRetries       []int64
	warmHedges        []int64
	totalEdgeAttempts []int64
	warmEdgeAttempts  []int64

	ingressTotal []int64
	ingressWarm  []int64
	reqSeq       uint64
}

// Simulate runs the discrete-event simulation of t and returns the measured
// steady-state rates over the post-warmup window. Every admission, retry,
// hedge, budget and deadline decision is made by a real
// retry.EdgeController, which is the same component used in production.
func Simulate(t *Topology, cfg SimConfig) (*SimResult, error) {
	it, err := indexTopology(t)
	if err != nil {
		return nil, err
	}
	if cfg.Duration <= 0 {
		cfg.Duration = 60 * time.Second
	}
	if cfg.ServiceScale <= 0 {
		cfg.ServiceScale = 1
	}

	s := &simulator{
		topo:              it,
		cfg:               cfg,
		rng:               rand.New(rand.NewSource(cfg.Seed)),
		clock:             retry.NewSimScheduler(),
		edgeByIndex:       map[int]int{},
		totalNormal:       make([]int64, len(it.nodes)),
		totalPhysical:     make([]int64, len(it.nodes)),
		totalFailure:      make([]int64, len(it.nodes)),
		warmNormal:        make([]int64, len(it.nodes)),
		warmPhysical:      make([]int64, len(it.nodes)),
		warmFailure:       make([]int64, len(it.nodes)),
		totalCalls:        make([]int64, len(it.edges)),
		totalRetries:      make([]int64, len(it.edges)),
		totalHedges:       make([]int64, len(it.edges)),
		warmCalls:         make([]int64, len(it.edges)),
		warmRetries:       make([]int64, len(it.edges)),
		warmHedges:        make([]int64, len(it.edges)),
		totalEdgeAttempts: make([]int64, len(it.edges)),
		warmEdgeAttempts:  make([]int64, len(it.edges)),
		ingressTotal:      make([]int64, len(it.nodes)),
		ingressWarm:       make([]int64, len(it.nodes)),
	}
	heap.Init(&s.events)

	for ei, e := range it.edges {
		rcfg := retry.EdgeConfig{
			MaxAttempts:   max1(e.MaxAttempts),
			MaxHedges:     e.MaxHedges,
			HedgeDelay:    e.HedgeDelay,
			MinProcessing: it.nodes[e.v].MinServiceTime,
			Backoff: retry.BackoffConfig{
				BaseDelay:  e.BackoffBase,
				Multiplier: 2,
				MaxDelay:   10 * time.Second,
				Jitter:     0,
				Seed:       uint64(cfg.Seed) + uint64(ei),
			},
			Budget: retry.RetryBudgetConfig{
				BudgetRatio:         e.BudgetRatio,
				MinRetryConcurrency: e.MinRetryConcurrency,
				Window:              cfg.Duration + cfg.Warmup,
			},
			Breaker: retry.BreakerConfig{
				// A high threshold keeps the steady measurement free of
				// open-breaker oscillations; breaker behaviour itself has
				// dedicated runtime unit tests.
				FailureThreshold:    1_000_000,
				OpenDuration:        5 * time.Second,
				HalfOpenMaxAttempts: 1,
			},
		}
		slot := len(s.controllers)
		s.controllers = append(s.controllers, retry.NewEdgeController(rcfg, s.clock))
		s.edgeByIndex[ei] = slot
	}

	s.scheduleIngress(0)
	s.push(&simEvent{at: int64(cfg.Warmup), kind: evEnd, node: -1})
	s.push(&simEvent{at: int64(cfg.Warmup + cfg.Duration), kind: evEnd, node: -2})
	s.run()
	return s.buildResult(), nil
}

func max1(v int) int {
	if v < 1 {
		return 1
	}
	return v
}

func (s *simulator) now() time.Time { return s.clock.Now() }

func (s *simulator) push(ev *simEvent) {
	s.seq++
	ev.seq = s.seq
	heap.Push(&s.events, ev)
}
