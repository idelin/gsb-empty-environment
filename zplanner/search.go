package zplanner

import (
	"sort"
	"strings"
)

const maxSearchDepth = 240

type searchState struct {
	fills [MaxZones]int
	loc   map[CID]Phys
	roots map[string]CID
	snaps map[string]map[string]CID
	// staged holds appended pages not yet folded into a published table.
	staged map[CID]Phys
	gen    int
	lastOp int
	opIdx  int
	cost   Cost
}

type planner struct {
	ops       []Op
	best      Cost
	bestSteps []Step
	found     bool
	memo      map[string]Cost
}

// Plan computes the exact-optimal fault-free plan for ops starting at init.
// It returns *ErrInfeasible when the operation sequence cannot be completed
// legally within five zones of eight pages.
func Plan(init InitialState, ops []Op) (*CommitPlan, error) {
	if len(ops) > MaxOps {
		return nil, &ErrInfeasible{reason: "too many client operations"}
	}
	for i := 1; i < len(ops); i++ {
		if ops[i].ID <= ops[i-1].ID {
			return nil, &ErrInfeasible{reason: "operation IDs must be strictly increasing"}
		}
	}
	seen := map[CID]bool{}
	for _, op := range ops {
		for _, cid := range op.Writes {
			seen[cid] = true
		}
	}
	for id := range init.Table.Loc {
		seen[id] = true
	}
	if len(seen) > MaxCIDs {
		return nil, &ErrInfeasible{reason: "more than eight distinct content IDs"}
	}

	start := &searchState{
		fills:  init.Fill,
		loc:    cloneLoc(init.Table.Loc),
		roots:  cloneRoots(init.Table.Roots),
		snaps:  cloneSnaps(init.Table.Snaps),
		staged: map[CID]Phys{},
		gen:    init.Table.Generation,
		lastOp: init.Table.LastOp,
	}
	for z := range start.fills {
		if start.fills[z] > PagesPerZone {
			return nil, &ErrInfeasible{reason: "initial zone fill exceeds capacity"}
		}
	}
	// The root table only locates content that a root or snapshot references.
	start.loc = pruneLoc(start.loc, referenced(start.roots, start.snaps))

	p := &planner{ops: ops, memo: map[string]Cost{}}
	p.dfs(start, nil, 0)
	if !p.found {
		return nil, &ErrInfeasible{reason: "no legal plan fits the device geometry"}
	}
	return &CommitPlan{Steps: p.bestSteps, Cost: p.best}, nil
}

