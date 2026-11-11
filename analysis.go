package netsec

import (
	"fmt"
	"sort"
)

// AnalysisConfig controls a full audit run.
type AnalysisConfig struct {
	// Entry is the node ID where ingress packets enter the graph.
	Entry int
	// Exit is the node ID whose reachable state must satisfy Egress.
	Exit int
	// Ingress constrains packets injected at Entry.
	Ingress []Constraint
	// Egress constrains packets that must be observable at Exit.
	Egress []Constraint
	// DomainMax caps the narrowed exhaustive domain (per field).
	DomainMax int
	// MaxFixpointIterations is a defensive ceiling for the symbolic loop.
	MaxFixpointIterations int
	// MaxWitnesses bounds how many concrete witnesses are materialized.
	MaxWitnesses int
}

// Mismatch explains a single divergence between symbolic analysis and the
// exhaustive concrete cross-check. A healthy audit report contains none.
type Mismatch struct {
	Node     int
	Kind     string
	Symbolic *Rect
	Concrete *Packet
	Detail   string
}

// AnalysisReport is the complete audit result.
type AnalysisReport struct {
	Config      AnalysisConfig
	Domain      *NarrowedDomain
	Symbolic    map[int][]Rect
	SymbolicOut map[int][]Rect
	Exemplars   map[int]map[Rect][]int
	Counters    map[int]NodeCounters
	LoopEvents  []LoopEvent
	EgressRects []Rect
	Enumeration *EnumerationReport
	Mismatches  []Mismatch
	Witnesses   []*Witness
	Iterations  int
}

// Reachable reports whether any packet satisfies both ingress and egress.
func (r *AnalysisReport) Reachable() bool {
	return len(r.EgressRects) > 0
}

// Analyze runs the complete audit pipeline:
//
//  1. symbolic fixpoint over the full uint32 x uint32 state space, handling
//     cycles by rectangular interval subsumption;
//  2. construction of a delta-closed narrowed domain;
//  3. exhaustive concrete enumeration on that domain;
//  4. set-equality cross-check of the two analyses;
//  5. materialization of real, independently replayable witnesses.
func Analyze(g *Graph, cfg AnalysisConfig) (*AnalysisReport, error) {
	if err := validateConfig(g, cfg); err != nil {
		return nil, err
	}
	engine := newFixpointEngine(g, cfg.MaxFixpointIterations)
	seed := rectFromConstraints(cfg.Ingress)
	if err := engine.run(cfg.Entry, seed); err != nil {
		return nil, err
	}

	domain, err := BuildNarrowedDomain(g, cfg.Ingress, cfg.Egress, DomainConfig{MaxValues: cfg.DomainMax})
	if err != nil {
		return nil, err
	}
	enum, err := Enumerate(g, cfg.Entry, cfg.Ingress, domain)
	if err != nil {
		return nil, err
	}

	report := &AnalysisReport{
		Config:      cfg,
		Domain:      domain,
		Symbolic:    make(map[int][]Rect),
		SymbolicOut: make(map[int][]Rect),
		Exemplars:   make(map[int]map[Rect][]int),
		Counters:    make(map[int]NodeCounters),
		LoopEvents:  append([]LoopEvent{}, engine.loops...),
		Enumeration: enum,
		Iterations:  engine.iterations,
	}
	for _, id := range sortedNodeIDs(g) {
		report.Symbolic[id] = engine.reachableAt(id)
		report.SymbolicOut[id] = engine.outputAt(id)
		report.Exemplars[id] = make(map[Rect][]int)
		for _, r := range report.Symbolic[id] {
			if p := engine.exemplarPath(id, r); p != nil {
				report.Exemplars[id][r] = p
			}
		}
		if c, ok := engine.counters[id]; ok {
			report.Counters[id] = *c
		}
	}

	egressRect := rectFromConstraints(cfg.Egress)
	for _, r := range report.SymbolicOut[cfg.Exit] {
		if hit, ok := r.intersect(egressRect); ok {
			report.EgressRects = append(report.EgressRects, hit)
		}
	}
	report.EgressRects = canonicalize(report.EgressRects)

	report.Mismatches = crossCheck(g, cfg, domain, report.Symbolic, enum)
	if err := report.materializeWitnesses(g, engine); err != nil {
		return nil, err
	}
	return report, nil
}

