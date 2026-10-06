package pageindex

// Put inserts or updates key. Every node on the affected path is copied
// into freshly allocated pages; previously committed roots stay untouched.
func (st *Store) Put(key, val uint64) {
	st.mu.Lock()
	defer st.mu.Unlock()

	if len(st.pages[st.root].keys) == maxKeys {
		nr := &node{leaf: false, kids: []PageID{st.root}}
		st.root = st.splitChild(st.allocNode(nr), 0)
	}
	st.root = st.insertNonfull(st.root, key, val)
	st.version++
	st.gcLocked()
}

// Delete removes key, reporting whether it existed.
func (st *Store) Delete(key uint64) bool {
	st.mu.Lock()
	defer st.mu.Unlock()

	newRoot, found := st.deleteRec(st.root, key)
	// deleteRec may have restructured the path even when the key was
	// absent; the result is an equivalent tree and must be committed so
	// its freshly allocated pages stay reachable.
	// A root that lost its last key is replaced by its only child.
	for rn := st.pages[newRoot]; !rn.leaf && len(rn.keys) == 0; {
		newRoot = rn.kids[0]
		rn = st.pages[newRoot]
	}
	st.root = newRoot
	if found {
		st.version++
	}
	st.gcLocked()
	return found
}

// insertNonfull inserts into a subtree whose root has room, returning the
// new subtree root page. Caller must hold mu.
func (st *Store) insertNonfull(id PageID, key, val uint64) PageID {
	n := st.pages[id]
	i := lowerBound(n.keys, key)

	if i < len(n.keys) && n.keys[i] == key {
		nn := n.shallowCopy()
		nn.vals[i] = val
		return st.allocNode(nn)
	}
	if n.leaf {
		return st.allocNode(&node{
			leaf: true,
			keys: insertUint(n.keys, i, key),
			vals: insertUint(n.vals, i, val),
		})
	}

	cur := id
	curNode := n
	if child := st.pages[curNode.kids[i]]; len(child.keys) == maxKeys {
		cur = st.splitChild(cur, i)
		curNode = st.pages[cur]
		if key == curNode.keys[i] {
			nn := curNode.shallowCopy()
			nn.vals[i] = val
			return st.allocNode(nn)
		}
		if key > curNode.keys[i] {
			i++
		}
	}
	newChild := st.insertNonfull(curNode.kids[i], key, val)
	nn := curNode.shallowCopy()
	nn.kids[i] = newChild
	return st.allocNode(nn)
}

// splitChild splits the full child at index i of parent pid, returning the
// new parent page. Both halves are fresh pages; the old child page is left
// for the garbage collector. Caller must hold mu.
func (st *Store) splitChild(pid PageID, i int) PageID {
	p := st.pages[pid]
	c := st.pages[p.kids[i]]

	left := &node{
		leaf: c.leaf,
		keys: dupUint(c.keys[:minDegree-1]),
		vals: dupUint(c.vals[:minDegree-1]),
	}
	right := &node{
		leaf: c.leaf,
		keys: dupUint(c.keys[minDegree:]),
		vals: dupUint(c.vals[minDegree:]),
	}
	if !c.leaf {
		left.kids = dupPage(c.kids[:minDegree])
		right.kids = dupPage(c.kids[minDegree:])
	}
	lid := st.allocNode(left)
	rid := st.allocNode(right)

	np := p.shallowCopy()
	np.keys = insertUint(np.keys, i, c.keys[minDegree-1])
	np.vals = insertUint(np.vals, i, c.vals[minDegree-1])
	np.kids = removePage(np.kids, i)
	np.kids = insertPage(np.kids, i, rid)
	np.kids = insertPage(np.kids, i, lid)
	return st.allocNode(np)
}

// deleteRec removes key from the subtree rooted at id, returning the new
// subtree root page and whether the key was found. Caller must hold mu.
func (st *Store) deleteRec(id PageID, key uint64) (PageID, bool) {
	n := st.pages[id]
	i := lowerBound(n.keys, key)

	if n.leaf {
		if i < len(n.keys) && n.keys[i] == key {
			return st.allocNode(&node{
				leaf: true,
				keys: removeUint(n.keys, i),
				vals: removeUint(n.vals, i),
			}), true
		}
		return id, false
	}

	if i < len(n.keys) && n.keys[i] == key {
		left := st.pages[n.kids[i]]
		right := st.pages[n.kids[i+1]]
		switch {
		case len(left.keys) >= minDegree:
			pk, pv := st.maxKV(n.kids[i])
			newLeft, _ := st.deleteRec(n.kids[i], pk)
			nn := n.shallowCopy()
			nn.keys[i], nn.vals[i] = pk, pv
			nn.kids[i] = newLeft
			return st.allocNode(nn), true
		case len(right.keys) >= minDegree:
			sk, sv := st.minKV(n.kids[i+1])
			newRight, _ := st.deleteRec(n.kids[i+1], sk)
			nn := n.shallowCopy()
			nn.keys[i], nn.vals[i] = sk, sv
			nn.kids[i+1] = newRight
			return st.allocNode(nn), true
		default:
			mid := st.allocNode(mergeNodes(left, n.keys[i], n.vals[i], right))
			nn := n.shallowCopy()
			nn.keys = removeUint(nn.keys, i)
			nn.vals = removeUint(nn.vals, i)
			nn.kids = removePage(nn.kids, i+1)
			nn.kids[i] = mid
			return st.deleteRec(st.allocNode(nn), key)
		}
	}

	if len(st.pages[n.kids[i]].keys) >= minDegree {
		newChild, found := st.deleteRec(n.kids[i], key)
		if !found {
			return id, false
		}
		nn := n.shallowCopy()
		nn.kids[i] = newChild
		return st.allocNode(nn), true
	}

	np, di := st.ensureChild(n, i)
	newChild, found := st.deleteRec(np.kids[di], key)
	if !found {
		// The restructure is still a valid tree; keep it.
		return st.allocNode(np), false
	}
	np.kids[di] = newChild
	return st.allocNode(np), true
}

