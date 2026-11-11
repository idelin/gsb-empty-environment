package canon

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// AtomRef identifies one concrete fresh atom as produced by a model run.
type AtomRef struct {
	Session string
	UID     string
}

// CanonicalAtom is the stable name of a fresh atom inside its generating
// session. Two atoms of different sessions that share an Index stay distinct
// because their Session differs.
type CanonicalAtom struct {
	Session string
	Index   int
}

// Mapping is one entry of the renaming permutation.
type Mapping struct {
	Original  AtomRef
	Canonical CanonicalAtom
}

// Certificate witnesses the canonicalization.
//
// Mappings is a bijection over the fresh atoms appearing in the input state:
// every concrete (session, UID) pair occurs exactly once as a source and
// every (session, index) pair exactly once as a target, and permutations never
// cross session boundaries.
type Certificate struct {
	Mappings []Mapping

	forward map[AtomRef]CanonicalAtom
	reverse map[CanonicalAtom]AtomRef
}

// CanonicalName reports the canonical replacement of a concrete fresh atom.
func (c *Certificate) CanonicalName(ref AtomRef) (CanonicalAtom, bool) {
	if c == nil {
		return CanonicalAtom{}, false
	}
	name, ok := c.forward[ref]
	return name, ok
}

// IsBijection checks the certificate invariants: unique sources, unique
// targets, session-preserving permutation and non-negative dense indices.
func (c *Certificate) IsBijection() bool {
	if c == nil {
		return false
	}
	sources := make(map[AtomRef]bool, len(c.Mappings))
	targets := make(map[CanonicalAtom]bool, len(c.Mappings))
	seenIndex := make(map[string]map[int]bool)
	for _, m := range c.Mappings {
		if m.Original.Session != m.Canonical.Session {
			return false
		}
		if sources[m.Original] || targets[m.Canonical] {
			return false
		}
		if m.Canonical.Index < 0 {
			return false
		}
		session := m.Canonical.Session
		if seenIndex[session] == nil {
			seenIndex[session] = map[int]bool{}
		}
		seenIndex[session][m.Canonical.Index] = true
		sources[m.Original] = true
		targets[m.Canonical] = true
	}
	for _, indices := range seenIndex {
		for i := 0; i < len(indices); i++ {
			if !indices[i] {
				return false
			}
		}
	}
	return true
}

// RenameTerm applies the certificate to a single term. Fresh atoms absent from
// the certificate are left untouched; constants and function names are never
// changed.
func (c *Certificate) RenameTerm(t Term) Term {
	return c.renameTerm(t, c.forwardLookup)
}

// RenameState applies the certificate to every term of s (queue, role
// variables and attacker knowledge) and returns a fresh deep copy.
func (c *Certificate) RenameState(s *State) *State {
	out := s.clone()
	if out == nil {
		return nil
	}
	for i := range out.Queue {
		out.Queue[i].Payload = c.RenameTerm(out.Queue[i].Payload)
	}
	for i := range out.Roles {
		for k, v := range out.Roles[i].Vars {
			out.Roles[i].Vars[k] = c.RenameTerm(v)
		}
	}
	for i, term := range out.AttackerKnowledge {
		out.AttackerKnowledge[i] = c.RenameTerm(term)
	}
	return out
}

// ReverseRenameTerm applies the inverse permutation to a canonical term,
// reconstructing the original concrete atom names.
func (c *Certificate) ReverseRenameTerm(t Term) Term {
	return c.renameTerm(t, func(atom Fresh) (Fresh, bool) {
		ref, ok := c.reverse[CanonicalAtom{Session: atom.Session, Index: parseCanonicalIndex(atom.UID)}]
		if !ok {
			return Fresh{}, false
		}
		return Fresh{Session: ref.Session, UID: ref.UID}, true
	})
}

// ReverseRenameState applies the inverse permutation to a canonical state.
func (c *Certificate) ReverseRenameState(s *State) *State {
	out := s.clone()
	if out == nil {
		return nil
	}
	for i := range out.Queue {
		out.Queue[i].Payload = c.ReverseRenameTerm(out.Queue[i].Payload)
	}
	for i := range out.Roles {
		for k, v := range out.Roles[i].Vars {
			out.Roles[i].Vars[k] = c.ReverseRenameTerm(v)
		}
	}
	for i, term := range out.AttackerKnowledge {
		out.AttackerKnowledge[i] = c.ReverseRenameTerm(term)
	}
	return out
}

