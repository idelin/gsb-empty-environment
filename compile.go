package netsec

import (
	"fmt"
	"sort"
)

// Region is one symbolic decision region: every device in the rectangle
// [MajorLo,MajorHi] x [MinorLo,MinorHi] of the given type resolves to Perms.
type Region struct {
	Type    byte
	MajorLo int
	MajorHi int
	MinorLo int
	MinorHi int
	Perms   Perm
}

func (r Region) String() string {
	return fmt.Sprintf("%c %d-%d %d-%d %v", r.Type, r.MajorLo, r.MajorHi, r.MinorLo, r.MinorHi, r.Perms)
}

func (r Region) contains(d Device) bool {
	return r.Type == d.Type &&
		d.Major >= r.MajorLo && d.Major <= r.MajorHi &&
		d.Minor >= r.MinorLo && d.Minor <= r.MinorHi
}

// Diagram is a disjoint symbolic region decision diagram: the regions are
// pairwise disjoint and jointly cover the whole device space, each mapping
// to the permission set granted by the compiled rule sequence.
type Diagram struct {
	Regions []Region
}

// Query returns the permissions the diagram grants for one device.
func (d *Diagram) Query(dev Device) Perm {
	for _, r := range d.Regions {
		if r.contains(dev) {
			return r.Perms
		}
	}
	return 0
}

// matcher is the concrete match set of a rule.
type matcher struct {
	matchChar  bool
	matchBlock bool
	majorLo    int
	majorHi    int
	minorLo    int
	minorHi    int
}

func ruleMatcher(r Rule) matcher {
	m := matcher{
		matchChar:  r.Type == TypeAll || r.Type == TypeChar,
		matchBlock: r.Type == TypeAll || r.Type == TypeBlock,
		majorLo:    0,
		majorHi:    MaxDeviceNumber,
		minorLo:    0,
		minorHi:    MaxDeviceNumber,
	}
	if r.Major != nil {
		m.majorLo, m.majorHi = *r.Major, *r.Major
	}
	if r.Minor != nil {
		m.minorLo, m.minorHi = *r.Minor, *r.Minor
	}
	return m
}

func (m matcher) matchesType(t byte) bool {
	return (t == TypeChar && m.matchChar) || (t == TypeBlock && m.matchBlock)
}

// Compile folds a rule sequence into a disjoint symbolic region diagram.
// Rules apply in order: Allow unions its permissions into every matched
// device, Deny subtracts them; every device starts with empty permissions.
func Compile(rules []Rule) (*Diagram, error) {
	regions := []Region{
		{Type: TypeChar, MajorHi: MaxDeviceNumber, MinorHi: MaxDeviceNumber},
		{Type: TypeBlock, MajorHi: MaxDeviceNumber, MinorHi: MaxDeviceNumber},
	}
	for i, r := range rules {
		if err := r.valid(); err != nil {
			return nil, fmt.Errorf("rule %d: %w", i, err)
		}
		regions = applyRule(regions, r, ruleMatcher(r))
	}
	regions = coalesce(regions)
	sortRegions(regions)
	return &Diagram{Regions: regions}, nil
}

