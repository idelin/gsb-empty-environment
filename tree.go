package pageindex

// split carries the median key/value and right sibling page produced when a
// node overflows during insertion.
type split struct {
	key   uint64
	val   uint64
	right PageNo
}

// cow performs one copy-on-write commit. Every node it touches is cloned onto
// a freshly allocated page; the old page numbers are collected in retired and
// handed to the pager's epoch-gated reclaimer by the caller.
type cow struct {
	p       *Pager
	t       int // minimum degree: nodes hold between t-1 and 2t-1 keys
	retired []PageNo
}

func (c *cow) maxKeys() int { return 2*c.t - 1 }

func (c *cow) retire(no PageNo) { c.retired = append(c.retired, no) }

// insert returns the page of the new root after inserting or replacing key.
func (c *cow) insert(root PageNo, key, val uint64) PageNo {
	newRoot, sp := c.insertRec(root, key, val)
	if sp == nil {
		return newRoot
	}
	return c.p.alloc(&node{
		keys: []uint64{sp.key},
		vals: []uint64{sp.val},
		kids: []PageNo{newRoot, sp.right},
	})
}

func (c *cow) insertRec(no PageNo, key, val uint64) (PageNo, *split) {
	n := c.p.get(no)
	if n.leaf {
		nn := &node{leaf: true}
		nn.keys = append(nn.keys, n.keys...)
		nn.vals = append(nn.vals, n.vals...)
		i := nn.find(key)
		if i < len(nn.keys) && nn.keys[i] == key {
			nn.vals[i] = val
		} else {
			nn.keys = insertUint64(nn.keys, i, key)
			nn.vals = insertUint64(nn.vals, i, val)
		}
		c.retire(no)
		if len(nn.keys) <= c.maxKeys() {
			return c.p.alloc(nn), nil
		}
		return c.splitNode(nn)
	}

	i := n.find(key)
	nn := n.clone()
	if i < len(n.keys) && n.keys[i] == key {
		nn.vals[i] = val
		c.retire(no)
		return c.p.alloc(nn), nil
	}
	childNew, sp := c.insertRec(n.kids[i], key, val)
	nn.kids[i] = childNew
	if sp != nil {
		nn.keys = insertUint64(nn.keys, i, sp.key)
		nn.vals = insertUint64(nn.vals, i, sp.val)
		nn.kids = insertPage(nn.kids, i+1, sp.right)
	}
	c.retire(no)
	if len(nn.keys) <= c.maxKeys() {
		return c.p.alloc(nn), nil
	}
	return c.splitNode(nn)
}

// splitNode splits an overflowing node (2t keys) around its median.
func (c *cow) splitNode(nn *node) (PageNo, *split) {
	mid := c.t
	l := &node{leaf: nn.leaf, keys: nn.keys[:mid], vals: nn.vals[:mid]}
	r := &node{leaf: nn.leaf, keys: nn.keys[mid+1:], vals: nn.vals[mid+1:]}
	if !nn.leaf {
		l.kids = nn.kids[:mid+1]
		r.kids = nn.kids[mid+1:]
	}
	lno := c.p.alloc(l)
	rno := c.p.alloc(r)
	return lno, &split{key: nn.keys[mid], val: nn.vals[mid], right: rno}
}

// delete returns the page of the new root after removing key, which must
// exist. If the root shrinks to an empty internal node it is replaced by its
// only child.
func (c *cow) delete(root PageNo, key uint64) PageNo {
	newRoot := c.deleteRec(root, key)
	for {
		n := c.p.get(newRoot)
		if n.leaf || len(n.keys) > 0 {
			return newRoot
		}
		c.retire(newRoot)
		newRoot = n.kids[0]
	}
}

func (c *cow) deleteRec(no PageNo, key uint64) PageNo {
	n := c.p.get(no)
	i := n.find(key)
	if n.leaf {
		if i >= len(n.keys) || n.keys[i] != key {
			return no // not present: subtree unchanged
		}
		nn := &node{leaf: true}
		nn.keys = append(nn.keys, n.keys[:i]...)
		nn.keys = append(nn.keys, n.keys[i+1:]...)
		nn.vals = append(nn.vals, n.vals[:i]...)
		nn.vals = append(nn.vals, n.vals[i+1:]...)
		c.retire(no)
		return c.p.alloc(nn)
	}
	if i < len(n.keys) && n.keys[i] == key {
		return c.deleteInternalKey(no, n, i)
	}
	// Descend, first ensuring the child has at least t keys so the
	// recursion never enters a node that could underflow.
	nn := n.clone()
	childIdx := i
	if child := c.p.get(nn.kids[childIdx]); len(child.keys) == c.t-1 {
		nn, childIdx = c.fill(nn, childIdx)
	}
	newChild := c.deleteRec(nn.kids[childIdx], key)
	nn.kids[childIdx] = newChild
	c.retire(no)
	return c.p.alloc(nn)
}

