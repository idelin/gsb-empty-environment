package capgate

import (
	"fmt"
	"sort"
)

// masks is a resolved tri-state condition over platform bits and feature
// switches, each stored as (valueMask, careMask).
type masks struct {
	platVal  uint64
	platCare uint64
	flagVal  uint64
	flagCare uint64
}

func (m masks) platOK(pv uint64) bool { return (pv & m.platCare) == m.platVal }
func (m masks) flagOK(fv uint64) bool { return (fv & m.flagCare) == m.flagVal }

// Universe indexes platform bits and feature switches and pre-resolves the
// condition of every edge.
type Universe struct {
	job       *Job
	platIndex map[string]int
	flagIndex map[string]int
	platCount int
	flagCount int
}

func newUniverse(j *Job) (*Universe, error) {
	u := &Universe{
		job:       j,
		platIndex: map[string]int{},
		flagIndex: map[string]int{},
	}
	if len(j.Platforms) > 64 || len(j.Flags) > 64 {
		return nil, fmt.Errorf("exhaustive enumeration limited to 64 platform bits and 64 flags")
	}
	for i, p := range j.Platforms {
		if _, dup := u.platIndex[p]; dup {
			return nil, fmt.Errorf("duplicate platform %q", p)
		}
		u.platIndex[p] = i
	}
	for i, f := range j.Flags {
		if _, dup := u.flagIndex[f]; dup {
			return nil, fmt.Errorf("duplicate feature flag %q", f)
		}
		u.flagIndex[f] = i
	}
	u.platCount = len(j.Platforms)
	u.flagCount = len(j.Flags)
	if err := u.validate(); err != nil {
		return nil, err
	}
	return u, nil
}

func stageIndex(stages []string, s string) int {
	for i, x := range stages {
		if x == s {
			return i
		}
	}
	return -1
}

func (u *Universe) validate() error {
	j := u.job
	if len(j.Stages) == 0 {
		return fmt.Errorf("at least one build stage required")
	}
	if stageIndex(j.Stages, j.SensitiveStage) < 0 {
		return fmt.Errorf("sensitive stage %q not present in stages", j.SensitiveStage)
	}
	if err := u.validateGraph("old", &j.Old); err != nil {
		return err
	}
	if err := u.validateGraph("new", &j.New); err != nil {
		return err
	}
	return nil
}

func (u *Universe) validateGraph(tag string, g *Graph) error {
	nodeIDs := map[string]int{}
	names := map[string]int{}
	for i, n := range g.Nodes {
		if n.ID == "" {
			return fmt.Errorf("%s graph: node %d has empty id", tag, i)
		}
		if _, dup := nodeIDs[n.ID]; dup {
			return fmt.Errorf("%s graph: duplicate node id %q", tag, n.ID)
		}
		if n.Digest == "" {
			return fmt.Errorf("%s graph: node %q has empty digest (credentials must bind to a digest)", tag, n.ID)
		}
		nodeIDs[n.ID] = i
		names[n.Name]++
	}
	stageSet := map[string]bool{}
	for _, s := range u.job.Stages {
		stageSet[s] = true
	}
	for i, e := range g.Edges {
		if _, ok := nodeIDs[e.From]; !ok {
			return fmt.Errorf("%s graph: edge %d references unknown from-node %q", tag, i, e.From)
		}
		if _, ok := nodeIDs[e.To]; !ok {
			return fmt.Errorf("%s graph: edge %d references unknown to-node %q", tag, i, e.To)
		}
		if !stageSet[e.Stage] {
			return fmt.Errorf("%s graph: edge %d uses unknown stage %q", tag, i, e.Stage)
		}
		if e.Optional && e.GateFlag == "" {
			return fmt.Errorf("%s graph: optional edge %d (%s->%s) must declare a gate flag", tag, i, e.From, e.To)
		}
		if e.GateFlag != "" {
			if _, ok := u.flagIndex[e.GateFlag]; !ok {
				return fmt.Errorf("%s graph: edge %d gates on unknown flag %q", tag, i, e.GateFlag)
			}
		}
		for _, sel := range e.Platform {
			if sel.Flag == "" {
				if _, ok := u.platIndex[sel.Platform]; !ok {
					return fmt.Errorf("%s graph: edge %d references unknown platform %q", tag, i, sel.Platform)
				}
			} else {
				if _, ok := u.flagIndex[sel.Flag]; !ok {
					return fmt.Errorf("%s graph: edge %d references unknown flag %q", tag, i, sel.Flag)
				}
			}
		}
		if e.Half != nil {
			h := e.Half
			if h.TxnID == "" || h.Digest == "" {
				return fmt.Errorf("%s graph: edge %d half needs txn id and digest", tag, i)
			}
			if h.HalfIndex != 0 && h.HalfIndex != 1 {
				return fmt.Errorf("%s graph: edge %d half_index must be 0 or 1", tag, i)
			}
			if !stageSet[h.Stage] {
				return fmt.Errorf("%s graph: edge %d half uses unknown stage %q", tag, i, h.Stage)
			}
			node, ok := g.nodeByID(e.To)
			if ok && h.Digest != node.Digest {
				return fmt.Errorf("%s graph: edge %d half digest %q does not bind to receiver %q digest %q (dependency downgrade / aliasing forbidden)",
					tag, i, h.Digest, e.To, node.Digest)
			}
		}
	}
	for i, lk := range g.Leaks {
		if !stageSet[lk.FromStage] || !stageSet[lk.ToStage] {
			return fmt.Errorf("%s graph: leak %d references unknown stage", tag, i)
		}
		if stageIndex(u.job.Stages, lk.ToStage) <= stageIndex(u.job.Stages, lk.FromStage) {
			return fmt.Errorf("%s graph: leak %d must target a strictly later stage", tag, i)
		}
		if _, ok := nodeIDs[lk.Node]; !ok {
			return fmt.Errorf("%s graph: leak %d references unknown node %q", tag, i, lk.Node)
		}
	}
	_ = names
	return nil
}

