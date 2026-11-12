package taskpkg

import (
	"sort"
	"strconv"
)

// packItem uses "granules" rather than pixels. One granule is one side of the
// coarsest mip pixel. Since every slot is rounded to that alignment, all
// packing dimensions are integral in this grid.
type packItem struct {
	index int
	id    int

	width  int64
	height int64
	validW int64
	validH int64
	expW   int64
	expH   int64
}

type packPos struct {
	x       int64
	y       int64
	rotated bool
}

type packRect struct {
	x int64
	y int64
	w int64
	h int64
}

type atlasCandidate struct {
	width    int64
	height   int64
	area     int64
	longSide int64
}

// atlasCandidates returns power-of-two shapes worth considering. A single row
// or column always fits in an upper x upper atlas, so candidates larger than
// that area cannot be optimal; the area limit also admits very elongated
// atlases.
func atlasCandidates(items []packItem, sumW, sumH int64) []atlasCandidate {
	upper := nextPow2(sumW)
	if p := nextPow2(sumH); p > upper {
		upper = p
	}
	if upper < 1 {
		upper = 1
	}
	// A single row always fits in upper x upper, so no optimum can have
	// larger area. Enumerating by area also covers very elongated atlases whose
	// longer side exceeds upper.
	limitArea := int64(1) << 62
	if upper <= int64(1)<<31 {
		limitArea = upper * upper
	}
	powers := powersUpTo(limitArea)

	totalArea := int64(0)
	for _, item := range items {
		totalArea += item.expW * item.expH
	}

	var out []atlasCandidate
	for _, w := range powers {
		for _, h := range powers {
			if h > limitArea/w {
				continue
			}
			area := w * h
			if area < totalArea {
				continue
			}
			if !canFitOneByOne(items, w, h) {
				continue
			}
			out = append(out, atlasCandidate{
				width:    w,
				height:   h,
				area:     area,
				longSide: maxInt64(w, h),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].area != out[j].area {
			return out[i].area < out[j].area
		}
		if out[i].longSide != out[j].longSide {
			return out[i].longSide < out[j].longSide
		}
		if out[i].width != out[j].width {
			return out[i].width < out[j].width
		}
		return out[i].height < out[j].height
	})
	return out
}

func powersUpTo(limit int64) []int64 {
	if limit < 1 {
		return nil
	}
	out := make([]int64, 0, 63)
	for p := int64(1); p > 0 && p <= limit; p <<= 1 {
		out = append(out, p)
		if p > limit/2 {
			break
		}
	}
	return out
}

func nextPow2(v int64) int64 {
	if v <= 1 {
		return 1
	}
	if v > int64(1)<<62 {
		return 0
	}
	p := int64(1)
	for p < v {
		p <<= 1
	}
	return p
}

func canFitOneByOne(items []packItem, width, height int64) bool {
	for _, item := range items {
		if (item.expW <= width && item.expH <= height) ||
			(item.expH <= width && item.expW <= height) {
			continue
		}
		return false
	}
	return true
}

// packAny returns one complete packing, if one exists.
func packAny(items []packItem, width, height int64) ([]packPos, bool) {
	if !necessaryFit(items, width, height) {
		return nil, false
	}
	if pack, ok := quickPack(items, width, height); ok {
		return pack, true
	}
	return packFeasible(items, width, height)
}

// packFeasible is a complete exact-cover style search. It chooses the
// remaining item with the fewest currently feasible placements (MRV) and
// tries each placement. Unused cells are simply left empty, so holes are
// represented without an exponential skip-cell branch.
func packFeasible(items []packItem, width, height int64) ([]packPos, bool) {
	if !necessaryFit(items, width, height) {
		return nil, false
	}
	s := newSearch(items, width, height)
	if !s.feasibleSolve() {
		return nil, false
	}
	if !s.bestFound {
		return nil, false
	}
	return append([]packPos(nil), s.best...), true
}

func (s *search) feasibleSolve() bool {
	if s.remaining == 0 {
		s.best = append(s.best[:0], s.pack...)
		s.bestFound = true
		return true
	}
	if s.areaMask[s.remaining] > s.width*s.height-s.usedArea {
		return false
	}
	stateKey := s.stateKey()
	if _, ok := s.failed[stateKey]; ok {
		return false
	}

	chosen := -1
	bestCount := int64(-1)
	for _, idx := range s.idOrder {
		if s.remaining&(1<<uint(idx)) == 0 {
			continue
		}
		count := int64(0)
		item := s.items[idx]
		for rotation := 0; rotation < 2; rotation++ {
			w, h := item.expW, item.expH
			if rotation == 1 {
				if w == h {
					continue
				}
				w, h = h, w
			}
			for y := int64(0); y+h <= s.height; y++ {
				for x := int64(0); x+w <= s.width; x++ {
					if s.canPlace(x, y, w, h) {
						count++
					}
				}
			}
		}
		if count == 0 {
			s.failed[stateKey] = struct{}{}
			return false
		}
		if bestCount < 0 || count < bestCount {
			chosen, bestCount = idx, count
		}
	}

	item := s.items[chosen]
	for rotation := 0; rotation < 2; rotation++ {
		w, h := item.expW, item.expH
		if rotation == 1 {
			if w == h {
				continue
			}
			w, h = h, w
		}
		for y := int64(0); y+h <= s.height; y++ {
			for x := int64(0); x+w <= s.width; x++ {
				if !s.canPlace(x, y, w, h) {
					continue
				}
				s.pack[chosen] = packPos{x: x, y: y, rotated: rotation == 1}
				s.placed[chosen] = true
				s.occ = append(s.occ, packRect{x: x, y: y, w: w, h: h})
				s.usedArea += w * h
				s.remaining &^= uint16(1 << uint(chosen))
				if s.feasibleSolve() {
					return true
				}
				s.remaining |= uint16(1 << uint(chosen))
				s.usedArea -= w * h
				s.occ = s.occ[:len(s.occ)-1]
				s.placed[chosen] = false
				s.pack[chosen] = packPos{}
			}
		}
	}
	s.failed[stateKey] = struct{}{}
	return false
}

// quickPack is a deterministic bottom-left heuristic. It is only an
// accelerator: a successful result is a valid packing, while a failure falls
// through to the complete search.
func quickPack(items []packItem, width, height int64) ([]packPos, bool) {
	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := items[order[i]], items[order[j]]
		areaA, areaB := a.expW*a.expH, b.expW*b.expH
		if areaA != areaB {
			return areaA > areaB
		}
		if a.id != b.id {
			return a.id < b.id
		}
		return a.index < b.index
	})
	return quickPackOrder(items, width, height, order)
}

