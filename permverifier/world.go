package permverifier

type genKey struct {
	object uint64
	gen    uint64
}

// cowNode is one copy-on-write page table. Forked nodes share their parent's
// frames until a write allocates a private frame in the child's private map.
type cowNode struct {
	view      uint64
	off       uint64
	length    uint64
	page      uint64
	pages     uint64
	rootPAddr uint64
	private   map[uint64]uint64
	parent    *cowNode
}

func (n *cowNode) translate(page uint64) uint64 {
	if p, ok := n.private[page]; ok {
		return p
	}
	if n.parent != nil {
		return n.parent.translate(page)
	}
	base, _ := add64(n.rootPAddr, mulNoOverflow(page, n.page))
	return base
}

func mulNoOverflow(a, b uint64) uint64 {
	p, _ := mul64(a, b)
	return p
}

type viewState struct {
	id       uint64
	object   uint64
	gen      uint64
	off      uint64
	length   uint64
	perm     Perm
	cpus     []uint64
	segs     []Segment
	cow      *cowNode
	revoking bool
}

type pendingRevoke struct {
	remaining map[uint64]bool
}

// world is one concrete memory-host configuration reachable in an interleaving.
type world struct {
	allocs   []Interval
	views    map[uint64]*viewState
	gens     map[genKey][]*viewState
	revokes  map[genKey]*pendingRevoke
	pool     Interval
	poolUsed uint64
}

func newWorld(pool Interval) *world {
	return &world{
		views:   map[uint64]*viewState{},
		gens:    map[genKey][]*viewState{},
		revokes: map[genKey]*pendingRevoke{},
		pool:    pool,
	}
}

func (w *world) clone() *world {
	c := &world{
		allocs:   append([]Interval(nil), w.allocs...),
		views:    make(map[uint64]*viewState, len(w.views)),
		gens:     make(map[genKey][]*viewState, len(w.gens)),
		revokes:  make(map[genKey]*pendingRevoke, len(w.revokes)),
		pool:     w.pool,
		poolUsed: w.poolUsed,
	}
	nodeCopies := map[*cowNode]*cowNode{}
	var copyNode func(n *cowNode) *cowNode
	copyNode = func(n *cowNode) *cowNode {
		if n == nil {
			return nil
		}
		if cn, ok := nodeCopies[n]; ok {
			return cn
		}
		cn := &cowNode{
			view:      n.view,
			off:       n.off,
			length:    n.length,
			page:      n.page,
			pages:     n.pages,
			rootPAddr: n.rootPAddr,
			private:   make(map[uint64]uint64, len(n.private)),
		}
		nodeCopies[n] = cn
		for p, addr := range n.private {
			cn.private[p] = addr
		}
		cn.parent = copyNode(n.parent)
		return cn
	}
	for id, v := range w.views {
		cv := &viewState{
			id:       v.id,
			object:   v.object,
			gen:      v.gen,
			off:      v.off,
			length:   v.length,
			perm:     v.perm,
			cpus:     append([]uint64(nil), v.cpus...),
			segs:     append([]Segment(nil), v.segs...),
			revoking: v.revoking,
		}
		cv.cow = copyNode(v.cow)
		c.views[id] = cv
	}
	for k, vs := range w.gens {
		c.gens[k] = append([]*viewState(nil), vs...)
	}
	for k, r := range w.revokes {
		cp := &pendingRevoke{remaining: make(map[uint64]bool, len(r.remaining))}
		for cpu := range r.remaining {
			cp.remaining[cpu] = true
		}
		c.revokes[k] = cp
	}
	return c
}

func (w *world) frameAllocated(phys Interval) bool {
	for _, a := range w.allocs {
		if a.Overlaps(phys) {
			return true
		}
	}
	return false
}

func (w *world) exactAlloc(phys Interval) bool {
	for _, a := range w.allocs {
		if a == phys {
			return true
		}
	}
	return false
}

func (w *world) poolAllocate(n uint64) (uint64, bool) {
	base, carry := add64(w.pool.Lo, w.poolUsed)
	if carry {
		return 0, false
	}
	used, carry := add64(w.poolUsed, n)
	if carry {
		return 0, false
	}
	if used > w.pool.Len {
		return 0, false
	}
	w.poolUsed = used
	return base, true
}

