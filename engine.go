package stgrid

import "sort"

// Entity is a dynamic traffic unit with a position (m) and velocity (m/s).
type Entity struct {
	ID  string
	Pos Vec3
	Vel Vec3
}

// Breach is a predicted violation of the minimum separation between two
// entities inside the prediction horizon.
type Breach struct {
	A, B         string
	TimeToBreach float64 // seconds from now until separation < MinSep
	MinDistance  float64 // closest approach (m) over the horizon
}

// Beacon is the warning signal emitted once a breach falls inside the
// warning lead time. It carries the current topological distance between
// the two units.
type Beacon struct {
	Time            float64 // simulation time of emission (s)
	A, B            string
	CurrentDistance float64 // current separation of the pair (m)
	TimeToBreach    float64 // seconds until the separation breach
	MinDistance     float64 // predicted closest approach (m)
}

// Engine is the spatiotemporal proximity detection engine. It shards space
// into a 3D hash grid and time into slots, extrapolates each entity's swept
// capsule per slot, and finds collision courses in (amortized) constant
// time per entity.
type Engine struct {
	MinSep       float64 // minimum safe separation (m)
	SlotDuration float64 // length of one time slot (s)
	HorizonSlots int     // prediction horizon, in slots
	WarnLead     float64 // warning lead time before a breach (s)
	CellSize     float64 // spatial hash cell size (m)

	// Diagnostics from the most recent Detect call.
	LastCandidates int // pairs actually distance-tested
	LastBruteForce int // pairs an all-pairs scan would test
	LastCells      int // occupied spatiotemporal cells
}

// NewEngine builds an engine. maxSpeed (m/s) bounds every entity's speed;
// it sizes the hash cells so each swept capsule touches at most 2 cells per
// axis, keeping insertion and lookup O(1).
func NewEngine(minSep, slotDuration float64, horizonSlots int, warnLead, maxSpeed float64) *Engine {
	return &Engine{
		MinSep:       minSep,
		SlotDuration: slotDuration,
		HorizonSlots: horizonSlots,
		WarnLead:     warnLead,
		CellSize:     maxSpeed*slotDuration + minSep,
	}
}

// Horizon returns the prediction horizon in seconds.
func (e *Engine) Horizon() float64 { return float64(e.HorizonSlots) * e.SlotDuration }

// Detect extrapolates every entity over the horizon and returns all pairs
// predicted to breach MinSep, with the earliest breach time per pair.
func (e *Engine) Detect(ents []Entity) []Breach {
	type hit struct{ tBreach float64 }
	earliest := make(map[[2]int]hit)
	grid := NewGrid(e.CellSize)
	radius := e.MinSep / 2
	e.LastCandidates, e.LastBruteForce, e.LastCells = 0, 0, 0

	for slot := 0; slot < e.HorizonSlots; slot++ {
		t0 := float64(slot) * e.SlotDuration
		t1 := t0 + e.SlotDuration
		grid.Reset()
		for i, ent := range ents {
			p0 := ent.Pos.Add(ent.Vel.Scale(t0))
			p1 := ent.Pos.Add(ent.Vel.Scale(t1))
			grid.Insert(i, Capsule{P0: p0, P1: p1, Radius: radius}, slot)
		}
		e.LastCells += len(grid.Cells)
		pairs := grid.CandidatePairs()
		e.LastCandidates += len(pairs)
		e.LastBruteForce += len(ents) * (len(ents) - 1) / 2
		for _, pr := range pairs {
			if _, done := earliest[pr]; done {
				continue // an earlier breach is already recorded
			}
			a, b := ents[pr[0]], ents[pr[1]]
			rel := a.Vel.Sub(b.Vel)
			r := a.Pos.Sub(b.Pos).Add(rel.Scale(t0))
			if tb, ok := BreachWindow(r, rel, e.MinSep, e.SlotDuration); ok {
				earliest[pr] = hit{tBreach: t0 + tb}
			}
		}
	}

	horizon := e.Horizon()
	var out []Breach
	for pr, h := range earliest {
		a, b := ents[pr[0]], ents[pr[1]]
		out = append(out, Breach{
			A:            a.ID,
			B:            b.ID,
			TimeToBreach: h.tBreach,
			MinDistance:  MinApproach(a.Pos.Sub(b.Pos), a.Vel.Sub(b.Vel), horizon),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TimeToBreach != out[j].TimeToBreach {
			return out[i].TimeToBreach < out[j].TimeToBreach
		}
		if out[i].A != out[j].A {
			return out[i].A < out[j].A
		}
		return out[i].B < out[j].B
	})
	return out
}

// Beacons runs Detect and emits a warning beacon for every breach that
// will happen within WarnLead seconds, stamped with the pair's current
// topological distance.
func (e *Engine) Beacons(ents []Entity, now float64) []Beacon {
	byID := make(map[string]int, len(ents))
	for i, ent := range ents {
		byID[ent.ID] = i
	}
	var out []Beacon
	for _, br := range e.Detect(ents) {
		if br.TimeToBreach > e.WarnLead+1e-9 {
			continue
		}
		out = append(out, Beacon{
			Time:            now,
			A:               br.A,
			B:               br.B,
			CurrentDistance: ents[byID[br.A]].Pos.Dist(ents[byID[br.B]].Pos),
			TimeToBreach:    br.TimeToBreach,
			MinDistance:     br.MinDistance,
		})
	}
	return out
}
