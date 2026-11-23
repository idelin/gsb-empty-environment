package envguard

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// DFA is a finite automaton over runes.
//
// It is constructed lazily from a Thompson NFA: reachable NFA subsets are
// memoized on demand, so compiling a pattern never enumerates the rune
// universe. Membership is a full-match test: the whole sequence must leave
// the machine in an accepting state. Condition expressions may only ask
// whether an input belongs to one of these languages.
type DFA struct {
	nfa    []nfaState
	start  int
	accept int

	subsetList [][]int
	subsetMap  map[string]int
	trans      map[dfaKey]int
}

type dfaKey struct {
	state int
	r     rune
}

type nfaState struct {
	eps    []int
	ranges []runeRange
	next   []int
}

type runeRange struct{ lo, hi rune }

// RuneRange is an inclusive rune interval used in host declarations.
type RuneRange struct {
	Lo rune
	Hi rune
}

func toInternalRanges(in []RuneRange) []runeRange {
	out := make([]runeRange, len(in))
	for i, r := range in {
		out[i] = runeRange{lo: r.Lo, hi: r.Hi}
	}
	return out
}

func (rg runeRange) contains(r rune) bool { return rg.lo <= r && r <= rg.hi }

// CompileRegex parses pattern into a lazy DFA.
//
// Supported syntax: concatenation, |, *, +, ?, parentheses, . (any rune
// except newline), [...] / [^...] classes with ranges, and the escapes
// \d \D \w \W \s \S plus single-rune escapes. The pattern is implicitly
// anchored at both ends.
func CompileRegex(pattern string) (*DFA, error) {
	p := &regexParser{src: []rune(pattern)}
	ast, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.src) {
		return nil, fmt.Errorf("envguard: trailing characters in regex %q", pattern)
	}
	b := &nfaBuilder{states: []nfaState{{}}}
	frag := b.build(ast)
	d := &DFA{
		nfa:       b.states,
		start:     frag.start,
		accept:    frag.accept,
		subsetMap: map[string]int{},
		trans:     map[dfaKey]int{},
	}
	return d, nil
}

// MustCompileRegex panics on invalid patterns; intended for host constants.
func MustCompileRegex(pattern string) *DFA {
	d, err := CompileRegex(pattern)
	if err != nil {
		panic(err)
	}
	return d
}

// RuneSeq is anything addressable by rune offset; graph nodes satisfy it.
type RuneSeq interface {
	RuneLen() int64
	RuneAt(int64) rune
}

type stringSeq string

func (s stringSeq) RuneLen() int64      { return int64(len([]rune(string(s)))) }
func (s stringSeq) RuneAt(i int64) rune { return []rune(string(s))[i] }

func runeSeqOf(s string) RuneSeq { return stringSeq(s) }

// AcceptsString is the string membership test.
func (d *DFA) AcceptsString(s string) bool {
	return d.Accepts(runeSeqOf(s))
}

// Accepts runs the automaton over seq without materializing it.
func (d *DFA) Accepts(seq RuneSeq) bool {
	state := d.subsetID(d.closure([]int{d.start}))
	n := seq.RuneLen()
	for i := int64(0); i < n; i++ {
		state = d.step(state, seq.RuneAt(i))
		if state == deadState {
			return false
		}
	}
	return d.isAccept(state)
}

const deadState = -1

func (d *DFA) isAccept(state int) bool {
	if state == deadState {
		return false
	}
	for _, id := range d.subsetList[state] {
		if id == d.accept {
			return true
		}
	}
	return false
}

func (d *DFA) step(state int, r rune) int {
	key := dfaKey{state, r}
	if next, ok := d.trans[key]; ok {
		return next
	}
	var moved []int
	for _, id := range d.subsetList[state] {
		st := d.nfa[id]
		for i, rg := range st.ranges {
			if rg.contains(r) {
				moved = append(moved, st.next[i])
			}
		}
	}
	next := deadState
	if len(moved) > 0 {
		next = d.subsetID(d.closure(moved))
	}
	d.trans[key] = next
	return next
}

func subsetKey(set []int) string {
	var b strings.Builder
	for _, id := range set {
		b.WriteString(strconv.Itoa(id))
		b.WriteByte(',')
	}
	return b.String()
}

func (d *DFA) subsetID(set []int) int {
	key := subsetKey(set)
	if id, ok := d.subsetMap[key]; ok {
		return id
	}
	id := len(d.subsetList)
	d.subsetList = append(d.subsetList, append([]int(nil), set...))
	d.subsetMap[key] = id
	return id
}

func (d *DFA) closure(seed []int) []int {
	seen := map[int]bool{}
	stack := append([]int(nil), seed...)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		stack = append(stack, d.nfa[id].eps...)
	}
	out := make([]int, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

// nfaBuilder constructs Thompson NFA fragments.
type nfaBuilder struct{ states []nfaState }

func (b *nfaBuilder) newState() int {
	id := len(b.states)
	b.states = append(b.states, nfaState{})
	return id
}

func (b *nfaBuilder) addEps(from, to int) {
	b.states[from].eps = append(b.states[from].eps, to)
}

func (b *nfaBuilder) addChar(from, to int, rgs []runeRange) {
	st := &b.states[from]
	for _, rg := range rgs {
		st.ranges = append(st.ranges, rg)
		st.next = append(st.next, to)
	}
}

type fragment struct{ start, accept int }

func (b *nfaBuilder) build(node regexNode) fragment {
	switch n := node.(type) {
	case *charNode:
		s, a := b.newState(), b.newState()
		b.addChar(s, a, n.ranges)
		return fragment{s, a}
	case *concatNode:
		if len(n.children) == 0 {
			s, a := b.newState(), b.newState()
			b.addEps(s, a)
			return fragment{s, a}
		}
		frag := b.build(n.children[0])
		for _, child := range n.children[1:] {
			next := b.build(child)
			b.addEps(frag.accept, next.start)
			frag.accept = next.accept
		}
		return frag
	case *altNode:
		s, a := b.newState(), b.newState()
		for _, child := range n.children {
			f := b.build(child)
			b.addEps(s, f.start)
			b.addEps(f.accept, a)
		}
		return fragment{s, a}
	case *repeatNode:
		inner := b.build(n.child)
		s, a := b.newState(), b.newState()
		b.addEps(s, inner.start)
		if n.min == 0 {
			b.addEps(s, a)
		}
		if n.max == -1 {
			b.addEps(inner.accept, inner.start)
		} else if n.max > 1 {
			prevAccept := inner.accept
			for i := 1; i < n.max; i++ {
				copyF := b.build(n.child)
				b.addEps(prevAccept, copyF.start)
				prevAccept = copyF.accept
				if i >= n.min {
					b.addEps(s, prevAccept)
				}
			}
			inner.accept = prevAccept
		}
		b.addEps(inner.accept, a)
		return fragment{s, a}
	default:
		panic("envguard: unknown regex node")
	}
}
