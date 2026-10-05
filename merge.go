package checkpoint

import "sort"

// mergeParents computes the merged reference state of two parents using
// a three-way merge against their merge base. It returns a
// *MergeConflictError if both sides changed the same reference in
// incompatible ways.
func (s *Store) mergeParents(a, b string) (map[string]Hash, error) {
	aState, err := s.Expand(a)
	if err != nil {
		return nil, err
	}
	bState, err := s.Expand(b)
	if err != nil {
		return nil, err
	}
	base := s.baseState(s.mergeBases(a, b))
	return mergeStates(aState, bState, base)
}

// mergeStates reconciles two states against their common base state.
// A reference changed on only one side takes that side's value; a
// reference changed on both sides to different outcomes conflicts.
func mergeStates(a, b, base map[string]Hash) (map[string]Hash, error) {
	names := make(map[string]bool, len(a)+len(b))
	for n := range a {
		names[n] = true
	}
	for n := range b {
		names[n] = true
	}
	out := make(map[string]Hash, len(names))
	var conflicts []string
	for n := range names {
		av, aOK := a[n]
		bv, bOK := b[n]
		switch {
		case aOK == bOK && (!aOK || av == bv):
			// Both sides agree.
			if aOK {
				out[n] = av
			}
		case sameRef(a, base, n):
			// Only b changed it.
			if bOK {
				out[n] = bv
			}
		case sameRef(b, base, n):
			// Only a changed it.
			if aOK {
				out[n] = av
			}
		default:
			conflicts = append(conflicts, n)
		}
	}
	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return nil, &MergeConflictError{Names: conflicts}
	}
	return out, nil
}

// sameRef reports whether state[name] equals base[name] (presence and value).
func sameRef(state, base map[string]Hash, name string) bool {
	v, ok := state[name]
	bv, bOK := base[name]
	return ok == bOK && (!ok || v == bv)
}

// ancestors returns the IDs of every ancestor of id, including id itself.
func (s *Store) ancestors(id string) map[string]bool {
	out := make(map[string]bool)
	stack := []string{id}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if out[cur] {
			continue
		}
		out[cur] = true
		if sn, ok := s.snaps[cur]; ok {
			stack = append(stack, sn.Parents...)
		}
	}
	return out
}

// mergeBases returns the maximal common ancestors of a and b: common
// ancestors that are not themselves ancestors of another common ancestor.
func (s *Store) mergeBases(a, b string) []string {
	ancA := s.ancestors(a)
	ancB := s.ancestors(b)
	var common []string
	for id := range ancA {
		if ancB[id] {
			common = append(common, id)
		}
	}
	isAnc := make(map[string]map[string]bool, len(common))
	var bases []string
	for _, c := range common {
		maximal := true
		for _, other := range common {
			if c == other {
				continue
			}
			anc, ok := isAnc[other]
			if !ok {
				anc = s.ancestors(other)
				isAnc[other] = anc
			}
			if anc[c] {
				maximal = false
				break
			}
		}
		if maximal {
			bases = append(bases, c)
		}
	}
	sort.Strings(bases)
	return bases
}

// baseState computes the reference state of the merge base. With several
// maximal bases (criss-cross history) their states are unioned; on
// disagreement the lexicographically smaller chunk hash wins, which keeps
// the result deterministic.
func (s *Store) baseState(bases []string) map[string]Hash {
	out := make(map[string]Hash)
	for _, id := range bases {
		st, err := s.Expand(id)
		if err != nil {
			continue
		}
		for n, c := range st {
			if cur, ok := out[n]; !ok || lessHash(c, cur) {
				out[n] = c
			}
		}
	}
	return out
}
