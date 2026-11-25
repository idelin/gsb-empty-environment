package devrule

import "fmt"

// Region is one cell of the decision diagram: an axis-aligned rectangle over
// (major, minor) for a single device type, mapped to a permission set.
// Regions of a Diagram are pairwise disjoint and cover the whole device
// space, so every device belongs to exactly one region.
type Region struct {
	Kind  Kind
	MajLo int
	MajHi int
	MinLo int
	MinHi int
	Perms Perm
}

func (r Region) contains(major, minor int) bool {
	return major >= r.MajLo && major <= r.MajHi && minor >= r.MinLo && minor <= r.MinHi
}

func (r Region) String() string {
	return fmt.Sprintf("%s %d-%d:%d-%d %s", r.Kind, r.MajLo, r.MajHi, r.MinLo, r.MinHi, r.Perms)
}

// Diagram is the compiled form of a rule sequence: a disjoint symbolic
// region decision diagram over the device space.
type Diagram struct {
	Regions []Region
}

// Compile folds an ordered rule sequence into a Diagram. Every device starts
// with the empty permission set; each Allow unions its permissions into
// matching devices, each Deny subtracts them.
func Compile(rules []Rule) (*Diagram, error) {
	d := &Diagram{Regions: []Region{
		{Kind: Char, MajHi: MaxDeviceNumber, MinHi: MaxDeviceNumber},
		{Kind: Block, MajHi: MaxDeviceNumber, MinHi: MaxDeviceNumber},
	}}
	for i, r := range rules {
		if err := r.Validate(); err != nil {
			return nil, fmt.Errorf("rule %d (%s): %w", i, r, err)
		}
		d.apply(r)
	}
	return d, nil
}

// Lookup returns the permission set the diagram grants to one device.
func (d *Diagram) Lookup(kind Kind, major, minor int) Perm {
	for _, r := range d.Regions {
		if r.Kind == kind && r.contains(major, minor) {
			return r.Perms
		}
	}
	return 0
}

// rects expands a rule into one rectangle per matched device type.
func (r Rule) rects() []Region {
	majLo, majHi := 0, MaxDeviceNumber
	if r.Major != Wildcard {
		majLo, majHi = r.Major, r.Major
	}
	minLo, minHi := 0, MaxDeviceNumber
	if r.Minor != Wildcard {
		minLo, minHi = r.Minor, r.Minor
	}
	var out []Region
	for _, k := range r.Type.kinds() {
		out = append(out, Region{Kind: k, MajLo: majLo, MajHi: majHi, MinLo: minLo, MinHi: minHi})
	}
	return out
}

func (d *Diagram) apply(rule Rule) {
	matches := rule.rects()
	next := make([]Region, 0, len(d.Regions))
	for _, reg := range d.Regions {
		applied := false
		for _, m := range matches {
			ov, ok := overlap(reg, m)
			if !ok {
				continue
			}
			next = append(next, splitRegion(reg, ov, rule)...)
			applied = true
			break
		}
		if !applied {
			next = append(next, reg)
		}
	}
	d.Regions = next
}

// overlap returns the intersection of two same-kind rectangles.
func overlap(a, b Region) (Region, bool) {
	if a.Kind != b.Kind {
		return Region{}, false
	}
	r := Region{
		Kind:  a.Kind,
		MajLo: max(a.MajLo, b.MajLo),
		MajHi: min(a.MajHi, b.MajHi),
		MinLo: max(a.MinLo, b.MinLo),
		MinHi: min(a.MinHi, b.MinHi),
	}
	if r.MajLo > r.MajHi || r.MinLo > r.MinHi {
		return Region{}, false
	}
	return r, true
}

// splitRegion cuts reg into disjoint sub-rectangles along the overlap
// boundaries and applies the rule's effect to the overlapping cell.
func splitRegion(reg, ov Region, rule Rule) []Region {
	majSegs := [][2]int{
		{reg.MajLo, ov.MajLo - 1},
		{ov.MajLo, ov.MajHi},
		{ov.MajHi + 1, reg.MajHi},
	}
	minSegs := [][2]int{
		{reg.MinLo, ov.MinLo - 1},
		{ov.MinLo, ov.MinHi},
		{ov.MinHi + 1, reg.MinHi},
	}
	var out []Region
	for _, ms := range majSegs {
		if ms[0] > ms[1] {
			continue
		}
		for _, ns := range minSegs {
			if ns[0] > ns[1] {
				continue
			}
			cell := Region{
				Kind:  reg.Kind,
				MajLo: ms[0], MajHi: ms[1],
				MinLo: ns[0], MinHi: ns[1],
				Perms: reg.Perms,
			}
			if ms[0] == ov.MajLo && ms[1] == ov.MajHi &&
				ns[0] == ov.MinLo && ns[1] == ov.MinHi {
				if rule.Effect == Allow {
					cell.Perms |= rule.Perms
				} else {
					cell.Perms &^= rule.Perms
				}
			}
			out = append(out, cell)
		}
	}
	return out
}

// subtractRegion returns r \ s as up to four disjoint rectangles. Both must
// share the same device type.
func subtractRegion(r, s Region) []Region {
	ov, ok := overlap(r, s)
	if !ok {
		return []Region{r}
	}
	var out []Region
	if r.MajLo < ov.MajLo {
		out = append(out, Region{Kind: r.Kind, MajLo: r.MajLo, MajHi: ov.MajLo - 1,
			MinLo: r.MinLo, MinHi: r.MinHi, Perms: r.Perms})
	}
	if ov.MajHi < r.MajHi {
		out = append(out, Region{Kind: r.Kind, MajLo: ov.MajHi + 1, MajHi: r.MajHi,
			MinLo: r.MinLo, MinHi: r.MinHi, Perms: r.Perms})
	}
	if r.MinLo < ov.MinLo {
		out = append(out, Region{Kind: r.Kind, MajLo: ov.MajLo, MajHi: ov.MajHi,
			MinLo: r.MinLo, MinHi: ov.MinLo - 1, Perms: r.Perms})
	}
	if ov.MinHi < r.MinHi {
		out = append(out, Region{Kind: r.Kind, MajLo: ov.MajLo, MajHi: ov.MajHi,
			MinLo: ov.MinHi + 1, MinHi: r.MinHi, Perms: r.Perms})
	}
	return out
}

// subtractFromAll subtracts every rectangle in subs from each rectangle in
// rs, returning the remaining (possibly empty) rectangle set.
func subtractFromAll(rs []Region, subs ...Region) []Region {
	out := rs
	for _, s := range subs {
		var next []Region
		for _, r := range out {
			if r.Kind != s.Kind {
				next = append(next, r)
				continue
			}
			next = append(next, subtractRegion(r, s)...)
		}
		out = next
		if len(out) == 0 {
			break
		}
	}
	return out
}

// regionsWithPerm returns the diagram's regions of the given type whose
// permission set includes p.
func (d *Diagram) regionsWithPerm(k Kind, p Perm) []Region {
	var out []Region
	for _, r := range d.Regions {
		if r.Kind == k && r.Perms&p != 0 {
			out = append(out, r)
		}
	}
	return out
}

// minDevice returns the smallest (major, minor) corner over the rectangles.
func minDevice(rs []Region) (major, minor int) {
	major, minor = rs[0].MajLo, rs[0].MinLo
	for _, r := range rs[1:] {
		if r.MajLo < major || (r.MajLo == major && r.MinLo < minor) {
			major, minor = r.MajLo, r.MinLo
		}
	}
	return major, minor
}