func quickPackByID(items []packItem, width, height int64) ([]packPos, bool) {
	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := items[order[i]], items[order[j]]
		if a.id != b.id {
			return a.id < b.id
		}
		return a.index < b.index
	})
	return quickPackOrder(items, width, height, order)
}

func quickPackOrder(items []packItem, width, height int64, order []int) ([]packPos, bool) {
	s := newSearch(items, width, height)
	out := make([]packPos, len(items))
	for _, idx := range order {
		item := items[idx]
		var best quickOption
		found := false
		for _, rotated := range []bool{false, true} {
			w, h := item.expW, item.expH
			if rotated {
				if w == h {
					continue
				}
				w, h = h, w
			}
			for _, x := range quickCandidates(s.occ, width, true) {
				for _, y := range quickCandidates(s.occ, height, false) {
					if x+w > width || y+h > height {
						continue
					}
					clear := true
					for _, r := range s.occ {
						if x < r.x+r.w && r.x < x+w && y < r.y+r.h && r.y < y+h {
							clear = false
							break
						}
					}
					if !clear {
						continue
					}
					opt := quickOption{pos: packPos{x: x, y: y, rotated: rotated}, w: w, h: h}
					if !found || quickOptionLess(opt, best) {
						best = opt
						found = true
					}
				}
			}
		}
		if !found {
			return nil, false
		}
		out[idx] = best.pos
		s.occ = append(s.occ, packRect{x: best.pos.x, y: best.pos.y, w: best.w, h: best.h})
	}
	return out, true
}