// applyRule splits every region intersecting the rule's match set and applies
// the allow/deny effect to the intersection, keeping regions disjoint.
func applyRule(regions []Region, rule Rule, m matcher) []Region {
	out := make([]Region, 0, len(regions)+1)
	for _, rg := range regions {
		if !m.matchesType(rg.Type) ||
			rg.MajorHi < m.majorLo || rg.MajorLo > m.majorHi ||
			rg.MinorHi < m.minorLo || rg.MinorLo > m.minorHi {
			out = append(out, rg)
			continue
		}
		majLo := max(rg.MajorLo, m.majorLo)
		majHi := min(rg.MajorHi, m.majorHi)
		minLo := max(rg.MinorLo, m.minorLo)
		minHi := min(rg.MinorHi, m.minorHi)
		if rg.MajorLo < majLo {
			out = append(out, Region{rg.Type, rg.MajorLo, majLo - 1, rg.MinorLo, rg.MinorHi, rg.Perms})
		}
		if majHi < rg.MajorHi {
			out = append(out, Region{rg.Type, majHi + 1, rg.MajorHi, rg.MinorLo, rg.MinorHi, rg.Perms})
		}
		if rg.MinorLo < minLo {
			out = append(out, Region{rg.Type, majLo, majHi, rg.MinorLo, minLo - 1, rg.Perms})
		}
		if minHi < rg.MinorHi {
			out = append(out, Region{rg.Type, majLo, majHi, minHi + 1, rg.MinorHi, rg.Perms})
		}
		center := Region{rg.Type, majLo, majHi, minLo, minHi, rg.Perms}
		if rule.Effect == Allow {
			center.Perms |= rule.Perms
		} else {
			center.Perms &^= rule.Perms
		}
		out = append(out, center)
	}
	return out
}

func sortRegions(regions []Region) {
	sort.Slice(regions, func(i, j int) bool {
		a, b := regions[i], regions[j]
		if a.Type != b.Type {
			return typeRank(a.Type) < typeRank(b.Type)
		}
		if a.MajorLo != b.MajorLo {
			return a.MajorLo < b.MajorLo
		}
		return a.MinorLo < b.MinorLo
	})
}

// coalesce merges adjacent regions with identical permissions so the diagram
// stays compact. It never changes the granted permission of any device.
func coalesce(regions []Region) []Region {
	for {
		n := len(regions)
		regions = mergePass(regions, true)
		regions = mergePass(regions, false)
		if len(regions) == n {
			return regions
		}
	}
}

// mergePass merges regions that share the major interval (alongMinor) or the
// minor interval and are contiguous along the other axis.
func mergePass(regions []Region, alongMinor bool) []Region {
	key := func(r Region) [5]int {
		if alongMinor {
			return [5]int{typeRank(r.Type), int(r.Perms), r.MajorLo, r.MajorHi, r.MinorLo}
		}
		return [5]int{typeRank(r.Type), int(r.Perms), r.MinorLo, r.MinorHi, r.MajorLo}
	}
	sort.Slice(regions, func(i, j int) bool {
		a, b := key(regions[i]), key(regions[j])
		for k := range a {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return false
	})
	out := regions[:0]
	for _, r := range regions {
		if len(out) > 0 {
			p := &out[len(out)-1]
			if alongMinor && p.Type == r.Type && p.Perms == r.Perms &&
				p.MajorLo == r.MajorLo && p.MajorHi == r.MajorHi && p.MinorHi+1 == r.MinorLo {
				p.MinorHi = r.MinorHi
				continue
			}
			if !alongMinor && p.Type == r.Type && p.Perms == r.Perms &&
				p.MinorLo == r.MinorLo && p.MinorHi == r.MinorHi && p.MajorHi+1 == r.MajorLo {
				p.MajorHi = r.MajorHi
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

// checkInvariant verifies the diagram is disjoint and covers the whole
// device space, symbolically, without enumerating devices.
func (d *Diagram) checkInvariant() error {
	for _, t := range []byte{TypeChar, TypeBlock} {
		cur := Device{Type: t}
		for _, r := range d.Regions {
			if r.Type != t {
				continue
			}
			if r.MajorLo != cur.Major || r.MinorLo != cur.Minor {
				return fmt.Errorf("type %c: gap or overlap before region %v at %v", t, r, cur)
			}
			cur.Major, cur.Minor = r.MajorHi, r.MinorHi+1
			if cur.Minor > MaxDeviceNumber {
				cur.Major, cur.Minor = cur.Major+1, 0
			}
		}
		if cur.Major != MaxDeviceNumber+1 {
			return fmt.Errorf("type %c: coverage stops at %v", t, cur)
		}
	}
	return nil
}
