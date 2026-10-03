// Package sim is a discrete-time, event-driven, fully deterministic
// simulation framework used to regression-test and debug the compressed
// bitmap implementation. Every run is driven by a seeded RNG, so any
// failure can be reproduced exactly by re-running with the same config.
package sim

import (
	"container/heap"
	"fmt"

	"bitmapsim/roaring"
)

const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
)

// Config controls a simulation run.
type Config struct {
	Seed            uint64 // RNG seed; identical seeds reproduce identical runs
	Steps           int    // number of event-generation steps
	NumBitmaps      int    // bitmaps under test (default 4)
	KeySpace        uint32 // IDs are drawn from [0, KeySpace)
	MaxDenseRun     uint32 // max length of a dense insert/remove (default 4096)
	CheckpointEvery int    // schedule a full verification every N events (default 1024)
	InjectFault     bool   // corrupt bitmap state once, to exercise detection
	FaultStep       int    // event index at which to inject (default Steps/2)
}

func (c Config) withDefaults() Config {
	if c.NumBitmaps <= 0 {
		c.NumBitmaps = 4
	}
	if c.MaxDenseRun == 0 {
		c.MaxDenseRun = 4096
	}
	if c.CheckpointEvery <= 0 {
		c.CheckpointEvery = 1024
	}
	if c.InjectFault && c.FaultStep <= 0 {
		c.FaultStep = c.Steps / 2
	}
	return c
}

// Stats summarizes a finished run.
type Stats struct {
	Events      uint64
	Adds        uint64
	Removes     uint64
	Queries     uint64
	Checkpoints uint64
	// Hash is an FNV-1a fingerprint of everything the run observed.
	// Identical configs must produce identical hashes.
	Hash uint64
}

// Failure describes a detected invariant violation.
type Failure struct {
	Step uint64
	Seed uint64
	What string
}

func (f *Failure) Error() string {
	return fmt.Sprintf("simulation failure at event %d (seed %d): %s", f.Step, f.Seed, f.What)
}

// Simulator runs a deterministic event-driven simulation.
type Simulator struct {
	cfg     Config
	rng     *RNG
	queue   eventHeap
	seq     uint64
	now     uint64
	bitmaps []*roaring.Bitmap
	models  []model
	hash    uint64
	stats   Stats
	applied uint64
	faulted bool
}

// New creates a simulator for the given config.
func New(cfg Config) *Simulator {
	cfg = cfg.withDefaults()
	s := &Simulator{
		cfg:     cfg,
		rng:     NewRNG(cfg.Seed),
		hash:    fnvOffset,
		bitmaps: make([]*roaring.Bitmap, cfg.NumBitmaps),
		models:  make([]model, cfg.NumBitmaps),
	}
	for i := range s.bitmaps {
		s.bitmaps[i] = roaring.New()
		if uint64(cfg.KeySpace) <= 1<<24 {
			s.models[i] = newWordModel(cfg.KeySpace)
		} else {
			s.models[i] = newMapModel()
		}
	}
	return s
}

// Reproduce reruns a configuration deterministically. A failure observed
// in an earlier run with the same config is guaranteed to reappear at the
// same event with the same message.
func Reproduce(cfg Config) (*Stats, *Failure) {
	return New(cfg).Run()
}

// Run executes the simulation and drains the event queue.
func (s *Simulator) Run() (*Stats, *Failure) {
	for step := 0; step < s.cfg.Steps; step++ {
		s.generate()
		if s.queue.Len() == 0 {
			continue
		}
		if f := s.step(); f != nil {
			return s.finalize(), f
		}
	}
	for s.queue.Len() > 0 {
		if f := s.step(); f != nil {
			return s.finalize(), f
		}
	}
	return s.finalize(), nil
}

func (s *Simulator) step() *Failure {
	e := heap.Pop(&s.queue).(Event)
	s.now = e.Time
	f := s.apply(e)
	s.applied++
	return f
}

func (s *Simulator) finalize() *Stats {
	s.stats.Hash = s.hash
	return &s.stats
}

func (s *Simulator) schedule(kind OpKind, target int, delay uint64) {
	heap.Push(&s.queue, Event{
		Time:   s.now + delay,
		Seq:    s.seq,
		Kind:   kind,
		Target: target,
	})
	s.seq++
}

// generate creates the next event deterministically from the RNG.
func (s *Simulator) generate() {
	target := s.rng.Intn(s.cfg.NumBitmaps)
	delay := uint64(s.rng.Intn(4))
	if s.seq%uint64(s.cfg.CheckpointEvery) == 0 {
		s.schedule(OpCheckpoint, target, delay)
		return
	}
	r := s.rng.Intn(100)
	switch {
	case r < 25:
		s.schedule(OpAddSparse, target, delay)
	case r < 40:
		s.schedule(OpAddDense, target, delay)
	case r < 55:
		s.schedule(OpRemove, target, delay)
	case r < 65:
		s.schedule(OpRemoveRange, target, delay)
	case r < 75:
		s.schedule(OpAnd, target, delay)
	case r < 85:
		s.schedule(OpOr, target, delay)
	default:
		s.schedule(OpXor, target, delay)
	}
}

func (s *Simulator) mix(v uint64) {
	h := s.hash
	for i := 0; i < 8; i++ {
		h ^= uint64(byte(v >> (8 * i)))
		h *= fnvPrime
	}
	s.hash = h
}

func (s *Simulator) randKey() uint32 {
	return uint32(s.rng.Next() % uint64(s.cfg.KeySpace))
}

