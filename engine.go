package netsec

import "sort"

type undoMove struct {
	idx      int
	from     PhysicalPage
	target   PhysicalPage
	oldW     int64
	sameGeom bool
}

type engine struct {
	s *scenario

	sensCount map[locKey]int
	attCount  map[locKey]int
	sensAt    map[locKey][]PageID
	attAt     map[locKey][]PageID

	cur     []PhysicalPage
	moved   []int
	isMoved []bool
	w       int64
}

func newEngine(s *scenario) *engine {
	e := &engine{
		s:         s,
		sensCount: make(map[locKey]int),
		attCount:  make(map[locKey]int),
		sensAt:    make(map[locKey][]PageID),
		attAt:     make(map[locKey][]PageID),
		cur:       append([]PhysicalPage(nil), s.initial...),
		isMoved:   make([]bool, len(s.pages)),
	}
	for i, p := range s.pages {
		if !s.mapped[e.cur[i]] {
			continue
		}
		k := keyFromLocation(s.location[e.cur[i]])
		switch p.Role {
		case RoleSensitive:
			e.sensCount[k]++
			e.sensAt[k] = append(e.sensAt[k], p.ID)
		case RoleAttacker:
			e.attCount[k]++
			e.attAt[k] = append(e.attAt[k], p.ID)
		}
	}
	e.w = e.recomputeW()
	return e
}

func (e *engine) recomputeW() int64 {
	var total int64
	for k := range e.sensCount {
		total += e.term(k)
	}
	return total
}

func (e *engine) term(centre locKey) int64 {
	if centre.row <= 0 || centre.row+1 >= e.s.limits().Rows {
		return 0
	}
	s := e.sensCount[centre]
	if s == 0 {
		return 0
	}
	below := e.attCount[locKey{channel: centre.channel, bank: centre.bank, row: centre.row - 1}]
	above := e.attCount[locKey{channel: centre.channel, bank: centre.bank, row: centre.row + 1}]
	return int64(s) * int64(below) * int64(above)
}

func (s *scenario) limits() Limits {
	return s.layout.Limits
}

func (e *engine) keyAtPhysical(physical PhysicalPage) (locKey, bool) {
	if physical < 0 || physical >= PhysicalPageCount || !e.s.mapped[physical] {
		return locKey{}, false
	}
	return keyFromLocation(e.s.location[physical]), true
}

func (e *engine) currentKey(idx int) (locKey, bool) {
	return e.keyAtPhysical(e.cur[idx])
}

func (e *engine) usefulTarget(idx int, target PhysicalPage) bool {
	oldKey, oldMapped := e.keyAtPhysical(e.cur[idx])
	newKey, newMapped := e.keyAtPhysical(target)
	if oldMapped != newMapped {
		return true
	}
	if !oldMapped {
		return false
	}
	return oldKey != newKey
}

func (e *engine) addAt(k locKey, p Page) {
	switch p.Role {
	case RoleSensitive:
		e.sensCount[k]++
		e.sensAt[k] = append(e.sensAt[k], p.ID)
	case RoleAttacker:
		e.attCount[k]++
		e.attAt[k] = append(e.attAt[k], p.ID)
	}
}

func (e *engine) removeAt(k locKey, p Page) {
	switch p.Role {
	case RoleSensitive:
		e.sensCount[k]--
		if e.sensCount[k] == 0 {
			delete(e.sensCount, k)
		}
		e.sensAt[k] = removePageID(e.sensAt[k], p.ID)
		if len(e.sensAt[k]) == 0 {
			delete(e.sensAt, k)
		}
	case RoleAttacker:
		e.attCount[k]--
		if e.attCount[k] == 0 {
			delete(e.attCount, k)
		}
		e.attAt[k] = removePageID(e.attAt[k], p.ID)
		if len(e.attAt[k]) == 0 {
			delete(e.attAt, k)
		}
	}
}