// ensureChild restructures a copy of parent n so the child to descend into
// has at least minDegree keys, by borrowing from a sibling or merging.
// Returns the new parent node and the descent index within it.
// Caller must hold mu.
func (st *Store) ensureChild(n *node, i int) (*node, int) {
	child := st.pages[n.kids[i]]

	if i > 0 {
		if sib := st.pages[n.kids[i-1]]; len(sib.keys) >= minDegree {
			nc := &node{
				leaf: child.leaf,
				keys: insertUint(child.keys, 0, n.keys[i-1]),
				vals: insertUint(child.vals, 0, n.vals[i-1]),
			}
			ns := sib.shallowCopy()
			ns.keys = ns.keys[:len(ns.keys)-1]
			ns.vals = ns.vals[:len(ns.vals)-1]
			np := n.shallowCopy()
			np.keys[i-1] = sib.keys[len(sib.keys)-1]
			np.vals[i-1] = sib.vals[len(sib.vals)-1]
			if !child.leaf {
				nc.kids = insertPage(child.kids, 0, sib.kids[len(sib.kids)-1])
				ns.kids = ns.kids[:len(ns.kids)-1]
			}
			np.kids[i] = st.allocNode(nc)
			np.kids[i-1] = st.allocNode(ns)
			return np, i
		}
	}
	if i < len(n.kids)-1 {
		if sib := st.pages[n.kids[i+1]]; len(sib.keys) >= minDegree {
			nc := &node{
				leaf: child.leaf,
				keys: append(dupUint(child.keys), n.keys[i]),
				vals: append(dupUint(child.vals), n.vals[i]),
			}
			ns := sib.shallowCopy()
			ns.keys = ns.keys[1:]
			ns.vals = ns.vals[1:]
			np := n.shallowCopy()
			np.keys[i] = sib.keys[0]
			np.vals[i] = sib.vals[0]
			if !child.leaf {
				nc.kids = append(dupPage(child.kids), sib.kids[0])
				ns.kids = ns.kids[1:]
			}
			np.kids[i] = st.allocNode(nc)
			np.kids[i+1] = st.allocNode(ns)
			return np, i
		}
	}
	if i > 0 {
		sib := st.pages[n.kids[i-1]]
		merged := mergeNodes(sib, n.keys[i-1], n.vals[i-1], child)
		np := n.shallowCopy()
		np.keys = removeUint(np.keys, i-1)
		np.vals = removeUint(np.vals, i-1)
		np.kids = removePage(np.kids, i)
		np.kids[i-1] = st.allocNode(merged)
		return np, i - 1
	}
	sib := st.pages[n.kids[i+1]]
	merged := mergeNodes(child, n.keys[i], n.vals[i], sib)
	np := n.shallowCopy()
	np.keys = removeUint(np.keys, i)
	np.vals = removeUint(np.vals, i)
	np.kids = removePage(np.kids, i+1)
	np.kids[i] = st.allocNode(merged)
	return np, i
}

// mergeNodes concatenates left, the separating entry and right into one
// node with 2*minDegree-1 keys.
func mergeNodes(left *node, key, val uint64, right *node) *node {
	nn := &node{leaf: left.leaf}
	nn.keys = append(append(dupUint(left.keys), key), right.keys...)
	nn.vals = append(append(dupUint(left.vals), val), right.vals...)
	if !left.leaf {
		nn.kids = append(dupPage(left.kids), right.kids...)
	}
	return nn
}

// maxKV returns the rightmost key/value of the subtree. Caller must hold mu.
func (st *Store) maxKV(id PageID) (uint64, uint64) {
	for {
		n := st.pages[id]
		if n.leaf {
			return n.keys[len(n.keys)-1], n.vals[len(n.vals)-1]
		}
		id = n.kids[len(n.kids)-1]
	}
}

// minKV returns the leftmost key/value of the subtree. Caller must hold mu.
func (st *Store) minKV(id PageID) (uint64, uint64) {
	for {
		n := st.pages[id]
		if n.leaf {
			return n.keys[0], n.vals[0]
		}
		id = n.kids[0]
	}
}
