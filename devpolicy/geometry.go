package devpolicy

// Rect is an axis-aligned, closed rectangle in the (major, minor) number
// plane. All four bounds are inclusive.
type Rect struct {
	MajMin, MajMax uint16
	MinMin, MinMax uint16
}

// FullRect is the wildcard rectangle covering every legal (major, minor).
func FullRect() Rect {
	return Rect{0, MaxDeviceNo, 0, MaxDeviceNo}
}

// empty reports whether the rectangle contains no point.
func (r Rect) empty() bool {
	return r.MajMin > r.MajMax || r.MinMin > r.MinMax
}

// intersect returns the overlap of r and s and true, or a zero rectangle and
// false when they are disjoint. The boolean is required because the single
// point (0,0) is itself a valid rectangle and must not be confused with
// "no intersection".
func (r Rect) intersect(s Rect) (Rect, bool) {
	out := Rect{
		MajMin: maxU16(r.MajMin, s.MajMin),
		MajMax: minU16(r.MajMax, s.MajMax),
		MinMin: maxU16(r.MinMin, s.MinMin),
		MinMax: minU16(r.MinMax, s.MinMax),
	}
	if out.empty() {
		return Rect{}, false
	}
	return out, true
}

// subtract partitions r into disjoint rectangles covering exactly the points
// of r that are not inside s. It never enumerates device numbers.
func (r Rect) subtract(s Rect) []Rect {
	if _, ok := r.intersect(s); !ok {
		return []Rect{r}
	}
	var out []Rect

	// Left and right strips keep r's minor span.
	if r.MajMin < s.MajMin {
		out = append(out, Rect{r.MajMin, s.MajMin - 1, r.MinMin, r.MinMax})
	}
	if s.MajMax < r.MajMax {
		out = append(out, Rect{s.MajMax + 1, r.MajMax, r.MinMin, r.MinMax})
	}
	majLo := maxU16(r.MajMin, s.MajMin)
	majHi := minU16(r.MajMax, s.MajMax)

	// Middle column: lower and upper minor strips.
	if r.MinMin < s.MinMin {
		out = append(out, Rect{majLo, majHi, r.MinMin, s.MinMin - 1})
	}
	if s.MinMax < r.MinMax {
		out = append(out, Rect{majLo, majHi, s.MinMax + 1, r.MinMax})
	}
	return out
}

// Cell is one rectangle of a disjoint plane partition together with the
// decision mask that is uniform over that whole rectangle.
type Cell struct {
	Rect
	// Mask holds the effective permission bits (a subset of MaskAll).
	Mask uint8
}

// Partition is an exact disjoint cover of the full [0,MaxDeviceNo]^2 plane.
// Cells never overlap and their union is always the whole plane.
type Partition []Cell

// fullPartition returns the single-cell plane with the given mask.
func fullPartition(mask uint8) Partition {
	return Partition{{Rect: FullRect(), Mask: mask}}
}

// applyRule folds one rule into the partition, splitting only cells that
// partially intersect the rule rectangle. Cells fully inside the rectangle
// keep their shape and only their mask changes.
func (p Partition) applyRule(area Rect, effect RuleEffect, perm uint8) Partition {
	out := make(Partition, 0, len(p)+4)
	for _, cell := range p {
		hit, ok := cell.Rect.intersect(area)
		if !ok {
			out = append(out, cell) // untouched
			continue
		}
		if hit == cell.Rect {
			out = append(out, Cell{Rect: cell.Rect, Mask: foldMask(cell.Mask, effect, perm)})
			continue
		}
		// Partial hit: the intersection takes the new decision, the remainder
		// keeps the old one. Subtract yields disjoint rectangles.
		out = append(out, Cell{Rect: hit, Mask: foldMask(cell.Mask, effect, perm)})
		for _, rem := range cell.Rect.subtract(area) {
			out = append(out, Cell{Rect: rem, Mask: cell.Mask})
		}
	}
	return out
}