func removePageID(ids []PageID, id PageID) []PageID {
	for i, candidate := range ids {
		if candidate == id {
			copy(ids[i:], ids[i+1:])
			return ids[:len(ids)-1]
		}
	}
	return ids
}

func affectedCentres(role Role, k locKey) []locKey {
	switch role {
	case RoleSensitive:
		return []locKey{k}
	case RoleAttacker:
		return []locKey{k.below(), k.above()}
	default:
		return nil
	}
}

func (e *engine) applyMove(idx int, target PhysicalPage) undoMove {
	oldPhysical := e.cur[idx]
	u := undoMove{idx: idx, from: oldPhysical, target: target, oldW: e.w}
	oldKey, oldMapped := e.keyAtPhysical(oldPhysical)
	newKey, newMapped := e.keyAtPhysical(target)

	if oldMapped == newMapped && (!oldMapped || oldKey == newKey) {
		u.sameGeom = true
		e.cur[idx] = target
		e.isMoved[idx] = true
		e.moved = append(e.moved, idx)
		return u
	}

	affectedSet := make(map[locKey]bool, 4)
	if oldMapped {
		for _, centre := range affectedCentres(e.s.pages[idx].Role, oldKey) {
			affectedSet[centre] = true
		}
	}
	if newMapped {
		for _, centre := range affectedCentres(e.s.pages[idx].Role, newKey) {
			affectedSet[centre] = true
		}
	}
	var before int64
	for centre := range affectedSet {
		before += e.term(centre)
	}

	if oldMapped {
		e.removeAt(oldKey, e.s.pages[idx])
	}
	if newMapped {
		e.addAt(newKey, e.s.pages[idx])
	}

	var after int64
	for centre := range affectedSet {
		after += e.term(centre)
	}
	e.w += after - before

	e.cur[idx] = target
	e.isMoved[idx] = true
	e.moved = append(e.moved, idx)
	return u
}

func (e *engine) undo(u undoMove) {
	last := e.moved[len(e.moved)-1]
	if last != u.idx {
		panic("netsec: non-LIFO move undo")
	}
	e.moved = e.moved[:len(e.moved)-1]
	e.isMoved[u.idx] = false
	e.cur[u.idx] = u.from
	e.w = u.oldW

	if u.sameGeom {
		return
	}
	newKey, newMapped := e.keyAtPhysical(u.target)
	if newMapped {
		e.removeAt(newKey, e.s.pages[u.idx])
	}
	oldKey, oldMapped := e.keyAtPhysical(u.from)
	if oldMapped {
		e.addAt(oldKey, e.s.pages[u.idx])
	}
}

func (e *engine) movedCount() int {
	return len(e.moved)
}

func (e *engine) witnessCount() int64 {
	return e.w
}

// participates reports whether the page, at its current location, is part of
// at least one witness.
func (e *engine) participates(idx int) bool {
	k, ok := e.currentKey(idx)
	if !ok {
		return false
	}
	switch e.s.pages[idx].Role {
	case RoleSensitive:
		return e.attCount[k.below()] > 0 && e.attCount[k.above()] > 0
	case RoleAttacker:
		// The attacker is below a centre at row+1.
		centre := locKey{channel: k.channel, bank: k.bank, row: k.row + 1}
		if e.sensCount[centre] > 0 {
			opposite := locKey{channel: centre.channel, bank: centre.bank, row: centre.row + 1}
			if e.attCount[opposite] > 0 {
				return true
			}
		}
		// The attacker is above a centre at row-1.
		centre = locKey{channel: k.channel, bank: k.bank, row: k.row - 1}
		if e.sensCount[centre] > 0 {
			opposite := locKey{channel: centre.channel, bank: centre.bank, row: centre.row - 1}
			if e.attCount[opposite] > 0 {
				return true
			}
		}
	}
	return false
}

func minPageID(ids []PageID) (PageID, bool) {
	if len(ids) == 0 {
		return 0, false
	}
	best := ids[0]
	for _, id := range ids[1:] {
		if id < best {
			best = id
		}
	}
	return best, true
}

