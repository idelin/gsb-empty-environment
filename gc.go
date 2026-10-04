package bptree

// Collect reclaims pages that are unreachable from every active root (the
// current root plus the roots of all open snapshots). Reachability follows
// internal child pointers from each active root.
//
// Collect blocks while a write transaction is open, so pages allocated by an
// uncommitted transaction are never freed from under it.
func (t *Tree) Collect() (int, error) {
	t.txMu.Lock()
	defer t.txMu.Unlock()

	t.mu.RLock()
	roots := make([]PageID, 0, len(t.activeRoots))
	for r := range t.activeRoots {
		roots = append(roots, r)
	}
	t.mu.RUnlock()

	marked := make(map[PageID]bool)
	stack := append([]PageID(nil), roots...)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id == 0 || marked[id] {
			continue
		}
		marked[id] = true
		buf, err := t.store.Read(id)
		if err != nil {
			return 0, err
		}
		n, err := decodeNode(buf)
		if err != nil {
			return 0, err
		}
		if !n.leaf {
			stack = append(stack, n.children...)
		}
	}

	freed := 0
	for _, id := range t.store.Allocated() {
		if !marked[id] {
			if err := t.store.Free(id); err != nil {
				return freed, err
			}
			freed++
		}
	}
	return freed, nil
}