func quickCandidates(occ []packRect, limit int64, xAxis bool) []int64 {
	out := []int64{0}
	for _, r := range occ {
		v := r.y + r.h
		if xAxis {
			v = r.x + r.w
		}
		if v <= limit {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	unique := out[:0]
	for _, v := range out {
		if len(unique) == 0 || unique[len(unique)-1] != v {
			unique = append(unique, v)
		}
	}
	return unique
}

type quickOption struct {
	pos packPos
	w   int64
	h   int64
}

func quickOptionLess(a, b quickOption) bool {
	// Prefer bottom-left; on a position tie prefer the less tall orientation.
	if a.pos.y != b.pos.y {
		return a.pos.y < b.pos.y
	}
	if a.pos.x != b.pos.x {
		return a.pos.x < b.pos.x
	}
	if a.h != b.h {
		return a.h < b.h
	}
	if a.w != b.w {
		return a.w < b.w
	}
	return !a.pos.rotated && b.pos.rotated
}

// necessaryFit applies cheap but strong projection bounds before the
// exponential search. For each item it uses the smallest width/height among
// orientations that fit the bin. If two such minimum widths do not fit side by
// side, the items must be separated vertically; a clique of such conflicts
// therefore has to fit its minimum heights into the bin. The transposed bound
// is checked too.
func necessaryFit(items []packItem, width, height int64) bool {
	n := len(items)
	minW := make([]int64, n)
	minH := make([]int64, n)
	for i, item := range items {
		has := false
		if item.expW <= width && item.expH <= height {
			minW[i], minH[i] = item.expW, item.expH
			has = true
		}
		if item.expW != item.expH && item.expH <= width && item.expW <= height {
			iw, ih := item.expH, item.expW
			if !has || iw < minW[i] {
				minW[i] = iw
			}
			if !has || ih < minH[i] {
				minH[i] = ih
			}
			has = true
		}
		if !has {
			return false
		}
	}

	// Horizontally conflicting pairs cannot share a row, so their heights
	// must be stacked. Check every clique of that conflict graph.
	hConflict := make([]uint16, n)
	vConflict := make([]uint16, n)
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if minW[i]+minW[j] > width {
				hConflict[i] |= 1 << uint(j)
				hConflict[j] |= 1 << uint(i)
			}
			if minH[i]+minH[j] > height {
				vConflict[i] |= 1 << uint(j)
				vConflict[j] |= 1 << uint(i)
			}
		}
	}
	if cliqueWeightExceeds(hConflict, minH, height, n) || cliqueWeightExceeds(vConflict, minW, width, n) {
		return false
	}

	// Items whose minimum height is at least t can stack at most floor(H/t)
	// deep at any x. Their minimum widths therefore must fit in that many
	// horizontal layers. The transposed bound is analogous.
	for _, threshold := range uniqueValues(minH) {
		layers := height / threshold
		var sum int64
		for i := 0; i < n; i++ {
			if minH[i] >= threshold {
				sum += minW[i]
			}
		}
		if sum > layers*width {
			return false
		}
	}
	for _, threshold := range uniqueValues(minW) {
		layers := width / threshold
		var sum int64
		for i := 0; i < n; i++ {
			if minW[i] >= threshold {
				sum += minH[i]
			}
		}
		if sum > layers*height {
			return false
		}
	}

	// If every pair among a set conflicts vertically, their widths must fit
	// side by side. This is the same clique test in the transposed graph.
	return true
}

func uniqueValues(values []int64) []int64 {
	out := make([]int64, 0, len(values))
	for _, v := range values {
		if v <= 0 {
			continue
		}
		found := false
		for _, existing := range out {
			if existing == v {
				found = true
				break
			}
		}
		if !found {
			out = append(out, v)
		}
	}
	return out
}

func cliqueWeightExceeds(adj []uint16, weights []int64, limit int64, n int) bool {
	for mask := uint16(1); mask < uint16(1<<uint(n)); mask++ {
		var sum int64
		isClique := true
		for i := 0; i < n; i++ {
			if mask&(1<<uint(i)) == 0 {
				continue
			}
			sum += weights[i]
			for j := i + 1; j < n; j++ {
				if mask&(1<<uint(j)) == 0 {
					continue
				}
				if adj[i]&(1<<uint(j)) == 0 {
					isClique = false
					break
				}
			}
			if !isClique {
				break
			}
		}
		if isClique && sum > limit {
			return true
		}
	}
	return false
}