func validateConfig(g *Graph, cfg AnalysisConfig) error {
	if _, ok := g.Nodes[cfg.Entry]; !ok {
		return errUnknownNode(cfg.Entry)
	}
	if _, ok := g.Nodes[cfg.Exit]; !ok {
		return errUnknownNode(cfg.Exit)
	}
	for _, c := range append(append([]Constraint{}, cfg.Ingress...), cfg.Egress...) {
		if c.Range.Lo > c.Range.Hi {
			return fmt.Errorf("netsec: inverted constraint range on %s", c.Field)
		}
	}
	return nil
}

// crossCheck verifies set equality, restricted to the narrowed domain,
// between symbolic fixpoint state and exhaustive concrete state at every
// node. Both directions are checked so an unsound abstraction and an
// over-approximation are caught.
func crossCheck(g *Graph, cfg AnalysisConfig, domain *NarrowedDomain, symbolic map[int][]Rect, enum *EnumerationReport) []Mismatch {
	var mismatches []Mismatch
	for _, id := range sortedNodeIDs(g) {
		concrete := map[uint64]ConcreteReach{}
		for _, cr := range enum.Reachable[id] {
			concrete[packetKey(cr.Packet)] = cr
		}
		covered := map[uint64]bool{}
		for _, r := range symbolic[id] {
			for _, p := range domain.packetsIn(r) {
				cr, ok := concrete[packetKey(p)]
				if !ok {
					mismatches = append(mismatches, Mismatch{
						Node:     id,
						Kind:     "symbolic-without-concrete",
						Symbolic: &r,
						Detail:   fmt.Sprintf("symbolic region covers domain packet %d->%d but enumeration never reached it", p.Src, p.Dst),
					})
					continue
				}
				_ = cr
				covered[packetKey(p)] = true
			}
		}
		for key, cr := range concrete {
			if !covered[key] {
				p := cr.Packet
				mismatches = append(mismatches, Mismatch{
					Node:     id,
					Kind:     "concrete-without-symbolic",
					Concrete: &p,
					Detail:   fmt.Sprintf("enumeration reached %d->%d but symbolic fixpoint has no covering region", p.Src, p.Dst),
				})
			}
		}
	}
	sort.SliceStable(mismatches, func(i, j int) bool {
		if mismatches[i].Node != mismatches[j].Node {
			return mismatches[i].Node < mismatches[j].Node
		}
		return mismatches[i].Kind < mismatches[j].Kind
	})
	return mismatches
}

// materializeWitnesses turns egress rectangles into concrete packets. Each
// candidate is replayed through the exemplar path and only retained when the
// independent replay reproduces a packet satisfying the egress constraints.
func (r *AnalysisReport) materializeWitnesses(g *Graph, engine *fixpointEngine) error {
	max := r.Config.MaxWitnesses
	if max <= 0 {
		max = 16
	}
	egressRect := rectFromConstraints(r.Config.Egress)
	// Witnesses are built from the exhaustive enumeration's ground truth:
	// each ConcreteReach carries the concrete packet observed at the exit and
	// the exact path it traveled. We recover the original ingress packet by
	// taking the first concrete state on that path, then independently replay
	// it and require the replay to satisfy the egress constraints.
	seen := map[uint64]struct{}{}
	for _, cr := range r.Enumeration.Reachable[r.Config.Exit] {
		if len(r.Witnesses) >= max {
			break
		}
		if !egressRect.containsPacket(cr.Packet) {
			continue
		}
		entryPacket := cr.Origin
		key := packetKey(entryPacket)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		w, err := replayPath(g, r.Config.Entry, entryPacket, cr.Path)
		if err != nil {
			continue
		}
		final := w.Traces[len(w.Traces)-1].Out
		if !egressRect.containsPacket(final) {
			continue
		}
		if _, err := w.Replay(g); err != nil {
			continue
		}
		r.Witnesses = append(r.Witnesses, w)
	}
	return nil
}
