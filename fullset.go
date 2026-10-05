package checkpoint

import "fmt"

// FullModel is the reference "full-set version" model used to audit the
// incremental Store. Every version here stores its complete reference
// set directly — no delta replay — so cross-checking Store.Expand
// against FullModel.Full validates the incremental machinery.
type FullModel struct {
	parents map[string][]string
	full    map[string]map[string]Hash
}

// NewFullModel returns an empty full-set model.
func NewFullModel() *FullModel {
	return &FullModel{
		parents: make(map[string][]string),
		full:    make(map[string]map[string]Hash),
	}
}

// Commit mirrors Store.Commit with full-set semantics. It returns a
// *MergeConflictError under the same conditions as the Store.
func (m *FullModel) Commit(id string, parents []string, ops []Op) error {
	if _, ok := m.full[id]; ok {
		return ErrDuplicateSnapshot
	}
	var state map[string]Hash
	switch len(parents) {
	case 0:
		state = make(map[string]Hash)
	case 1:
		p, ok := m.full[parents[0]]
		if !ok {
			return fmt.Errorf("%v: %q", ErrMissingParent, parents[0])
		}
		state = copyState(p)
	case 2:
		a, okA := m.full[parents[0]]
		b, okB := m.full[parents[1]]
		if !okA || !okB {
			return fmt.Errorf("%v", ErrMissingParent)
		}
		merged, err := mergeStates(a, b, m.baseState(parents[0], parents[1]))
		if err != nil {
			return err
		}
		state = merged
	default:
		return fmt.Errorf("checkpoint: at most two parents supported")
	}
	for _, op := range ops {
		if op.Kind == OpAdd {
			state[op.Name] = op.Chunk
		} else {
			if _, ok := state[op.Name]; !ok {
				return fmt.Errorf("%v: %q", ErrUnknownRef, op.Name)
			}
			delete(state, op.Name)
		}
	}
	m.parents[id] = append([]string(nil), parents...)
	m.full[id] = state
	return nil
}

// Full returns the full reference set of a version.
func (m *FullModel) Full(id string) (map[string]Hash, bool) {
	st, ok := m.full[id]
	if !ok {
		return nil, false
	}
	return copyState(st), true
}

func (m *FullModel) ancestors(id string) map[string]bool {
	out := make(map[string]bool)
	stack := []string{id}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if out[cur] {
			continue
		}
		out[cur] = true
		stack = append(stack, m.parents[cur]...)
	}
	return out
}

// baseState computes the full-set merge base state of two versions,
// using the same maximal-common-ancestor rule as the Store.
func (m *FullModel) baseState(a, b string) map[string]Hash {
	ancA, ancB := m.ancestors(a), m.ancestors(b)
	var common []string
	for id := range ancA {
		if ancB[id] {
			common = append(common, id)
		}
	}
	var bases []string
	for _, c := range common {
		maximal := true
		for _, other := range common {
			if c != other && m.ancestors(other)[c] {
				maximal = false
				break
			}
		}
		if maximal {
			bases = append(bases, c)
		}
	}
	out := make(map[string]Hash)
	for _, id := range bases {
		for n, c := range m.full[id] {
			if cur, ok := out[n]; !ok || lessHash(c, cur) {
				out[n] = c
			}
		}
	}
	return out
}