// packBest finds the lexicographically smallest position vector for a fixed
// atlas shape. seed is a known complete packing used to initialize the
// incumbent.
func packBest(items []packItem, width, height int64, seed []packPos) ([]packPos, bool) {
	best := append([]packPos(nil), seed...)
	for _, candidateFn := range []func([]packItem, int64, int64) ([]packPos, bool){quickPackByID} {
		candidate, ok := candidateFn(items, width, height)
		if ok && packVectorLess(items, candidate, best) {
			best = candidate
		}
	}
	if len(best) == 0 {
		return nil, false
	}
	s := newSearch(items, width, height)
	s.best = append([]packPos(nil), best...)
	s.bestFound = true
	// The lexicographic tie search is exact for small sets. A node budget
	// keeps large sets from exploding; if it is reached, the already valid
	// canonical quick packing remains the deterministic fallback.
	if len(items) > 8 {
		s.nodeLimit = 5000
	} else {
		s.nodeLimit = 200000
	}
	s.positionSolve(0)
	return append([]packPos(nil), s.best...), true
}

// positionSolve assigns items in ascending ID order and tries positions
// lexicographically as (X, Y). The first complete assignment is therefore
// exactly the lexicographically smallest position vector. Empty atlas cells
// are simply never occupied, so no separate waste branch is needed.
func (s *search) positionSolve(depth int) bool {
	if s.nodeLimit > 0 && s.nodes >= s.nodeLimit {
		s.truncated = true
		return false
	}
	s.nodes++
	if depth == len(s.items) {
		s.best = append(s.best[:0], s.pack...)
		s.bestFound = true
		return true
	}
	if s.areaMask[s.remaining] > s.width*s.height-s.usedArea {
		return false
	}
	stateKey := s.stateKey()
	if _, ok := s.failed[stateKey]; ok {
		return false
	}

	idx := s.idOrder[depth]
	item := s.items[idx]
	start, ok := s.earliestFit(idx)
	if !ok {
		s.failed[stateKey] = struct{}{}
		return false
	}
	for x := start.x; x < s.width; x++ {
		y0 := int64(0)
		if x == start.x {
			y0 = start.y
		}
		for y := y0; y < s.height; y++ {
			for rotation := 0; rotation < 2; rotation++ {
				w, h := item.expW, item.expH
				if rotation == 1 {
					if item.expW == item.expH {
						continue
					}
					w, h = h, w
				}
				if x+w > s.width || y+h > s.height {
					continue
				}
				if !s.canPlace(x, y, w, h) {
					continue
				}
				s.pack[idx] = packPos{x: x, y: y, rotated: rotation == 1}
				s.placed[idx] = true
				s.occ = append(s.occ, packRect{x: x, y: y, w: w, h: h})
				s.usedArea += w * h
				s.remaining &^= uint16(1 << uint(idx))
				if s.positionSolve(depth + 1) {
					return true
				}
				s.remaining |= uint16(1 << uint(idx))
				s.usedArea -= w * h
				s.occ = s.occ[:len(s.occ)-1]
				s.placed[idx] = false
				s.pack[idx] = packPos{}
				if s.truncated {
					return false
				}
			}
		}
	}
	if s.truncated {
		return false
	}
	s.failed[stateKey] = struct{}{}
	return false
}

func packVectorLess(items []packItem, a, b []packPos) bool {
	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		ia, ib := items[order[i]], items[order[j]]
		if ia.id != ib.id {
			return ia.id < ib.id
		}
		return ia.index < ib.index
	})
	for _, idx := range order {
		if positionLess(a[idx], b[idx]) {
			return true
		}
		if positionLess(b[idx], a[idx]) {
			return false
		}
	}
	return false
}

type search struct {
	items   []packItem
	width   int64
	height  int64
	idOrder []int

	remaining uint16
	usedArea  int64
	occ       []packRect
	pack      []packPos
	placed    []bool
	areaMask  []int64
	failed    map[string]struct{}

	best      []packPos
	bestFound bool
	nodes     int64
	nodeLimit int64
	truncated bool
}

