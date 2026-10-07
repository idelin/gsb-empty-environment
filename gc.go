package objstore

import "sync"

// Tri-color invariant: no black object may point to a white object. The
// mutator upholds it with an insertion (Dijkstra-style) write barrier in
// Tx.Commit: any edge created while a cycle is active shades its target
// gray, so concurrently inserted references always join the retained set.

type gcColor uint8

const (
	white gcColor = iota // not yet reached this cycle; sweep candidate
	gray                 // reached, children not yet scanned
	black                // reached and fully scanned
)

type gcPhase uint8

const (
	gcIdle gcPhase = iota
	gcMarking
	gcSweeping
)

// gcState is the collector's private state. Lock ordering is always
// Store.mu before gcState.mu; the mark loop never holds both at once.
type gcState struct {
	mu     sync.Mutex
	phase  gcPhase
	colors map[string]gcColor // objects absent from the map are implicitly black
	work   []string           // gray stack, only drained while marking
}

// gcHooks lets tests interleave mutations with collector phases.
type gcHooks struct {
	afterMarkStart func() // called once roots have been seeded gray
	beforeSweep    func() // called after marking completes, before sweeping
	sweepStep      func() // called after each swept object
}

func (g *gcState) active() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.phase != gcIdle
}

// shade implements the insertion write barrier. It is a no-op for objects
// allocated after the cycle began: they are absent from colors and thus
// implicitly black (retained).
func (g *gcState) shade(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.phase == gcIdle {
		return
	}
	if c, ok := g.colors[id]; ok && c == white {
		g.colors[id] = gray
		if g.phase == gcMarking {
			g.work = append(g.work, id)
		}
	}
}

// RunGC executes one full mark-sweep cycle to completion, concurrently with
// mutator transactions. Objects reachable from any named root — directly or
// transitively — plus anything shaded by the write barrier survive; the rest
// are durably deleted. Returns ErrGCRunning if a cycle is already active.
func (s *Store) RunGC() error {
	s.gc.mu.Lock()
	if s.gc.phase != gcIdle {
		s.gc.mu.Unlock()
		return ErrGCRunning
	}
	s.gc.phase = gcMarking
	s.gc.colors = make(map[string]gcColor)
	s.gc.work = nil
	s.gc.mu.Unlock()

	// Seed: everything starts white; root heads become gray.
	s.mu.RLock()
	s.gc.mu.Lock()
	for id := range s.objects {
		s.gc.colors[id] = white
	}
	for _, id := range s.roots {
		if _, ok := s.gc.colors[id]; ok {
			s.gc.colors[id] = gray
			s.gc.work = append(s.gc.work, id)
		}
	}
	s.gc.mu.Unlock()
	s.mu.RUnlock()

	if s.hooks.afterMarkStart != nil {
		s.hooks.afterMarkStart()
	}

	s.mark()

	s.gc.mu.Lock()
	s.gc.phase = gcSweeping
	s.gc.mu.Unlock()

	if s.hooks.beforeSweep != nil {
		s.hooks.beforeSweep()
	}

	if err := s.sweep(); err != nil {
		s.gc.mu.Lock()
		s.gc.phase = gcIdle
		s.gc.colors = nil
		s.gc.work = nil
		s.gc.mu.Unlock()
		return err
	}

	s.gc.mu.Lock()
	s.gc.phase = gcIdle
	s.gc.colors = nil
	s.gc.work = nil
	s.gc.mu.Unlock()
	return nil
}

// mark drains the gray work stack. Barrier-shaded objects pushed during this
// phase are processed here too, so edges committed mid-mark are traced.
func (s *Store) mark() {
	for {
		s.gc.mu.Lock()
		if len(s.gc.work) == 0 {
			s.gc.mu.Unlock()
			return
		}
		id := s.gc.work[len(s.gc.work)-1]
		s.gc.work = s.gc.work[:len(s.gc.work)-1]
		s.gc.mu.Unlock()

		s.mu.RLock()
		obj := s.objects[id]
		var refs []string
		if obj != nil {
			refs = obj.Refs
		}
		s.mu.RUnlock()

		s.gc.mu.Lock()
		if s.gc.colors[id] == gray {
			s.gc.colors[id] = black
		}
		for _, ref := range refs {
			if c, ok := s.gc.colors[ref]; ok && c == white {
				s.gc.colors[ref] = gray
				s.gc.work = append(s.gc.work, ref)
			}
		}
		s.gc.mu.Unlock()
	}
}

// sweep removes white objects one at a time. Each deletion is logged to the
// WAL and applied under the store lock as one critical section, so a
// concurrent barrier shade always wins the race, and a crash mid-sweep loses
// nothing: logged deletions replay, and not-yet-swept garbage simply
// survives to the next cycle.
func (s *Store) sweep() error {
	for {
		s.mu.RLock()
		s.gc.mu.Lock()
		victim := ""
		for id := range s.objects {
			if c, ok := s.gc.colors[id]; ok && c == white {
				victim = id
				break
			}
		}
		s.gc.mu.Unlock()
		s.mu.RUnlock()
		if victim == "" {
			return nil
		}

		s.mu.Lock()
		s.gc.mu.Lock()
		c, ok := s.gc.colors[victim]
		stillWhite := ok && c == white
		s.gc.mu.Unlock()
		if stillWhite {
			if err := s.appendWAL(walRecord{Dels: []string{victim}}); err != nil {
				s.mu.Unlock()
				return err
			}
			delete(s.objects, victim)
			s.gc.mu.Lock()
			delete(s.gc.colors, victim)
			s.gc.mu.Unlock()
		}
		s.mu.Unlock()

		if s.hooks.sweepStep != nil {
			s.hooks.sweepStep()
		}
	}
}
