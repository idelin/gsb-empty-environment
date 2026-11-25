// Package pathlang models file-resource permissions as regular languages over
// normalized path components backed by explicit, finite DFAs.
//
// A normalized resource is the word
//
//	<root-or-tenant-alias> ::= component ::= component ::= ...
//
// where the first symbol is a root / tenant-root alias and every later symbol
// is a single normalized path component. Three grant shapes are first-class:
// the directory itself (self), its direct children (exactly one component under
// it) and any strict descendant (one or more components under it). All boolean
// combinations are supported through DFA product construction; containment is
// decided with DFA emptiness rather than by evaluating sample paths.
package pathlang

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// DFA is a complete, deterministic finite automaton over a fixed alphabet.
// Transitions maps each alphabet symbol to a transition table of length
// States, so Transitions[a][s] is the successor of state s on symbol a.
type DFA struct {
	Alphabet    []string         `json:"alphabet"`
	States      int              `json:"states"`
	Start       int              `json:"start"`
	Accepting   []bool           `json:"accepting"`
	Transitions map[string][]int `json:"transitions"`
}

// Normalize validates and canonicalizes a sequence of path components. It
// rejects empty components, "." and ".." so every component is a single
// normalized directory/file name; callers supply the root or tenant-root alias
// separately as the leading symbol.
func Normalize(components []string) ([]string, error) {
	out := make([]string, 0, len(components))
	for _, c := range components {
		if c == "" || c == "." || c == ".." {
			return nil, fmt.Errorf("pathlang: non-normalized component %q", c)
		}
		if strings.ContainsAny(c, `/\`) || strings.ContainsRune(c, 0) {
			return nil, fmt.Errorf("pathlang: component %q contains a separator", c)
		}
		out = append(out, c)
	}
	return out, nil
}

// BuildAlphabet collects the finite alphabet shared by every DFA of a program:
// all root and tenant-root aliases followed by all declared path components.
func BuildAlphabet(roots []string, componentRefs ...[]string) []string {
	seen := map[string]struct{}{}
	var syms []string
	add := func(s string) {
		if s == "" {
			return
		}
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			syms = append(syms, s)
		}
	}
	for _, r := range roots {
		add(r)
	}
	for _, refs := range componentRefs {
		for _, c := range refs {
			add(c)
		}
	}
	sort.Strings(syms)
	return syms
}

func newDFA(alphabet []string, states int, accepting []bool) *DFA {
	if accepting == nil {
		accepting = make([]bool, states)
	}
	d := &DFA{
		Alphabet:    append([]string(nil), alphabet...),
		States:      states,
		Start:       0,
		Accepting:   append([]bool(nil), accepting...),
		Transitions: map[string][]int{},
	}
	dead := states - 1
	for _, a := range alphabet {
		t := make([]int, states)
		for s := range t {
			t[s] = dead
		}
		d.Transitions[a] = t
	}
	return d
}

func indexOf(syms []string, want string) int {
	for i, s := range syms {
		if s == want {
			return i
		}
	}
	return -1
}

// BuildSelf accepts exactly the word [root, components...], i.e. the resource
// itself. componentAlphabet is accepted for signature symmetry but only roots
// and the referenced components need to live in alphabet.
func BuildSelf(alphabet, componentAlphabet []string, root string, components []string) (*DFA, error) {
	if indexOf(alphabet, root) < 0 {
		return nil, fmt.Errorf("pathlang: root %q missing from alphabet", root)
	}
	norm, err := Normalize(components)
	if err != nil {
		return nil, err
	}
	word := append([]string{root}, norm...)
	// States 0..len(word) track the prefix and state len(word) accepts the
	// full word; the final state (states-1) is a distinct dead state.
	states := len(word) + 2
	acc := make([]bool, states)
	acc[len(word)] = true
	d := newDFA(alphabet, states, acc)
	for i, sym := range word {
		d.Transitions[sym][i] = i + 1
	}
	for _, a := range alphabet {
		d.Transitions[a][len(word)] = states - 1
	}
	return d, nil
}

// BuildChild accepts [root, components..., c] for any single component c drawn
// from componentAlphabet: exactly the direct children of the resource. Root
// and alias symbols are never treated as component names, while a component
// name that also occurs in the directory prefix remains a valid child name.
func BuildChild(alphabet, componentAlphabet []string, root string, components []string) (*DFA, error) {
	if indexOf(alphabet, root) < 0 {
		return nil, fmt.Errorf("pathlang: root %q missing from alphabet", root)
	}
	norm, err := Normalize(components)
	if err != nil {
		return nil, err
	}
	word := append([]string{root}, norm...)
	// States 0..len(word) walk the directory prefix; state len(word)+1 is the
	// accepting state reached after exactly one child component; the state
	// after it (states-1) is the distinct dead state.
	accept := len(word) + 1
	states := accept + 2
	acc := make([]bool, states)
	acc[accept] = true
	d := newDFA(alphabet, states, acc)
	for i, sym := range word {
		d.Transitions[sym][i] = i + 1
	}
	for _, a := range componentAlphabet {
		if indexOf(alphabet, a) < 0 {
			return nil, fmt.Errorf("pathlang: component %q missing from alphabet", a)
		}
		d.Transitions[a][len(word)] = accept
	}
	for _, a := range alphabet {
		d.Transitions[a][accept] = states - 1
	}
	return d, nil
}

// BuildDesc accepts [root, components..., c1, ..., ck] for k >= 1 with every
// ci drawn from componentAlphabet: any strict descendant of the resource.
func BuildDesc(alphabet, componentAlphabet []string, root string, components []string) (*DFA, error) {
	if indexOf(alphabet, root) < 0 {
		return nil, fmt.Errorf("pathlang: root %q missing from alphabet", root)
	}
	norm, err := Normalize(components)
	if err != nil {
		return nil, err
	}
	word := append([]string{root}, norm...)
	// States 0..len(word)-1 walk the directory prefix. State len(word) is the
	// "directory reached, no descendant yet" state (non-accepting). On any
	// component symbol it moves to state len(word)+1, the accepting descendant
	// loop. The final state (states-1) is a distinct dead state.
	dir := len(word)
	loop := dir + 1
	states := loop + 2
	acc := make([]bool, states)
	acc[loop] = true
	d := newDFA(alphabet, states, acc)
	for i, sym := range word {
		d.Transitions[sym][i] = i + 1
	}
	for _, a := range componentAlphabet {
		if indexOf(alphabet, a) < 0 {
			return nil, fmt.Errorf("pathlang: component %q missing from alphabet", a)
		}
		d.Transitions[a][dir] = loop
		d.Transitions[a][loop] = loop
	}
	for _, a := range alphabet {
		if indexOf(componentAlphabet, a) < 0 {
			d.Transitions[a][dir] = states - 1
			d.Transitions[a][loop] = states - 1
		}
	}
	return d, nil
}

// Empty accepts no words.
func Empty(alphabet []string) *DFA {
	return newDFA(alphabet, 1, []bool{false})
}

// Universe accepts every word over the alphabet.
func Universe(alphabet []string) *DFA {
	d := newDFA(alphabet, 1, []bool{true})
	for _, a := range alphabet {
		d.Transitions[a][0] = 0
	}
	return d
}

func sameAlphabet(a, b *DFA) error {
	if len(a.Alphabet) != len(b.Alphabet) {
		return errors.New("pathlang: alphabets differ")
	}
	for i := range a.Alphabet {
		if a.Alphabet[i] != b.Alphabet[i] {
			return errors.New("pathlang: alphabets differ")
		}
	}
	return nil
}

func product(a, b *DFA, accept func(x, y bool) bool) *DFA {
	if err := sameAlphabet(a, b); err != nil {
		panic(err)
	}
	n := a.States * b.States
	acc := make([]bool, n)
	for i := 0; i < a.States; i++ {
		for j := 0; j < b.States; j++ {
			acc[i*b.States+j] = accept(a.Accepting[i], b.Accepting[j])
		}
	}
	d := newDFA(a.Alphabet, n, acc)
	for _, sym := range a.Alphabet {
		ta := a.Transitions[sym]
		tb := b.Transitions[sym]
		t := d.Transitions[sym]
		for i := 0; i < a.States; i++ {
			for j := 0; j < b.States; j++ {
				t[i*b.States+j] = ta[i]*b.States + tb[j]
			}
		}
	}
	d.Start = a.Start*b.States + b.Start
	return d
}

// Intersect returns the DFA for L(a) ∩ L(b).
func Intersect(a, b *DFA) *DFA {
	return product(a, b, func(x, y bool) bool { return x && y })
}

// Union returns the DFA for L(a) ∪ L(b).
func Union(a, b *DFA) *DFA {
	return product(a, b, func(x, y bool) bool { return x || y })
}

// Complement returns the alphabet-relative complement of L(d).
func Complement(d *DFA) *DFA {
	out := newDFA(d.Alphabet, d.States, nil)
	out.Start = d.Start
	for i, v := range d.Accepting {
		out.Accepting[i] = !v
	}
	for sym, t := range d.Transitions {
		copy(out.Transitions[sym], t)
	}
	return out
}

// IsEmpty reports whether the DFA accepts any word.
func IsEmpty(d *DFA) bool {
	seen := make([]bool, d.States)
	stack := []int{d.Start}
	seen[d.Start] = true
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if d.Accepting[s] {
			return false
		}
		for _, sym := range d.Alphabet {
			ns := d.Transitions[sym][s]
			if !seen[ns] {
				seen[ns] = true
				stack = append(stack, ns)
			}
		}
	}
	return true
}

// Subset reports whether L(a) is contained in L(b) over the shared alphabet.
func Subset(a, b *DFA) bool {
	if err := sameAlphabet(a, b); err != nil {
		panic(err)
	}
	return IsEmpty(Intersect(a, Complement(b)))
}

// Equal reports language equality over the shared alphabet.
func Equal(a, b *DFA) bool { return Subset(a, b) && Subset(b, a) }

// Minimize applies partition refinement (Moore) and removes unreachable
// states, returning an equivalent canonical DFA.
func Minimize(d *DFA) *DFA {
	reachable := make([]int, 0, d.States)
	mark := make([]int, d.States)
	for i := range mark {
		mark[i] = -1
	}
	queue := []int{d.Start}
	mark[d.Start] = 0
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		reachable = append(reachable, s)
		for _, sym := range d.Alphabet {
			ns := d.Transitions[sym][s]
			if mark[ns] < 0 {
				mark[ns] = 0
				queue = append(queue, ns)
			}
		}
	}
	class := make([]int, d.States)
	for s := range class {
		if mark[s] < 0 {
			class[s] = -1
			continue
		}
		if d.Accepting[s] {
			class[s] = 1
		}
	}
	changed := true
	for changed {
		changed = false
		sig := map[string]int{}
		next := make([]int, d.States)
		for i := range next {
			next[i] = -1
		}
		count := 0
		for _, s := range reachable {
			var b strings.Builder
			fmt.Fprintf(&b, "%d|", class[s])
			for _, sym := range d.Alphabet {
				fmt.Fprintf(&b, "%d,", class[d.Transitions[sym][s]])
			}
			c, ok := sig[b.String()]
			if !ok {
				c = count
				sig[b.String()] = count
				count++
			}
			next[s] = c
		}
		for _, s := range reachable {
			if next[s] != class[s] {
				changed = true
				break
			}
		}
		class = next
	}
	n := 0
	for _, s := range reachable {
		if class[s] >= n {
			n = class[s] + 1
		}
	}
	acc := make([]bool, n)
	for _, s := range reachable {
		acc[class[s]] = d.Accepting[s]
	}
	out := newDFA(d.Alphabet, n, acc)
	for _, s := range reachable {
		for _, sym := range d.Alphabet {
			out.Transitions[sym][class[s]] = class[d.Transitions[sym][s]]
		}
	}
	out.Start = class[d.Start]
	return out
}

// Accepts is a convenience membership test for a fully normalized word whose
// leading symbol is a root/alias. Analysis decisions never rely on this alone.
func (d *DFA) Accepts(word []string) bool {
	s := d.Start
	for _, sym := range word {
		t, ok := d.Transitions[sym]
		if !ok {
			return false
		}
		s = t[s]
	}
	return d.Accepting[s]
}