// foldMask combines an incoming rule's permission bits with the running mask.
// Allow adds bits, Deny strips them; bits are independent so m never implies
// r/w and r/w never implies m.
func foldMask(mask uint8, effect RuleEffect, perm uint8) uint8 {
	if effect == EffectAllow {
		return (mask | perm) & MaskAll
	}
	return mask &^ perm
}

// combineDomain is combine with an explicit domain bound for small-domain
// exhaustive tests.
func combineDomain(a, b Partition, domainMax uint16, fn func(x, y uint8) uint8) Partition {
	// Coordinate-compressed overlay. The union of both partitions' rectangle
	// edges defines a slab grid on which each partition's mask is constant; the
	// combined mask is therefore also constant per slab cell. This avoids any
	// recursive splitting and keeps the cell count quadratic in rule count rather
	// than in the device-number universe size.
	rects := make([]Rect, 0, len(a)+len(b))
	for _, cell := range a {
		rects = append(rects, cell.Rect)
	}
	for _, cell := range b {
		rects = append(rects, cell.Rect)
	}
	return gridPartition(rects, domainMax, func(major, minor uint16) uint8 {
		return fn(maskInPartition(a, major, minor), maskInPartition(b, major, minor))
	})
}

// maskInPartition returns the mask of the unique partition cell containing a
// point. Partitions are exact covers, so exactly one cell matches.
func maskInPartition(p Partition, major, minor uint16) uint8 {
	for _, cell := range p {
		if major >= cell.MajMin && major <= cell.MajMax &&
			minor >= cell.MinMin && minor <= cell.MinMax {
			return cell.Mask
		}
	}
	return 0
}

// intersectPartitionsDomain is the domain-aware layer intersection used by
// the compiler and small-domain tests.
func intersectPartitionsDomain(a, b Partition, domainMax uint16) Partition {
	return combineDomain(a, b, domainMax, func(x, y uint8) uint8 { return x & y })
}

// compact merges geometrically adjacent cells that carry identical masks,
// shrinking the diagram without changing any point's decision.
func compact(in Partition) Partition {
	work := append(Partition(nil), in...)
	// Fixed point: each successful merge strictly shrinks the cell count, so at
	// most len(work) passes can happen. A single left-to-right pass per round is
	// enough because mergeRect only joins edge-adjacent rectangles.
	for {
		changed := false
		next := make(Partition, 0, len(work))
		for _, cell := range work {
			if len(next) > 0 {
				last := &next[len(next)-1]
				if last.Mask == cell.Mask {
					if joined, ok := mergeRect(last.Rect, cell.Rect); ok {
						last.Rect = joined
						changed = true
						continue
					}
				}
			}
			next = append(next, cell)
		}
		work = next
		if !changed {
			return work
		}
	}
}

// mergeRect reports whether two rectangles with equal masks form a single
// rectangle, and returns it. Only the two simple joins are needed by the
// partition builder: shared full minor span with touching major bounds, or
// shared full major span with touching minor bounds.
func mergeRect(a, b Rect) (Rect, bool) {
	if a.MinMin == b.MinMin && a.MinMax == b.MinMax {
		if a.MajMax < MaxDeviceNo && a.MajMax+1 == b.MajMin {
			return Rect{a.MajMin, b.MajMax, a.MinMin, a.MinMax}, true
		}
		if b.MajMax < MaxDeviceNo && b.MajMax+1 == a.MajMin {
			return Rect{b.MajMin, a.MajMax, a.MinMin, a.MinMax}, true
		}
	}
	if a.MajMin == b.MajMin && a.MajMax == b.MajMax {
		if a.MinMax < MaxDeviceNo && a.MinMax+1 == b.MinMin {
			return Rect{a.MajMin, a.MajMax, a.MinMin, b.MinMax}, true
		}
		if b.MinMax < MaxDeviceNo && b.MinMax+1 == a.MinMin {
			return Rect{a.MajMin, a.MajMax, b.MinMin, a.MinMax}, true
		}
	}
	return Rect{}, false
}

func minU16(a, b uint16) uint16 {
	if a < b {
		return a
	}
	return b
}

func maxU16(a, b uint16) uint16 {
	if a > b {
		return a
	}
	return b
}