func newSearch(items []packItem, width, height int64) *search {
	n := len(items)
	s := &search{
		items:     items,
		width:     width,
		height:    height,
		remaining: uint16(1<<uint(n)) - 1,
		occ:       make([]packRect, 0, n),
		pack:      make([]packPos, n),
		placed:    make([]bool, n),
		areaMask:  make([]int64, 1<<uint(n)),
		failed:    make(map[string]struct{}),
	}
	s.idOrder = make([]int, n)
	for i, item := range items {
		s.idOrder[i] = item.index
	}
	sort.Slice(s.idOrder, func(i, j int) bool {
		a, b := items[s.idOrder[i]], items[s.idOrder[j]]
		if a.id != b.id {
			return a.id < b.id
		}
		return a.index < b.index
	})
	for mask := 1; mask < len(s.areaMask); mask++ {
		lsb := mask & -mask
		idx := 0
		for (1 << uint(idx)) != lsb {
			idx++
		}
		item := items[idx]
		s.areaMask[mask] = s.areaMask[mask&(mask-1)] + item.expW*item.expH
	}
	return s
}

// dfs reports whether at least one completion was visited and whether any
// branch was skipped by the incumbent bound. A bound-pruned branch is not an
// infeasible state and must not be memoized as one.
func (s *search) canPlace(x, y, w, h int64) bool {
	for _, r := range s.occ {
		if x < r.x+r.w && r.x < x+w && y < r.y+r.h && r.y < y+h {
			return false
		}
	}
	return true
}

// earliestFit returns the lexicographically earliest (X, Y) position at which
// item idx can be placed in the currently free space. Only bottom-left stable
// corners are needed: sliding a fitting rectangle left and then down cannot
// make its position worse. The result is an optimistic lower bound because
// other unplaced rectangles still have to fit around it.
func (s *search) earliestFit(idx int) (packPos, bool) {
	item := s.items[idx]
	xs := quickCandidates(s.occ, s.width, true)
	ys := quickCandidates(s.occ, s.height, false)
	var best packPos
	found := false
	for _, rotated := range []bool{false, true} {
		w, h := item.expW, item.expH
		if rotated {
			if w == h {
				continue
			}
			w, h = h, w
		}
		if w > s.width || h > s.height {
			continue
		}
		for _, x := range xs {
			if x+w > s.width {
				continue
			}
			for _, y := range ys {
				if y+h > s.height {
					continue
				}
				clear := true
				for _, r := range s.occ {
					if x < r.x+r.w && r.x < x+w && y < r.y+r.h && r.y < y+h {
						clear = false
						break
					}
				}
				if !clear {
					continue
				}
				pos := packPos{x: x, y: y, rotated: rotated}
				if !found || positionLess(pos, best) {
					best = pos
					found = true
				}
				break
			}
		}
	}
	return best, found
}

func positionLess(a, b packPos) bool {
	if a.x != b.x {
		return a.x < b.x
	}
	if a.y != b.y {
		return a.y < b.y
	}
	return !a.rotated && b.rotated
}

// stateKey identifies occupancy and the set of remaining items. Item IDs do
// not matter for feasibility, so the key is independent of placement history.
func (s *search) stateKey() string {
	b := make([]byte, 0, 96)
	b = strconv.AppendUint(b, uint64(s.remaining), 16)
	b = append(b, '|')
	rects := make([]packRect, len(s.occ))
	copy(rects, s.occ)
	sort.Slice(rects, func(i, j int) bool {
		a, b := rects[i], rects[j]
		if a.x != b.x {
			return a.x < b.x
		}
		if a.y != b.y {
			return a.y < b.y
		}
		if a.w != b.w {
			return a.w < b.w
		}
		return a.h < b.h
	})
	for _, r := range rects {
		b = strconv.AppendInt(b, r.x, 10)
		b = append(b, ',')
		b = strconv.AppendInt(b, r.y, 10)
		b = append(b, ',')
		b = strconv.AppendInt(b, r.w, 10)
		b = append(b, ',')
		b = strconv.AppendInt(b, r.h, 10)
		b = append(b, ';')
	}
	return string(b)
}