func (e *engine) witnessContaining(idx int) (Witness, bool) {
	k, ok := e.currentKey(idx)
	if !ok {
		return Witness{}, false
	}
	p := e.s.pages[idx]
	switch p.Role {
	case RoleSensitive:
		below, okBelow := minPageID(e.attAt[k.below()])
		above, okAbove := minPageID(e.attAt[k.above()])
		if !okBelow || !okAbove {
			return Witness{}, false
		}
		return e.witnessForIDs(p.ID, below, above, k), true
	case RoleAttacker:
		centre := locKey{channel: k.channel, bank: k.bank, row: k.row + 1}
		if sensitive, okS := minPageID(e.sensAt[centre]); okS {
			opposite := locKey{channel: centre.channel, bank: centre.bank, row: centre.row + 1}
			if above, okA := minPageID(e.attAt[opposite]); okA {
				return e.witnessForIDs(sensitive, p.ID, above, centre), true
			}
		}
		centre = locKey{channel: k.channel, bank: k.bank, row: k.row - 1}
		if sensitive, okS := minPageID(e.sensAt[centre]); okS {
			opposite := locKey{channel: centre.channel, bank: centre.bank, row: centre.row - 1}
			if below, okA := minPageID(e.attAt[opposite]); okA {
				return e.witnessForIDs(sensitive, below, p.ID, centre), true
			}
		}
	}
	return Witness{}, false
}

func (e *engine) witnessForIDs(sensitive, below, above PageID, centre locKey) Witness {
	return Witness{
		Sensitive:             sensitive,
		AttackerBelow:         below,
		AttackerAbove:         above,
		Channel:               centre.channel,
		Bank:                  centre.bank,
		Row:                   centre.row,
		SensitivePhysical:     e.cur[e.s.pageByID[sensitive]],
		AttackerBelowPhysical: e.cur[e.s.pageByID[below]],
		AttackerAbovePhysical: e.cur[e.s.pageByID[above]],
	}
}

// findWitness returns one current witness.  Initial witnesses are checked
// first; after all of them are broken, every new witness must contain a moved
// page and can be found by examining only the at-most-twenty moved pages.
func (e *engine) findWitness() (Witness, bool) {
	for _, w := range e.s.witnesses {
		si := e.s.pageByID[w.Sensitive]
		bi := e.s.pageByID[w.AttackerBelow]
		ai := e.s.pageByID[w.AttackerAbove]
		if !e.isMoved[si] && !e.isMoved[bi] && !e.isMoved[ai] {
			return w, true
		}
	}

	moved := append([]int(nil), e.moved...)
	sort.Slice(moved, func(i, j int) bool {
		return e.s.pages[moved[i]].ID < e.s.pages[moved[j]].ID
	})
	for _, idx := range moved {
		if w, ok := e.witnessContaining(idx); ok {
			return w, true
		}
	}

	// Defensive fallback: this path should be unreachable when w > 0, but it
	// keeps search complete if the incremental index is ever out of sync.
	keys := make([]locKey, 0, len(e.sensCount))
	for k, count := range e.sensCount {
		if count > 0 {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].channel != keys[j].channel {
			return keys[i].channel < keys[j].channel
		}
		if keys[i].bank != keys[j].bank {
			return keys[i].bank < keys[j].bank
		}
		return keys[i].row < keys[j].row
	})
	for _, centre := range keys {
		sensitive, okS := minPageID(e.sensAt[centre])
		below, okB := minPageID(e.attAt[centre.below()])
		above, okA := minPageID(e.attAt[centre.above()])
		if okS && okB && okA {
			return e.witnessForIDs(sensitive, below, above, centre), true
		}
	}
	return Witness{}, false
}

func (e *engine) currentMoves() []Move {
	out := make([]Move, 0, len(e.moved))
	for _, idx := range e.moved {
		out = append(out, Move{
			Page: e.s.pages[idx].ID,
			From: e.s.initial[idx],
			To:   e.cur[idx],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Page < out[j].Page })
	return out
}
