package netsec

// Equivalent reports whether recursive groups a and b canonicalize to the
// same type: their members admit a one-to-one correspondence preserving the
// final flag, supertype, and the exact field sequence, with intra-group
// references mapped through the correspondence and references to earlier
// types required to name the same absolute type index.
func Equivalent(a, b RecGroup) bool {
	if len(a.Members) != len(b.Members) {
		return false
	}
	m := &matcher{
		a:   a,
		b:   b,
		a2b: make([]int, len(a.Members)),
		b2a: make([]int, len(b.Members)),
	}
	for i := range m.a2b {
		m.a2b[i] = -1
		m.b2a[i] = -1
	}
	return m.search()
}

// matcher performs a backtracking search for an isomorphism between the two
// groups, propagating the constraints implied by intra-group references.
type matcher struct {
	a, b  RecGroup
	a2b   []int // a member -> b member, -1 if unassigned
	b2a   []int // b member -> a member, -1 if unassigned
	trail []int // a-members assigned since the last search checkpoint
}

func (m *matcher) search() bool {
	h := -1
	for i := range m.a2b {
		if m.a2b[i] < 0 {
			h = i
			break
		}
	}
	if h < 0 {
		return true
	}
	mark := len(m.trail)
	for k := range m.b2a {
		if m.b2a[k] >= 0 {
			continue
		}
		if m.pair(h, k) && m.search() {
			return true
		}
		m.undo(mark)
	}
	return false
}

func (m *matcher) undo(mark int) {
	for _, h := range m.trail[mark:] {
		m.b2a[m.a2b[h]] = -1
		m.a2b[h] = -1
	}
	m.trail = m.trail[:mark]
}

// pair assigns a-member h to b-member k and recursively propagates the
// assignments forced by intra-group reference fields. It reports whether
// the assignment is consistent; on failure the partial assignments remain
// on the trail for the caller to undo.
func (m *matcher) pair(h, k int) bool {
	if m.a2b[h] >= 0 || m.b2a[k] >= 0 {
		return false
	}
	sa, sb := m.a.Members[h], m.b.Members[k]
	if !headerEqual(sa, sb) {
		return false
	}
	m.a2b[h] = k
	m.b2a[k] = h
	m.trail = append(m.trail, h)
	for i := range sa.Fields {
		fa, fb := sa.Fields[i].Type, sb.Fields[i].Type
		if fa.Kind != Ref {
			continue
		}
		ta, tb := fa.Ref, fb.Ref
		if m.a.intra(ta) {
			if !m.b.intra(tb) {
				return false
			}
			j, l := ta-m.a.Base, tb-m.b.Base
			switch {
			case m.a2b[j] < 0 && m.b2a[l] < 0:
				if !m.pair(j, l) {
					return false
				}
			case m.a2b[j] != l:
				return false
			}
		} else if m.b.intra(tb) || ta != tb {
			return false
		}
	}
	return true
}

// headerEqual compares two structs on everything except intra-group
// reference targets, which the matcher resolves through the member
// correspondence: final flag, supertype, and the field sequence's kinds,
// order and mutability.
func headerEqual(a, b StructType) bool {
	if a.Final != b.Final || a.Super != b.Super || len(a.Fields) != len(b.Fields) {
		return false
	}
	for i := range a.Fields {
		fa, fb := a.Fields[i], b.Fields[i]
		if fa.Mutable != fb.Mutable || fa.Type.Kind != fb.Type.Kind {
			return false
		}
	}
	return true
}