func (p *planner) dfs(st *searchState, steps []Step, depth int) {
	if st.opIdx >= len(p.ops) {
		if !p.found || st.cost.Less(p.best) {
			p.found = true
			p.best = st.cost
			p.bestSteps = append([]Step(nil), steps...)
		}
		return
	}
	if depth >= maxSearchDepth {
		return
	}
	if st.cost.Copies > MaxCIDs*MaxOps {
		return
	}
	key := stateKey(st)
	if prev, ok := p.memo[key]; ok && !st.cost.Less(prev) {
		return
	}
	p.memo[key] = st.cost
	if p.found && !st.cost.Less(p.best) {
		return
	}

	op := p.ops[st.opIdx]
	freePages := st.freePageCount()
	needNow := 0
	if op.Kind == OpWrite {
		needNow = len(st.missingWrites(op))
	}
	// Reclamation is only useful when space is tight, which keeps the search
	// focused on plans that could actually be necessary.
	reclaimUseful := freePages == 0 || (needNow > 0 && freePages < needNow)

	// Space management is available before every client operation, letting
	// the planner reclaim zones proactively for later writes. Memoization and
	// the depth cap keep these branches bounded.

	// A. Reset an unpinned, non-empty zone (garbage reclamation).
	if reclaimUseful {
		for z := ZoneID(0); z < MaxZones; z++ {
			if st.fills[z] == 0 || zonePinned(st, z) {
				continue
			}
			next := st.clone()
			next.cost.Erases++
			next.fills[z] = 0
			p.dfs(next, append(steps, Step{Kind: StepReset, Zone: z}), depth+1)
		}
	}

	// B. Relocate live content out of a pinned zone using a free page.
	if reclaimUseful && freePages > 0 {
		live := referenced(st.roots, st.snaps)
		for srcZone := ZoneID(0); srcZone < MaxZones; srcZone++ {
			for id, phys := range st.loc {
				if !live[id] || phys.Zone != srcZone {
					continue
				}
				if _, queued := st.staged[id]; queued {
					continue
				}
				for z := ZoneID(0); z < MaxZones; z++ {
					if z == srcZone || st.fills[z] >= PagesPerZone {
						continue
					}
					next := st.clone()
					next.cost.Copies++
					next.staged[id] = Phys{Zone: z, Off: uint8(next.fills[z])}
					next.fills[z]++
					p.dfs(next, append(steps, Step{Kind: StepCopy, CID: id, Zone: z}), depth+1)
				}
			}
		}
	}

	// C. Fold staged copies so their source zones become unpinned.
	if len(st.staged) > 0 {
		next, step := st.relocationPublish()
		p.dfs(next, append(steps, step), depth+1)
	}

	// D. Take the current client operation. Snapshot and delete need no space.
	if op.Kind != OpWrite {
		if final := st.finalizeClient(op); final != nil {
			p.dfs(final.state, append(steps, final.step), depth+1)
		}
		return
	}

	need := st.missingWrites(op)
	if len(need) == 0 {
		if final := st.finalizeClient(op); final != nil {
			p.dfs(final.state, append(steps, final.step), depth+1)
		}
		return
	}
	if freePages < len(need) {
		// No room right now; the space-management branches above recurse.
		return
	}
	stagedAll := st
	built := append([]Step(nil), steps...)
	for id := range need {
		z := stagedAll.firstFreeZone()
		if z < 0 {
			return
		}
		ns := stagedAll.clone()
		ns.staged[id] = Phys{Zone: ZoneID(z), Off: uint8(ns.fills[z])}
		ns.fills[z]++
		built = append(built, Step{Kind: StepWrite, CID: id, Zone: ZoneID(z)})
		stagedAll = ns
	}
	if final := stagedAll.finalizeClient(op); final != nil {
		p.dfs(final.state, append(built, final.step), depth+len(built)+1)
	}
}

type finalized struct {
	state *searchState
	step  Step
}

func (st *searchState) finalizeClient(op Op) *finalized {
	next := st.clone()
	switch op.Kind {
	case OpWrite:
		for page, id := range op.Writes {
			if _, ok := next.loc[id]; !ok {
				if _, ok := next.staged[id]; !ok {
					return nil
				}
			}
			next.roots[page] = id
		}
	case OpSnapshot:
		cp := make(map[string]CID, len(next.roots))
		for page, id := range next.roots {
			cp[page] = id
		}
		next.snaps[op.Name] = cp
	case OpDeleteSnapshot:
		if _, ok := next.snaps[op.Name]; !ok {
			return nil
		}
		delete(next.snaps, op.Name)
	}
	next.foldStaged()
	next.opIdx++
	next.lastOp = op.ID
	next.gen++
	return &finalized{
		state: next,
		step:  Step{Kind: StepPublish, OpID: op.ID, Table: next.table(op.ID)},
	}
}

func (st *searchState) relocationPublish() (*searchState, Step) {
	next := st.clone()
	next.foldStaged()
	next.gen++
	return next, Step{Kind: StepPublish, OpID: -1, Table: next.table(next.lastOp)}
}

func (st *searchState) foldStaged() {
	for id, phys := range st.staged {
		st.loc[id] = phys
	}
	st.staged = map[CID]Phys{}
	st.loc = pruneLoc(st.loc, referenced(st.roots, st.snaps))
}

func (st *searchState) table(opID int) *Table {
	return &Table{
		Generation: st.gen,
		LastOp:     opID,
		Loc:        cloneLoc(st.loc),
		Roots:      cloneRoots(st.roots),
		Snaps:      cloneSnaps(st.snaps),
	}
}

