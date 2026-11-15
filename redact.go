package netsec

// Redact serializes the value graph rooted at root into a deterministic
// byte encoding safe to ship to the log store.
//
// Pass one marks every node reachable from a Secret-tagged node as
// confidential. Pass two serializes only the public subgraph:
// confidential nodes become a fixed placeholder, public sharing and
// cycles are preserved through $id/$ref markers numbered in public
// traversal order, and dict keys are sorted byte-wise.
//
// If the encoding would exceed budget bytes, Redact returns
// ErrBudgetExceeded and no output at all.
func Redact(root *Value, budget int) ([]byte, error) {
	if budget < 0 {
		budget = 0
	}
	e := &encoder{
		tainted: computeTaint(root),
		count:   make(map[*Value]int),
		ids:     make(map[*Value]int),
		emitted: make(map[*Value]bool),
		nextID:  1,
		budget:  budget,
	}
	e.countPublic(root)
	e.emit(root)
	if e.err != nil {
		return nil, e.err
	}
	return e.buf.Bytes(), nil
}