// enabled reports whether the op's preconditions hold in this world. Ops whose
// preconditions never hold along a terminal interleaving make it incomplete.
func (w *world) enabled(op Op) bool {
	switch {
	case op.Alloc != nil:
		if op.Alloc.Reuse {
			return true
		}
		return !w.frameAllocated(op.Alloc.Phys)
	case op.Free != nil:
		return w.exactAlloc(op.Free.Phys)
	case op.Map != nil:
		return w.mapEnabled(op.Map)
	case op.Remap != nil:
		return w.mapEnabled(&op.Remap.Map)
	case op.Revoke != nil:
		key := genKey{op.Revoke.Object, op.Revoke.Gen}
		_, hasGen := w.gens[key]
		_, pending := w.revokes[key]
		return hasGen && !pending
	case op.Ack != nil:
		return true
	case op.Fork != nil:
		f := op.Fork
		parent, ok := w.views[f.Parent]
		if !ok || parent.cow == nil {
			return false
		}
		if _, taken := w.views[f.View]; taken {
			return false
		}
		_, genTaken := w.gens[genKey{f.Object, f.Gen}]
		return !genTaken
	case op.Write != nil:
		wr := op.Write
		v, ok := w.views[wr.View]
		if !ok || v.object != wr.Object || v.gen != wr.Gen || v.revoking {
			return false
		}
		if !withinView(v, wr.Off, wr.Len) {
			return false
		}
		return v.perm.Has(PermWrite)
	default:
		return false
	}
}

func withinView(v *viewState, off, length uint64) bool {
	if off < v.off {
		return false
	}
	rel := off - v.off
	end, carry := add64(rel, length)
	if carry {
		return false
	}
	return end <= v.length
}

func (w *world) mapEnabled(m *MapOp) bool {
	if _, taken := w.views[m.View]; taken {
		return false
	}
	if _, genTaken := w.gens[genKey{m.Object, m.Gen}]; genTaken {
		return false
	}
	if m.Cow != nil {
		bytes, overflow := mul64(pagesFor(m.Cow.Len, m.Cow.Page), m.Cow.Page)
		if overflow {
			return false
		}
		return w.frameAllocated(Interval{Lo: m.Cow.PAddr, Len: bytes})
	}
	for _, s := range m.Segments {
		phys, ok := segmentInterval(s)
		if !ok || !w.frameAllocated(phys) {
			return false
		}
	}
	return true
}

func segmentInterval(s Segment) (Interval, bool) {
	if s.Len == 0 {
		return Interval{}, false
	}
	sum, carry := add64(s.PAddr, s.Len)
	// A carry producing a zero sum is the exact [x, 2^64) range.
	return Interval{Lo: s.PAddr, Len: s.Len}, !carry || (carry && sum == 0)
}

// apply executes an op known to be enabled. It returns false only on pool
// exhaustion, which static validation guarantees never happens.
func (w *world) apply(op Op) bool {
	switch {
	case op.Alloc != nil:
		a := op.Alloc
		if !a.Reuse || !w.frameAllocated(a.Phys) {
			w.allocs = append(w.allocs, a.Phys)
		}
	case op.Free != nil:
		f := op.Free.Phys
		for i, a := range w.allocs {
			if a == f {
				w.allocs = append(w.allocs[:i], w.allocs[i+1:]...)
				break
			}
		}
	case op.Map != nil:
		w.installMap(op.Map)
	case op.Remap != nil:
		w.installMap(&op.Remap.Map)
	case op.Revoke != nil:
		w.applyRevoke(op.Revoke.Object, op.Revoke.Gen)
	case op.Ack != nil:
		w.applyAck(op.Ack.CPU, op.Ack.Object, op.Ack.Gen)
	case op.Fork != nil:
		w.applyFork(op.Fork)
	case op.Write != nil:
		return w.applyWrite(op.Write)
	}
	return true
}

func pagesFor(length, page uint64) uint64 {
	if page == 0 {
		return 0
	}
	q, r := length/page, length%page
	if r != 0 {
		q++
	}
	return q
}