// deleteInternalKey removes n.keys[i] from internal node n.
func (c *cow) deleteInternalKey(no PageNo, n *node, i int) PageNo {
	key := n.keys[i]
	if left := c.p.get(n.kids[i]); len(left.keys) >= c.t {
		pk, pv := c.maxKV(n.kids[i])
		newChild := c.deleteRec(n.kids[i], pk)
		nn := n.clone()
		nn.keys[i], nn.vals[i] = pk, pv
		nn.kids[i] = newChild
		c.retire(no)
		return c.p.alloc(nn)
	}
	if right := c.p.get(n.kids[i+1]); len(right.keys) >= c.t {
		sk, sv := c.minKV(n.kids[i+1])
		newChild := c.deleteRec(n.kids[i+1], sk)
		nn := n.clone()
		nn.keys[i], nn.vals[i] = sk, sv
		nn.kids[i+1] = newChild
		c.retire(no)
		return c.p.alloc(nn)
	}
	// Both neighbours are minimal: merge them with the key, then delete.
	nn := n.clone()
	c.mergeKids(nn, i)
	newChild := c.deleteRec(nn.kids[i], key)
	nn.kids[i] = newChild
	c.retire(no)
	return c.p.alloc(nn)
}

// fill ensures nn.kids[i] has at least t keys by rotating from a sibling or
// merging, and returns the index to descend into.
func (c *cow) fill(nn *node, i int) (*node, int) {
	if i > 0 {
		if left := c.p.get(nn.kids[i-1]); len(left.keys) >= c.t {
			c.rotateRight(nn, i)
			return nn, i
		}
	}
	if i < len(nn.kids)-1 {
		if right := c.p.get(nn.kids[i+1]); len(right.keys) >= c.t {
			c.rotateLeft(nn, i)
			return nn, i
		}
	}
	if i > 0 {
		c.mergeKids(nn, i-1)
		return nn, i - 1
	}
	c.mergeKids(nn, i)
	return nn, i
}

// rotateRight moves nn.keys[i-1] down into kids[i] and the left sibling's
// last key up.
func (c *cow) rotateRight(nn *node, i int) {
	child := c.p.get(nn.kids[i])
	left := c.p.get(nn.kids[i-1])
	nc := &node{leaf: child.leaf}
	nc.keys = append([]uint64{nn.keys[i-1]}, child.keys...)
	nc.vals = append([]uint64{nn.vals[i-1]}, child.vals...)
	if !child.leaf {
		nc.kids = append([]PageNo{left.kids[len(left.kids)-1]}, child.kids...)
	}
	nl := &node{leaf: left.leaf}
	nl.keys = append(nl.keys, left.keys[:len(left.keys)-1]...)
	nl.vals = append(nl.vals, left.vals[:len(left.vals)-1]...)
	if !left.leaf {
		nl.kids = append(nl.kids, left.kids[:len(left.kids)-1]...)
	}
	nn.keys[i-1] = left.keys[len(left.keys)-1]
	nn.vals[i-1] = left.vals[len(left.vals)-1]
	c.retire(nn.kids[i])
	c.retire(nn.kids[i-1])
	nn.kids[i] = c.p.alloc(nc)
	nn.kids[i-1] = c.p.alloc(nl)
}

// rotateLeft moves nn.keys[i] down into kids[i] and the right sibling's
// first key up.
func (c *cow) rotateLeft(nn *node, i int) {
	child := c.p.get(nn.kids[i])
	right := c.p.get(nn.kids[i+1])
	nc := &node{leaf: child.leaf}
	nc.keys = append(append([]uint64{}, child.keys...), nn.keys[i])
	nc.vals = append(append([]uint64{}, child.vals...), nn.vals[i])
	if !child.leaf {
		nc.kids = append(append([]PageNo{}, child.kids...), right.kids[0])
	}
	nr := &node{leaf: right.leaf}
	nr.keys = append(nr.keys, right.keys[1:]...)
	nr.vals = append(nr.vals, right.vals[1:]...)
	if !right.leaf {
		nr.kids = append(nr.kids, right.kids[1:]...)
	}
	nn.keys[i] = right.keys[0]
	nn.vals[i] = right.vals[0]
	c.retire(nn.kids[i])
	c.retire(nn.kids[i+1])
	nn.kids[i] = c.p.alloc(nc)
	nn.kids[i+1] = c.p.alloc(nr)
}

// mergeKids merges nn.kids[i], nn.keys[i] and nn.kids[i+1] into one node.
func (c *cow) mergeKids(nn *node, i int) {
	l := c.p.get(nn.kids[i])
	r := c.p.get(nn.kids[i+1])
	m := &node{leaf: l.leaf}
	m.keys = append(append(append([]uint64{}, l.keys...), nn.keys[i]), r.keys...)
	m.vals = append(append(append([]uint64{}, l.vals...), nn.vals[i]), r.vals...)
	if !l.leaf {
		m.kids = append(append([]PageNo{}, l.kids...), r.kids...)
	}
	c.retire(nn.kids[i])
	c.retire(nn.kids[i+1])
	mno := c.p.alloc(m)
	nn.keys = removeUint64At(nn.keys, i)
	nn.vals = removeUint64At(nn.vals, i)
	nn.kids[i] = mno
	nn.kids = removePageAt(nn.kids, i+1)
}

// maxKV returns the rightmost key/value in the subtree.
func (c *cow) maxKV(no PageNo) (uint64, uint64) {
	n := c.p.get(no)
	for !n.leaf {
		n = c.p.get(n.kids[len(n.kids)-1])
	}
	return n.keys[len(n.keys)-1], n.vals[len(n.vals)-1]
}

// minKV returns the leftmost key/value in the subtree.
func (c *cow) minKV(no PageNo) (uint64, uint64) {
	n := c.p.get(no)
	for !n.leaf {
		n = c.p.get(n.kids[0])
	}
	return n.keys[0], n.vals[0]
}