func (s *Simulator) randRange() (uint64, uint64) {
	start := s.randKey()
	length := uint32(1 + s.rng.Intn(int(s.cfg.MaxDenseRun)))
	end := uint64(start) + uint64(length)
	if end > uint64(s.cfg.KeySpace) {
		end = uint64(s.cfg.KeySpace)
	}
	return uint64(start), end
}

// apply executes one event, updating both the compressed bitmap and the
// reference model, and verifies the results of queries.
func (s *Simulator) apply(e Event) *Failure {
	s.stats.Events++
	if s.cfg.InjectFault && !s.faulted && int(s.applied) == s.cfg.FaultStep {
		s.injectFault()
	}
	switch e.Kind {
	case OpAddSparse:
		n := 1 + s.rng.Intn(16)
		bm := s.bitmaps[e.Target]
		m := s.models[e.Target]
		for i := 0; i < n; i++ {
			v := s.randKey()
			bm.Add(v)
			m.add(v)
			s.mix(uint64(v))
		}
		s.stats.Adds++
	case OpAddDense:
		start, end := s.randRange()
		s.bitmaps[e.Target].AddRange(start, end)
		s.models[e.Target].addRange(uint32(start), uint32(end-1))
		s.mix(start)
		s.stats.Adds++
	case OpRemove:
		v := s.randKey()
		s.bitmaps[e.Target].Remove(v)
		s.models[e.Target].remove(v)
		s.mix(uint64(v))
		s.stats.Removes++
	case OpRemoveRange:
		start, end := s.randRange()
		s.bitmaps[e.Target].RemoveRange(start, end)
		s.models[e.Target].removeRange(uint32(start), uint32(end-1))
		s.mix(start)
		s.stats.Removes++
	case OpAnd, OpOr, OpXor:
		t2 := (e.Target + 1 + s.rng.Intn(s.cfg.NumBitmaps-1)) % s.cfg.NumBitmaps
		var res *roaring.Bitmap
		a, b := s.bitmaps[e.Target], s.bitmaps[t2]
		switch e.Kind {
		case OpAnd:
			res = a.And(b)
		case OpOr:
			res = a.Or(b)
		case OpXor:
			res = a.Xor(b)
		}
		if s.stats.Queries%16 == 0 {
			if err := res.Validate(); err != nil {
				return &Failure{Step: s.applied, Seed: s.cfg.Seed, What: fmt.Sprintf("%s result invalid: %v", e.Kind, err)}
			}
		}
		ma, mb := s.models[e.Target], s.models[t2]
		if _, dense := ma.(*wordModel); dense {
			// Dense key space: exact cardinality check against the model.
			var expected uint64
			switch e.Kind {
			case OpAnd:
				expected = ma.andCount(mb)
			case OpOr:
				expected = ma.orCount(mb)
			case OpXor:
				expected = ma.xorCount(mb)
			}
			if res.Cardinality() != expected {
				return &Failure{Step: s.applied, Seed: s.cfg.Seed, What: fmt.Sprintf("%s cardinality mismatch: got %d, want %d", e.Kind, res.Cardinality(), expected)}
			}
		} else {
			// Huge key space: exact set algebra on the reference model
			// would be too expensive, so verify membership on random
			// samples instead. Checkpoints still verify exactly.
			for i := 0; i < 64; i++ {
				v := s.randKey()
				inA, inB := ma.contains(v), mb.contains(v)
				var want bool
				switch e.Kind {
				case OpAnd:
					want = inA && inB
				case OpOr:
					want = inA || inB
				case OpXor:
					want = inA != inB
				}
				if got := res.Contains(v); got != want {
					return &Failure{Step: s.applied, Seed: s.cfg.Seed, What: fmt.Sprintf("%s membership mismatch at %d: got %v, want %v", e.Kind, v, got, want)}
				}
			}
		}
		s.mix(res.Cardinality())
		s.stats.Queries++
	case OpCheckpoint:
		if f := s.checkpoint(); f != nil {
			return f
		}
		s.stats.Checkpoints++
	}
	return nil
}

// checkpoint verifies every bitmap against its reference model.
func (s *Simulator) checkpoint() *Failure {
	for i, bm := range s.bitmaps {
		if err := bm.Validate(); err != nil {
			return &Failure{Step: s.applied, Seed: s.cfg.Seed, What: fmt.Sprintf("bitmap %d invalid: %v", i, err)}
		}
		want := s.models[i].count()
		if bm.Cardinality() != want {
			return &Failure{Step: s.applied, Seed: s.cfg.Seed, What: fmt.Sprintf("bitmap %d cardinality mismatch: got %d, want %d", i, bm.Cardinality(), want)}
		}
		s.mix(bm.Cardinality())
	}
	// Every 8th checkpoint additionally verifies full set equality.
	if s.stats.Checkpoints%8 == 0 {
		for i, bm := range s.bitmaps {
			m := s.models[i]
			var fail *Failure
			bm.ForEach(func(v uint32) {
				if fail == nil && !m.contains(v) {
					fail = &Failure{Step: s.applied, Seed: s.cfg.Seed, What: fmt.Sprintf("bitmap %d contains %d, absent from reference model", i, v)}
				}
			})
			if fail != nil {
				return fail
			}
		}
	}
	return nil
}

// injectFault corrupts one bitmap in a deterministic way and schedules an
// immediate checkpoint so the corruption is detected.
func (s *Simulator) injectFault() {
	s.faulted = true
	// Guarantee the target bitmap is non-empty so corruption always takes.
	s.bitmaps[0].Add(12345)
	s.models[0].add(12345)
	s.bitmaps[0].DebugCorrupt()
	s.schedule(OpCheckpoint, 0, 1)
}