func (w *world) installMap(m *MapOp) {
	v := &viewState{
		id:     m.View,
		object: m.Object,
		gen:    m.Gen,
		off:    m.Off,
		length: m.Len,
		perm:   m.Perm,
		cpus:   append([]uint64(nil), m.CPUs...),
		segs:   append([]Segment(nil), m.Segments...),
	}
	if m.Cow != nil {
		v.cow = &cowNode{
			view:      m.View,
			off:       m.Off,
			length:    m.Len,
			page:      m.Cow.Page,
			pages:     pagesFor(m.Cow.Len, m.Cow.Page),
			rootPAddr: m.Cow.PAddr,
			private:   map[uint64]uint64{},
		}
	}
	sortU64(v.cpus)
	w.views[m.View] = v
	key := genKey{m.Object, m.Gen}
	w.gens[key] = append(w.gens[key], v)
}

func (w *world) applyRevoke(object, gen uint64) {
	key := genKey{object, gen}
	remaining := map[uint64]bool{}
	for _, v := range w.gens[key] {
		v.revoking = true
		for _, cpu := range v.cpus {
			remaining[cpu] = true
		}
	}
	w.revokes[key] = &pendingRevoke{remaining: remaining}
}

func (w *world) applyAck(cpu, object, gen uint64) {
	key := genKey{object, gen}
	r, ok := w.revokes[key]
	if !ok {
		return
	}
	delete(r.remaining, cpu)
	if len(r.remaining) != 0 {
		return
	}
	delete(w.revokes, key)
	for _, v := range w.gens[key] {
		delete(w.views, v.id)
	}
	delete(w.gens, key)
}

func (w *world) applyFork(f *ForkOp) {
	parent := w.views[f.Parent]
	node := &cowNode{
		view:      f.View,
		off:       parent.off,
		length:    parent.length,
		page:      parent.cow.page,
		pages:     parent.cow.pages,
		rootPAddr: parent.cow.rootPAddr,
		private:   map[uint64]uint64{},
		parent:    parent.cow,
	}
	v := &viewState{
		id:     f.View,
		object: f.Object,
		gen:    f.Gen,
		off:    parent.off,
		length: parent.length,
		perm:   f.Perm,
		cpus:   append([]uint64(nil), f.CPUs...),
		cow:    node,
	}
	sortU64(v.cpus)
	w.views[f.View] = v
	key := genKey{f.Object, f.Gen}
	w.gens[key] = append(w.gens[key], v)
}

func (w *world) applyWrite(wr *WriteOp) bool {
	v := w.views[wr.View]
	if v.cow == nil || wr.Len == 0 {
		return true
	}
	rel, under := sub64(wr.Off, v.off)
	if under {
		return true
	}
	lastRel, carry := add64(rel, wr.Len-1)
	if carry {
		lastRel = mathMaxUint64
	}
	first := rel / v.cow.page
	last := lastRel / v.cow.page
	for page := first; page <= last; page++ {
		if page >= v.cow.pages {
			break
		}
		if _, split := v.cow.private[page]; split {
			continue
		}
		base, ok := w.poolAllocate(v.cow.page)
		if !ok {
			return false
		}
		v.cow.private[page] = base
	}
	return true
}

// coverage enumerates every physical byte reachable through any effective view
// on any CPU as disjoint, attributed pieces.
func (w *world) coverage() []coveragePiece {
	var frags []frag
	for _, v := range w.views {
		for _, cpu := range v.cpus {
			ref := ViewRef{ID: v.id, Gen: v.gen, CPU: cpu, Perm: v.perm}
			if v.cow != nil {
				frags = append(frags, cowFrags(v, ref)...)
				continue
			}
			for _, s := range v.segs {
				frags = append(frags, frag{
					phys: Interval{Lo: s.PAddr, Len: s.Len},
					ref:  ref,
				})
			}
		}
	}
	return mergeFrags(frags)
}

func cowFrags(v *viewState, ref ViewRef) []frag {
	var out []frag
	for page := uint64(0); page < v.cow.pages; page++ {
		physLo := v.cow.translate(page)
		out = append(out, frag{
			phys: Interval{Lo: physLo, Len: v.cow.page},
			ref:  ref,
		})
	}
	return out
}

// firstConflict returns the lowest physical interval where some effective view
// writes and some effective view executes the same byte.
func (w *world) firstConflict() *Conflict {
	for _, piece := range w.coverage() {
		if !(piece.W && piece.X) {
			continue
		}
		c := &Conflict{Phys: piece.Phys}
		for _, r := range piece.Refs {
			if r.Perm.Has(PermWrite) {
				c.Writers = append(c.Writers, r)
			}
			if r.Perm.Has(PermExec) {
				c.Execers = append(c.Execers, r)
			}
		}
		return c
	}
	return nil
}
