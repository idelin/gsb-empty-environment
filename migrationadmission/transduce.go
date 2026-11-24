package migrationadmission

import "fmt"

// Transduce runs the deterministic policy over a legal raw name and returns
// the emitted canonical symbol sequence, including the end tail.
func Transduce(p Policy, name []byte) ([]Symbol, error) {
	cp, err := p.compile()
	if err != nil {
		return nil, err
	}
	return cp.transduce(name)
}

func (p *compiledPolicy) transduce(name []byte) ([]Symbol, error) {
	if len(name) > MaxNameLength {
		return nil, fmt.Errorf("%w: name length %d exceeds %d", ErrIllegalName, len(name), MaxNameLength)
	}
	state := p.g.acceptingStart()
	out := make([]Symbol, 0, len(name)*2+1)
	for _, on := range name {
		next, ok := p.g.step(state, on)
		if !ok {
			return nil, ErrIllegalName
		}
		out = append(out, p.t.emitFor(on)...)
		state = next
	}
	if !p.g.accept[state] {
		return nil, ErrIllegalName
	}
	out = append(out, p.t.tail...)
	return out, nil
}

func (g *compiledGrammar) acceptingStart() int {
	// Grammar.compile guarantees Start exists; the zero state numbering is not
	// assumed, callers use Grammar.Start.
	return g.startState
}

// hasAcceptingWord reports whether the grammar accepts any word within the
// length bound.
func (p *compiledPolicy) hasAcceptingWord() bool {
	type frame struct {
		state int
		depth int
	}
	seen := map[int]bool{}
	queue := []frame{{state: p.g.startState, depth: 0}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if p.g.accept[cur.state] {
			return true
		}
		if cur.depth == MaxNameLength {
			continue
		}
		for _, to := range p.g.delta[cur.state] {
			if !seen[to] {
				seen[to] = true
				queue = append(queue, frame{state: to, depth: cur.depth + 1})
			}
		}
	}
	return false
}

// hasEmittingWord reports whether some accepted word within the length bound
// produces a non-empty canonical sequence. This is a shortest-path reachability
// search over the product of the grammar and the "emitted anything yet" bit.
func (p *compiledPolicy) hasEmittingWord() bool {
	type frame struct {
		state   int
		depth   int
		emitted bool
	}
	type key struct {
		state   int
		emitted bool
	}
	seen := map[key]bool{}
	queue := []frame{{state: p.g.startState, depth: 0}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		k := key{cur.state, cur.emitted}
		if seen[k] {
			continue
		}
		seen[k] = true
		emitted := cur.emitted || len(p.t.tail) > 0
		if p.g.accept[cur.state] && emitted {
			return true
		}
		if cur.depth == MaxNameLength {
			continue
		}
		for on, to := range p.g.delta[cur.state] {
			nextEmitted := emitted || len(p.t.emitFor(on)) > 0
			queue = append(queue, frame{state: to, depth: cur.depth + 1, emitted: nextEmitted})
		}
	}
	return false
}
