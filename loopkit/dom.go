package loopkit

// DomTree holds dominator information for a function CFG.
type DomTree struct {
	f      *Func
	rpo    []*Block          // reverse postorder, entry first
	rpoNum map[*Block]int    // block -> RPO index
	idom   map[*Block]*Block // immediate dominator
	dom    map[*Block]*Block // alias of idom for clarity
}

// RPO returns the reverse postorder block list.
func (d *DomTree) RPO() []*Block { return d.rpo }

// IDom returns the immediate dominator (nil for the entry).
func (d *DomTree) IDom(b *Block) *Block { return d.idom[b] }

// Dominates reports whether a dominates b (reflexive).
func (d *DomTree) Dominates(a, b *Block) bool {
	if a == nil || b == nil {
		return false
	}
	for x := b; x != nil; x = d.idom[x] {
		if x == a {
			return true
		}
	}
	return false
}

// InstrDominates reports whether instruction a is guaranteed to execute
// before any use in block b (a dominates b, or same block ordering).
func (d *DomTree) InstrDominates(a *Instr, b *Block) bool {
	if a == nil || a.Block == nil {
		return false
	}
	if a.Block == b {
		for _, i := range b.Instrs {
			if i == a {
				return true
			}
		}
		return false
	}
	return d.Dominates(a.Block, b)
}

// ComputeDomTree builds the dominator tree using the iterative
// Cooper-Harvey-Kennedy algorithm over reverse postorder numbering.
func ComputeDomTree(f *Func) *DomTree {
	d := &DomTree{
		f:      f,
		rpoNum: map[*Block]int{},
		idom:   map[*Block]*Block{},
		dom:    map[*Block]*Block{},
	}
	d.rpo = reversePostOrder(f)
	for idx, b := range d.rpo {
		d.rpoNum[b] = idx
	}
	entry := f.Entry()
	d.idom[entry] = nil

	intersect := func(x, y *Block) *Block {
		for x != y {
			for d.rpoNum[x] > d.rpoNum[y] {
				x = d.idom[x]
			}
			for d.rpoNum[y] > d.rpoNum[x] {
				y = d.idom[y]
			}
		}
		return x
	}

	changed := true
	for changed {
		changed = false
		for _, b := range d.rpo[1:] {
			var newIDom *Block
			for _, p := range predsOf(b) {
				if _, ok := d.idom[p]; !ok {
					continue
				}
				if newIDom == nil {
					newIDom = p
				} else {
					newIDom = intersect(p, newIDom)
				}
			}
			if newIDom != nil && d.idom[b] != newIDom {
				d.idom[b] = newIDom
				d.dom[b] = newIDom
				changed = true
			}
		}
	}
	return d
}

// reversePostOrder computes an RPO listing reachable from the entry.
func reversePostOrder(f *Func) []*Block {
	visited := map[*Block]int{} // 0 unseen, 1 on-stack, 2 done
	var post []*Block
	var dfs func(b *Block)
	dfs = func(b *Block) {
		if visited[b] != 0 {
			return
		}
		visited[b] = 1
		for _, s := range b.Succs() {
			dfs(s)
		}
		visited[b] = 2
		post = append(post, b)
	}
	dfs(f.Entry())
	rpo := make([]*Block, 0, len(post))
	for i := len(post) - 1; i >= 0; i-- {
		rpo = append(rpo, post[i])
	}
	return rpo
}

// Preds returns the predecessor blocks of b.
func Preds(b *Block) []*Block { return predsOf(b) }