func zonePinned(st *searchState, z ZoneID) bool {
	for _, phys := range st.loc {
		if phys.Zone == z {
			return true
		}
	}
	for _, phys := range st.staged {
		if phys.Zone == z {
			return true
		}
	}
	return false
}

func (st *searchState) freePageCount() int {
	total := 0
	for _, f := range st.fills {
		total += PagesPerZone - f
	}
	return total
}

func (st *searchState) firstFreeZone() int {
	for z := ZoneID(0); z < MaxZones; z++ {
		if st.fills[z] < PagesPerZone {
			return int(z)
		}
	}
	return -1
}

// missingWrites returns the brand-new CIDs the write still has to append.
func (st *searchState) missingWrites(op Op) map[CID]bool {
	out := map[CID]bool{}
	for _, id := range op.Writes {
		if _, have := st.loc[id]; have {
			continue
		}
		if _, queued := st.staged[id]; queued {
			continue
		}
		out[id] = true
	}
	return out
}

func referenced(roots map[string]CID, snaps map[string]map[string]CID) map[CID]bool {
	out := map[CID]bool{}
	for _, id := range roots {
		out[id] = true
	}
	for _, snap := range snaps {
		for _, id := range snap {
			out[id] = true
		}
	}
	return out
}

func pruneLoc(loc map[CID]Phys, live map[CID]bool) map[CID]Phys {
	out := make(map[CID]Phys, len(loc))
	for id, phys := range loc {
		if live[id] {
			out[id] = phys
		}
	}
	return out
}

func (st *searchState) clone() *searchState {
	return &searchState{
		fills:  st.fills,
		loc:    cloneLoc(st.loc),
		roots:  cloneRoots(st.roots),
		snaps:  cloneSnaps(st.snaps),
		staged: cloneLoc(st.staged),
		gen:    st.gen,
		lastOp: st.lastOp,
		opIdx:  st.opIdx,
		cost:   st.cost,
	}
}

func cloneLoc(in map[CID]Phys) map[CID]Phys {
	out := make(map[CID]Phys, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneRoots(in map[string]CID) map[string]CID {
	out := make(map[string]CID, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneSnaps(in map[string]map[string]CID) map[string]map[string]CID {
	out := make(map[string]map[string]CID, len(in))
	for name, snap := range in {
		cp := make(map[string]CID, len(snap))
		for k, v := range snap {
			cp[k] = v
		}
		out[name] = cp
	}
	return out
}

// stateKey canonicalizes the planning state. Generation, lastOp and cost are
// derivable and intentionally excluded; opIdx fixes the remaining history.
func stateKey(st *searchState) string {
	var b strings.Builder
	b.WriteByte('o')
	b.WriteByte(byte(st.opIdx))
	for _, f := range st.fills {
		b.WriteByte(byte(f))
	}
	writeCIDPhys := func(m map[CID]Phys) {
		ids := make([]CID, 0, len(m))
		for id := range m {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			p := m[id]
			b.WriteByte(byte(id))
			b.WriteByte(byte(p.Zone))
			b.WriteByte(byte(p.Off))
		}
		b.WriteByte('|')
	}
	writeCIDPhys(st.loc)
	writeCIDPhys(st.staged)

	pages := make([]string, 0, len(st.roots))
	for page := range st.roots {
		pages = append(pages, page)
	}
	sort.Strings(pages)
	for _, page := range pages {
		b.WriteString(page)
		b.WriteByte('=')
		b.WriteByte(byte(st.roots[page]))
		b.WriteByte(';')
	}
	b.WriteByte('|')

	names := make([]string, 0, len(st.snaps))
	for name := range st.snaps {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		snap := st.snaps[name]
		sp := make([]string, 0, len(snap))
		for page := range snap {
			sp = append(sp, page)
		}
		sort.Strings(sp)
		b.WriteString(name)
		b.WriteByte(':')
		for _, page := range sp {
			b.WriteString(page)
			b.WriteByte('=')
			b.WriteByte(byte(snap[page]))
			b.WriteByte(',')
		}
		b.WriteByte(';')
	}
	return b.String()
}
