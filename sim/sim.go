// Package sim is a deterministic, discrete-time, event-driven
// simulation framework for exercising the DB, WAL and ARIES recovery
// engine, with fault injection and scenario replay for bug
// reproduction.
package sim

import (
	"container/heap"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math/rand"
	"sort"

	"aries/db"
	"aries/recovery"
	"aries/storage"
	"aries/wal"
)

type event struct {
	time uint64
	seq  uint64
	name string
	fn   func(s *Sim) error
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].time != h[j].time {
		return h[i].time < h[j].time
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x interface{}) { *h = append(*h, x.(event)) }
func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Sim is the simulator. All randomness flows from the seed, and events
// are ordered by (time, sequence), so runs are fully deterministic.
type Sim struct {
	Now   uint64
	Store *wal.MemStore
	Disk  *storage.MemDisk
	Pool  *storage.BufferPool
	DB    *db.DB

	Rng *rand.Rand

	pq     eventHeap
	seq    uint64
	steps  uint64
	trace  []string
	Traced bool

	Recoveries int
	LastResult *recovery.Result
	Fail       error
}

// New creates a simulator with fresh in-memory storage.
func New(seed int64) *Sim {
	store := wal.NewMemStore()
	disk := storage.NewMemDisk()
	pool := storage.NewBufferPool(disk)
	w := wal.New(store, 1)
	s := &Sim{
		Store: store,
		Disk:  disk,
		Pool:  pool,
		DB:    db.New(w, pool),
		Rng:   rand.New(rand.NewSource(seed)),
	}
	return s
}

// Schedule queues fn to run delay time units from now.
func (s *Sim) Schedule(delay uint64, name string, fn func(s *Sim) error) {
	s.seq++
	heap.Push(&s.pq, event{time: s.Now + delay, seq: s.seq, name: name, fn: fn})
}

// Run executes events until the queue drains, maxSteps is reached, or
// an event fails. It returns the number of events executed.
func (s *Sim) Run(maxSteps uint64) uint64 {
	for len(s.pq) > 0 && s.steps < maxSteps && s.Fail == nil {
		ev := heap.Pop(&s.pq).(event)
		s.Now = ev.time
		if s.Traced {
			s.trace = append(s.trace, fmt.Sprintf("t=%d #%d %s", ev.time, ev.seq, ev.name))
		}
		if err := ev.fn(s); err != nil {
			s.Fail = fmt.Errorf("event %q at t=%d: %v", ev.name, ev.time, err)
		}
		s.steps++
	}
	return s.steps
}

// Steps reports how many events have executed.
func (s *Sim) Steps() uint64 { return s.steps }

// PendingEvents reports how many events remain queued (0 == no leaked
// work).
func (s *Sim) PendingEvents() int { return len(s.pq) }

// Trace returns the recorded event trace (enable with Traced).
func (s *Sim) Trace() []string { return append([]string(nil), s.trace...) }

// StateHash hashes the durable state: stable log frames plus disk
// pages. Two runs of the same scenario must produce identical hashes.
func (s *Sim) StateHash() uint64 {
	h := fnv.New64a()
	frames, _ := s.Store.ReadStable()
	for _, fr := range frames {
		h.Write(fr)
	}
	snap := s.Disk.Snapshot()
	ids := make([]uint64, 0, len(snap))
	for id := range snap {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		img := snap[id]
		var buf [24]byte
		for i := 0; i < 8; i++ {
			buf[i] = byte(id >> (8 * uint(i)))
			buf[8+i] = byte(img.Value >> (8 * uint(i)))
			buf[16+i] = byte(img.PageLSN >> (8 * uint(i)))
		}
		h.Write(buf[:])
	}
	return h.Sum64()
}

// WorkloadConfig describes a generated workload.
type WorkloadConfig struct {
	NumTxns         int
	NumPages        uint64
	MaxOpsPerTxn    int
	CommitPct       int // percent of txns that commit; rest split abort/uncommitted
	AbortPct        int // percent of txns aborted at runtime; remainder stay uncommitted
	CrashAt         uint64
	CheckpointEvery uint64 // 0 disables periodic checkpoints
	FlushEvery      uint64 // 0 disables periodic page flushes
	PostCrashTxns   int    // transactions to run after recovery
}