func (g *Graph) nodeByID(id string) (Node, bool) {
	for _, n := range g.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

// resolve converts a selector list into a bitmask, rejecting contradictory
// selectors (the condition is then unsatisfiable, never silently permissive).
func (u *Universe) resolve(sels []Selector) (masks, bool) {
	var m masks
	for _, s := range sels {
		if s.Flag == "" {
			bit := uint64(1) << u.platIndex[s.Platform]
			if m.platCare&bit != 0 {
				want := m.platVal&bit != 0
				if want != s.On {
					return m, false
				}
				continue
			}
			m.platCare |= bit
			if s.On {
				m.platVal |= bit
			}
		} else {
			bit := uint64(1) << u.flagIndex[s.Flag]
			if m.flagCare&bit != 0 {
				want := m.flagVal&bit != 0
				if want != s.On {
					return m, false
				}
				continue
			}
			m.flagCare |= bit
			if s.On {
				m.flagVal |= bit
			}
		}
	}
	return m, true
}

func (u *Universe) edgeMask(e *Edge) (masks, bool) {
	m, ok := u.resolve(e.Platform)
	if !ok {
		return m, false
	}
	if e.GateFlag != "" {
		bit := uint64(1) << u.flagIndex[e.GateFlag]
		if m.flagCare&bit != 0 {
			want := m.flagVal&bit != 0
			if want != e.GateOn {
				return m, false
			}
		} else {
			m.flagCare |= bit
			if e.GateOn {
				m.flagVal |= bit
			}
		}
	}
	return m, true
}

// enumerate yields every legal platform/flag assignment as (pv, fv, Combo).
func (u *Universe) enumerate() []struct {
	pv    uint64
	fv    uint64
	combo Combo
} {
	platN := 1 << u.platCount
	flagN := 1 << u.flagCount
	out := make([]struct {
		pv    uint64
		fv    uint64
		combo Combo
	}, 0, platN*flagN)
	for pv := 0; pv < platN; pv++ {
		if !u.job.AllowMultiPlatform && !singleBit(uint64(pv), u.platCount) {
			continue
		}
		for fv := 0; fv < flagN; fv++ {
			out = append(out, struct {
				pv    uint64
				fv    uint64
				combo Combo
			}{uint64(pv), uint64(fv), u.combo(uint64(pv), uint64(fv))})
		}
	}
	return out
}

// singleBit reports whether pv has exactly one platform bit set, i.e. the
// combo selects exactly one mutually-exclusive target platform.
func singleBit(pv uint64, bits int) bool {
	if bits == 0 {
		return pv == 0
	}
	return pv != 0 && pv&(pv-1) == 0 && pv < (uint64(1)<<bits)
}

func bitOn(v uint64, i int) bool { return v&(uint64(1)<<i) != 0 }

func (u *Universe) combo(pv, fv uint64) Combo {
	c := Combo{
		PlatformValues: map[string]bool{},
		FlagValues:     map[string]bool{},
	}
	for _, p := range u.job.Platforms {
		c.PlatformValues[p] = bitOn(pv, u.platIndex[p])
	}
	for _, f := range u.job.Flags {
		c.FlagValues[f] = bitOn(fv, u.flagIndex[f])
	}
	return c
}

func (u *Universe) comboID(pv, fv uint64) string {
	return fmt.Sprintf("p%0*b|f%0*b", (u.platCount+3)/4*4, pv, (u.flagCount+3)/4*4, fv)
}

// cubeFromMasks builds a reporting Cube from resolved masks.
func (u *Universe) cubeFromMasks(m masks) Cube {
	c := Cube{Platforms: map[string]*bool{}, Flags: map[string]*bool{}}
	for _, p := range u.job.Platforms {
		bit := uint64(1) << u.platIndex[p]
		if m.platCare&bit != 0 {
			on := m.platVal&bit != 0
			c.Platforms[p] = &on
		}
	}
	for _, f := range u.job.Flags {
		bit := uint64(1) << u.flagIndex[f]
		if m.flagCare&bit != 0 {
			on := m.flagVal&bit != 0
			c.Flags[f] = &on
		}
	}
	return c
}

func sortedPlatforms(m map[string]*bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
