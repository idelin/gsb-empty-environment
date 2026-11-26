package devpolicy

import "sort"

// bounds collects the sorted, de-duplicated interval edges [lo, hi+1) of a
// set of rectangles. Together with 0 and domainMax+1 they define a slab
// decomposition in which no rectangle boundary falls inside a slab.
type bounds []uint16

func collectBounds(rects []Rect, domainMax uint16) bounds {
	// Use int internally so that the exclusive edge domainMax+1 and a
	// rectangle ending at MaxDeviceNo (4095) cannot wrap around in uint16.
	limit := int(domainMax) + 1
	seen := map[int]struct{}{0: {}, limit: {}}
	for _, r := range rects {
		seen[int(r.MajMin)] = struct{}{}
		seen[int(r.MajMax)+1] = struct{}{}
		seen[int(r.MinMin)] = struct{}{}
		seen[int(r.MinMax)+1] = struct{}{}
	}
	out := make(bounds, 0, len(seen))
	for edge := range seen {
		if edge <= limit {
			out = append(out, uint16(edge))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// slab is one half-open coordinate run [lo, hi).
type slab struct {
	lo, hi uint16
}

func (b bounds) slabs() []slab {
	out := make([]slab, 0, len(b)-1)
	for i := 0; i+1 < len(b); i++ {
		if b[i] < b[i+1] {
			out = append(out, slab{lo: b[i], hi: uint16(int(b[i+1]) - 1)})
		}
	}
	return out
}

// gridPartition builds an exact disjoint partition by coordinate compression.
// maskFor is evaluated once per slab cell with the lower-left coordinates;
// slab alignment guarantees the decision is uniform over the whole cell.
// Adjacent equal-mask cells are merged afterwards.
func gridPartition(rects []Rect, domainMax uint16, maskFor func(major, minor uint16) uint8) Partition {
	edges := collectBounds(rects, domainMax)
	slabs := edges.slabs()
	var cells Partition
	for _, maj := range slabs {
		for _, min := range slabs {
			mask := maskFor(maj.lo, min.lo)
			cells = append(cells, Cell{
				Rect: Rect{
					MajMin: maj.lo, MajMax: maj.hi,
					MinMin: min.lo, MinMax: min.hi,
				},
				Mask: mask,
			})
		}
	}
	return compact(cells)
}