func (c *Certificate) renameTerm(t Term, lookup func(Fresh) (Fresh, bool)) Term {
	switch x := t.(type) {
	case nil:
		return nil
	case Const:
		return Const{Name: x.Name}
	case Fresh:
		replacement, ok := lookup(x)
		if !ok {
			return Fresh{Session: x.Session, UID: x.UID}
		}
		return replacement
	case Tuple:
		items := make([]Term, len(x.Items))
		for i, item := range x.Items {
			items[i] = c.renameTerm(item, lookup)
		}
		return Tuple{Items: items}
	case SymEnc:
		return SymEnc{
			Plaintext: c.renameTerm(x.Plaintext, lookup),
			Key:       c.renameTerm(x.Key, lookup),
		}
	case PKEEnc:
		return PKEEnc{
			Plaintext: c.renameTerm(x.Plaintext, lookup),
			PubKey:    c.renameTerm(x.PubKey, lookup),
		}
	case FuncApp:
		args := make([]Term, len(x.Args))
		for i, arg := range x.Args {
			args[i] = c.renameTerm(arg, lookup)
		}
		return FuncApp{Func: x.Func, Args: args}
	default:
		return nil
	}
}

// forwardLookup maps a concrete fresh atom to its canonical replacement.
func (c *Certificate) forwardLookup(atom Fresh) (Fresh, bool) {
	name, ok := c.forward[AtomRef{Session: atom.Session, UID: atom.UID}]
	if !ok {
		return Fresh{}, false
	}
	return Fresh{Session: name.Session, UID: canonicalUID(name.Index)}, true
}

// canonicalUID renders an index as the UID used inside a canonical Fresh.
func canonicalUID(index int) string {
	return "c" + strconv.Itoa(index)
}

// parseCanonicalIndex is the inverse of canonicalUID used when reversing a
// certificate. Non-canonical UIDs map to -1 and round-trip only through the
// stored reverse map, which always contains the original UID.
func parseCanonicalIndex(uid string) int {
	if len(uid) < 2 || uid[0] != 'c' {
		return -1
	}
	index, err := strconv.Atoi(uid[1:])
	if err != nil {
		return -1
	}
	return index
}

type canonicalizer struct {
	firstSeen map[AtomRef]CanonicalAtom
	counters  map[string]int
	mappings  []Mapping
}

func newCanonicalizer() *canonicalizer {
	return &canonicalizer{
		firstSeen: map[AtomRef]CanonicalAtom{},
		counters:  map[string]int{},
	}
}

// observe records every fresh atom of t in preorder. The equality relation of
// atoms inside one session is exactly the first-occurrence order: the first
// distinct (session, UID) becomes c0, the second c1, and so on.
func (z *canonicalizer) observe(t Term) {
	walk(t, func(node Term) {
		atom, ok := node.(Fresh)
		if !ok {
			return
		}
		ref := AtomRef{Session: atom.Session, UID: atom.UID}
		if _, seen := z.firstSeen[ref]; seen {
			return
		}
		index := z.counters[atom.Session]
		z.counters[atom.Session] = index + 1
		name := CanonicalAtom{Session: atom.Session, Index: index}
		z.firstSeen[ref] = name
		z.mappings = append(z.mappings, Mapping{Original: ref, Canonical: name})
	})
}

// termKey renders a structural, collision-free key used only to order
// set-valued state components deterministically.
func termKey(t Term) string {
	var b strings.Builder
	writeTermKey(&b, t)
	return b.String()
}

