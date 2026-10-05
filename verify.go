package blockgc

// Verify independently traverses the store from every currently active root
// (published roots plus all reader pins) and returns any reachable block
// that appears in the quarantine set.
//
// It is deliberately a separate traversal from the collector's mark phase:
// it reads the manifest fresh, walks the graph breadth-first, and treats
// quarantined blocks as still present (they are, physically). A non-empty
// result means the sweep picked blocks that are still reachable and the
// deletion must not be confirmed.
func Verify(s *Store, m *Manifest, quarantine map[BlockID]bool) []BlockID {
	seen := make(map[BlockID]bool)
	var bad []BlockID
	queue := m.ActiveHeads()
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		if quarantine[id] {
			bad = append(bad, id)
		}
		b, ok := s.Get(id)
		if !ok {
			continue // dangling reference: nothing to check
		}
		queue = append(queue, b.Refs...)
	}
	return bad
}