// LoadWorkload schedules a deterministic workload derived from the
// simulator's RNG.
//
// ARIES undo restores before-images, which is only correct when no
// transaction overwrites another transaction's uncommitted data. Real
// systems guarantee this with strict two-phase locking, so the workload
// enforces a deterministic page-level lock table: a transaction may
// only update a page that is free or already held by itself, and locks
// are released at commit/abort/crash.
func (s *Sim) LoadWorkload(cfg WorkloadConfig) {
	locks := make(map[uint64]uint64)    // page -> holding txn
	heldBy := make(map[uint64][]uint64) // txn -> held pages
	release := func(txnID uint64) {
		for _, p := range heldBy[txnID] {
			delete(locks, p)
		}
		delete(heldBy, txnID)
	}
	acquire := func(s *Sim, txnID uint64) (uint64, bool) {
		for attempt := 0; attempt < 4; attempt++ {
			p := uint64(s.Rng.Intn(int(cfg.NumPages)))
			if holder, ok := locks[p]; !ok || holder == txnID {
				locks[p] = txnID
				heldBy[txnID] = append(heldBy[txnID], p)
				return p, true
			}
		}
		return 0, false
	}
	for i := 0; i < cfg.NumTxns; i++ {
		txnIdx := i
		s.Schedule(uint64(1+i), "txn.begin", func(s *Sim) error {
			txnID := s.DB.Begin()
			ops := 1 + s.Rng.Intn(cfg.MaxOpsPerTxn)
			var step func(op int) func(*Sim) error
			step = func(op int) func(*Sim) error {
				return func(s *Sim) error {
					if page, ok := acquire(s, txnID); ok {
						val := int64(txnIdx*1000 + op)
						if err := s.DB.Update(txnID, page, val); err != nil {
							return err
						}
					}
					// lock unavailable: deterministic no-op, chain continues
					if op+1 < ops {
						s.Schedule(uint64(1+s.Rng.Intn(3)), "txn.op", step(op+1))
						return nil
					}
					roll := s.Rng.Intn(100)
					switch {
					case roll < cfg.CommitPct:
						s.Schedule(1, "txn.commit", func(s *Sim) error {
							defer release(txnID)
							return s.DB.Commit(txnID)
						})
					case roll < cfg.CommitPct+cfg.AbortPct:
						s.Schedule(1, "txn.abort", func(s *Sim) error {
							defer release(txnID)
							return s.DB.Abort(txnID)
						})
					default:
						// left uncommitted; the crash decides its fate
					}
					return nil
				}
			}
			s.Schedule(1, "txn.op", step(0))
			return nil
		})
	}
	if cfg.CheckpointEvery > 0 {
		for t := cfg.CheckpointEvery; t < cfg.CrashAt; t += cfg.CheckpointEvery {
			t := t
			s.Schedule(t, "checkpoint", func(s *Sim) error {
				s.DB.Checkpoint()
				return nil
			})
		}
	}
	if cfg.FlushEvery > 0 {
		for t := cfg.FlushEvery; t < cfg.CrashAt; t += cfg.FlushEvery {
			t := t
			s.Schedule(t, "page.flush", func(s *Sim) error {
				ids := s.Pool.CachedIDs()
				if len(ids) > 0 {
					s.DB.FlushPage(ids[s.Rng.Intn(len(ids))])
				}
				return nil
			})
		}
	}
	if cfg.CrashAt > 0 {
		s.Schedule(cfg.CrashAt, "CRASH", func(s *Sim) error {
			s.DB.Crash()
			locks = make(map[uint64]uint64)
			heldBy = make(map[uint64][]uint64)
			return nil
		})
		s.Schedule(cfg.CrashAt+1, "RESTART.recover", func(s *Sim) error {
			res, w, err := recovery.Recover(s.Store, s.Pool, s.Disk, nil)
			if err != nil {
				return err
			}
			s.Recoveries++
			s.LastResult = res
			s.Pool.FlushAll()
			s.DB = db.NewAt(w, s.Pool, res.Stats.MaxTxnID+1)
			return nil
		})
		for i := 0; i < cfg.PostCrashTxns; i++ {
			txnIdx := cfg.NumTxns + i
			s.Schedule(cfg.CrashAt+2+uint64(i), "post.begin", func(s *Sim) error {
				txnID := s.DB.Begin()
				page, ok := acquire(s, txnID)
				if ok {
					if err := s.DB.Update(txnID, page, int64(txnIdx)); err != nil {
						return err
					}
				}
				s.Schedule(1, "post.commit", func(s *Sim) error {
					defer release(txnID)
					return s.DB.Commit(txnID)
				})
				return nil
			})
		}
	}
}

// Scenario is a serializable bug-reproduction case: a seed plus a
// workload config fully determines the run.
type Scenario struct {
	Seed     int64          `json:"seed"`
	Config   WorkloadConfig `json:"config"`
	Expected ScenarioExpect `json:"expected"`
}

// ScenarioExpect records the observable outcome of a run.
type ScenarioExpect struct {
	Steps     uint64 `json:"steps"`
	StateHash uint64 `json:"state_hash"`
}

// RunScenario executes a scenario and returns its observable outcome.
func RunScenario(sc Scenario) (ScenarioExpect, error) {
	s := New(sc.Seed)
	s.LoadWorkload(sc.Config)
	s.Run(1 << 62)
	if s.Fail != nil {
		return ScenarioExpect{}, s.Fail
	}
	return ScenarioExpect{Steps: s.Steps(), StateHash: s.StateHash()}, nil
}

// Reproduce reruns a scenario and verifies the outcome matches the
// recorded expectation, enabling deterministic bug reproduction.
func Reproduce(sc Scenario) error {
	got, err := RunScenario(sc)
	if err != nil {
		return err
	}
	if got != sc.Expected {
		return fmt.Errorf("sim: reproduction mismatch: got %+v, want %+v", got, sc.Expected)
	}
	return nil
}

// MarshalScenario serializes a scenario for bug reports.
func MarshalScenario(sc Scenario) ([]byte, error) { return json.MarshalIndent(sc, "", "  ") }

// UnmarshalScenario parses a serialized scenario.
func UnmarshalScenario(data []byte) (Scenario, error) {
	var sc Scenario
	err := json.Unmarshal(data, &sc)
	return sc, err
}