func writeTermKey(b *strings.Builder, t Term) {
	switch x := t.(type) {
	case nil:
		b.WriteString("n:0;")
	case Const:
		b.WriteString("k:")
		b.WriteString(strconv.Itoa(len(x.Name)))
		b.WriteByte(':')
		b.WriteString(x.Name)
		b.WriteByte(';')
	case Fresh:
		b.WriteString("f:")
		b.WriteString(strconv.Itoa(len(x.Session)))
		b.WriteByte(':')
		b.WriteString(x.Session)
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(len(x.UID)))
		b.WriteByte(':')
		b.WriteString(x.UID)
		b.WriteByte(';')
	case Tuple:
		b.WriteString("t:")
		b.WriteString(strconv.Itoa(len(x.Items)))
		b.WriteByte(';')
		for _, item := range x.Items {
			writeTermKey(b, item)
		}
	case SymEnc:
		b.WriteString("s;")
		writeTermKey(b, x.Plaintext)
		writeTermKey(b, x.Key)
	case PKEEnc:
		b.WriteString("p;")
		writeTermKey(b, x.Plaintext)
		writeTermKey(b, x.PubKey)
	case FuncApp:
		b.WriteString("a:")
		b.WriteString(strconv.Itoa(len(x.Func)))
		b.WriteByte(':')
		b.WriteString(x.Func)
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(len(x.Args)))
		b.WriteByte(';')
		for _, arg := range x.Args {
			writeTermKey(b, arg)
		}
	default:
		b.WriteString("?:")
		b.WriteString(fmt.Sprintf("%T", t))
		b.WriteByte(';')
	}
}

// sortedRoleOrder returns role indices ordered by (session, role) so that
// canonicalization does not depend on the host's slice layout.
func sortedRoleOrder(roles []RoleState) []int {
	order := make([]int, len(roles))
	for i := range roles {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		ri, rj := roles[order[i]], roles[order[j]]
		if ri.Session != rj.Session {
			return ri.Session < rj.Session
		}
		return ri.Role < rj.Role
	})
	return order
}

func sortedVarKeys(vars map[string]Term) []string {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKnowledgeOrder(knowledge []Term) []int {
	order := make([]int, len(knowledge))
	for i := range knowledge {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		return termKey(knowledge[order[i]]) < termKey(knowledge[order[j]])
	})
	return order
}

// Canonicalize rewrites s into a state in which every fresh atom is named by
// its generating session and a dense per-session index, while public constants
// and function names are preserved verbatim. The input state is never
// mutated. The returned certificate is a verified bijection.
func Canonicalize(s *State) (*State, *Certificate) {
	if s == nil {
		return nil, nil
	}
	z := newCanonicalizer()

	// Fixed traversal order: message queue first (its order is semantically
	// meaningful), then role states in (session, role) order with variables in
	// key order, finally attacker knowledge as a deterministically ordered set.
	for _, msg := range s.Queue {
		z.observe(msg.Payload)
	}
	for _, idx := range sortedRoleOrder(s.Roles) {
		role := s.Roles[idx]
		for _, key := range sortedVarKeys(role.Vars) {
			z.observe(role.Vars[key])
		}
	}
	for _, idx := range sortedKnowledgeOrder(s.AttackerKnowledge) {
		z.observe(s.AttackerKnowledge[idx])
	}

	cert := &Certificate{
		Mappings: z.mappings,
		forward:  z.firstSeen,
		reverse:  make(map[CanonicalAtom]AtomRef, len(z.mappings)),
	}
	for _, m := range z.mappings {
		cert.reverse[m.Canonical] = m.Original
	}
	canonical := cert.RenameState(s)

	// Reorder the canonical output itself: role states and attacker knowledge
	// are set-valued, so equal states must compare equal slice-for-slice
	// regardless of the host's input layout.
	canonical.Roles = reorderRoles(canonical.Roles, sortedRoleOrder(canonical.Roles))
	canonical.AttackerKnowledge = reorderTerms(
		canonical.AttackerKnowledge,
		sortedKnowledgeOrder(canonical.AttackerKnowledge),
	)

	return canonical, cert
}

func reorderTerms(terms []Term, order []int) []Term {
	out := make([]Term, len(terms))
	for newPos, oldPos := range order {
		out[newPos] = terms[oldPos]
	}
	return out
}

func reorderRoles(roles []RoleState, order []int) []RoleState {
	out := make([]RoleState, len(roles))
	for newPos, oldPos := range order {
		out[newPos] = roles[oldPos]
	}
	return out
}
