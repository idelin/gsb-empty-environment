package verifier

import (
	"fmt"
	"sort"

	"prescreen.local/ddd-10554-v3/spec"
)

const vMaxGen = spec.Generation(^uint32(0))

func clone(s *spec.State) *spec.State {
	c := &spec.State{
		Counters: map[spec.PositionID]map[spec.Scope]spec.Generation{},
		Pointers: map[spec.OwnerID]spec.Pointer{},
	}
	for pid, m := range s.Counters {
		c.Counters[pid] = map[spec.Scope]spec.Generation{}
		for sc, g := range m {
			c.Counters[pid][sc] = g
		}
	}
	for o, p := range s.Pointers {
		c.Pointers[o] = p
	}
	return c
}

func sameAction(a, b *spec.Action) bool {
	if a.Kind != b.Kind || a.Owner != b.Owner || a.Position != b.Position {
		return false
	}
	if len(a.Gen) != len(b.Gen) {
		return false
	}
	for sc, g := range a.Gen {
		if b.Gen[sc] != g {
			return false
		}
	}
	return true
}

func (e *engine) ceiling(p spec.Position) map[spec.Scope]spec.Generation {
	c := map[spec.Scope]spec.Generation{}
	for _, sc := range p.Scopes {
		c[sc] = vMaxGen
	}
	for _, combo := range e.chip.Recovery {
		img := e.chip.Images[combo.Images[p.Owner]]
		for _, sc := range p.Scopes {
			if img.Versions[sc] < c[sc] {
				c[sc] = img.Versions[sc]
			}
		}
	}
	return c
}

// legalActions 独立重写动作合法性：单调烧录（不穿恢复上限）、
// 分阶段指针动作、yield，以及 pending/committed 下的幂等重放。
func (e *engine) legalActions(state *spec.State, budget int) []*spec.Action {
	acts := []*spec.Action{{Kind: spec.ActionYield}}
	if budget > 0 {
		for _, p := range e.chip.Positions {
			cur := state.Counters[p.ID]
			ceil := e.ceiling(p)
			head := false
			for _, sc := range p.Scopes {
				if ceil[sc] > cur[sc] {
					head = true
				}
			}
			if !head {
				continue
			}
			var gen func(i int, acc map[spec.Scope]spec.Generation, raised bool)
			gen = func(i int, acc map[spec.Scope]spec.Generation, raised bool) {
				if i == len(p.Scopes) {
					if raised {
						dup := map[spec.Scope]spec.Generation{}
						for sc, g := range acc {
							dup[sc] = g
						}
						acts = append(acts, &spec.Action{Kind: spec.ActionBurn, Position: p.ID, Gen: dup})
					}
					return
				}
				sc := p.Scopes[i]
				for g := cur[sc]; g <= ceil[sc]; g++ {
					acc[sc] = g
					gen(i+1, acc, raised || g > cur[sc])
				}
			}
			gen(0, map[spec.Scope]spec.Generation{}, false)
		}
	}
	for _, o := range e.chip.Owners {
		switch state.Pointers[o].Phase {
		case spec.PhaseStable:
			acts = append(acts, &spec.Action{Kind: spec.ActionRequest, Owner: o})
		case spec.PhasePending:
			acts = append(acts, &spec.Action{Kind: spec.ActionRequest, Owner: o})
			acts = append(acts, &spec.Action{Kind: spec.ActionConfirm, Owner: o})
		case spec.PhaseCommitted:
			acts = append(acts, &spec.Action{Kind: spec.ActionConfirm, Owner: o})
		}
	}
	return acts
}

func (e *engine) apply(state *spec.State, a *spec.Action) (*spec.State, error) {
	next := clone(state)
	switch a.Kind {
	case spec.ActionYield:
	case spec.ActionBurn:
		p, ok := e.positions[a.Position]
		if !ok {
			return nil, fmt.Errorf("未知位置 %q", a.Position)
		}
		raised := false
		for _, sc := range p.Scopes {
			t, ok := a.Gen[sc]
			if !ok {
				return nil, fmt.Errorf("烧录缺范围 %q", sc)
			}
			if t < next.Counters[p.ID][sc] {
				return nil, fmt.Errorf("拒绝回滚烧录")
			}
			if t > next.Counters[p.ID][sc] {
				raised = true
			}
			next.Counters[p.ID][sc] = t
		}
		if !raised {
			return nil, fmt.Errorf("拒绝空烧录")
		}
	case spec.ActionRequest:
		if next.Pointers[a.Owner].Phase == spec.PhaseCommitted {
			return nil, fmt.Errorf("已确认后不能请求")
		}
		next.Pointers[a.Owner] = spec.Pointer{Phase: spec.PhasePending}
	case spec.ActionConfirm:
		if next.Pointers[a.Owner].Phase == spec.PhaseStable {
			return nil, fmt.Errorf("未请求不能确认")
		}
		next.Pointers[a.Owner] = spec.Pointer{Phase: spec.PhaseCommitted}
	default:
		return nil, fmt.Errorf("未知动作 %q", a.Kind)
	}
	if !e.safe(next) {
		return nil, fmt.Errorf("动作破坏安全不变量")
	}
	return next, nil
}

type vOutcome struct {
	state *spec.State
	obs   *spec.Observation
	burns int
}

func (e *engine) outcomes(state *spec.State, a *spec.Action) ([]*vOutcome, error) {
	next, err := e.apply(state, a)
	if err != nil {
		return nil, err
	}
	burns := 0
	if a.Kind == spec.ActionBurn {
		burns = 1
	}
	if a.Kind == spec.ActionYield {
		return []*vOutcome{{state: next, obs: &spec.Observation{Kind: spec.ObsBoot, Readback: e.readback(next)}, burns: 0}}, nil
	}
	return []*vOutcome{
		{state: next, obs: &spec.Observation{Kind: spec.ObsAck}, burns: burns},
		{state: next, obs: &spec.Observation{Kind: spec.ObsBoot, Readback: e.readback(next)}, burns: burns},
	}, nil
}

type vBelief struct {
	key    string
	states map[string]*spec.State
	obsKey string
	edges  map[string]map[string]string
}

func stateKey(s *spec.State) string {
	k, err := spec.StateKey(s)
	if err != nil {
		panic(err)
	}
	return k
}

func actionKey(a *spec.Action) string {
	k, err := spec.ActionKey(a)
	if err != nil {
		panic(err)
	}
	return k
}

func obsKey(o *spec.Observation) string {
	k, err := spec.ObservationKey(o)
	if err != nil {
		panic(err)
	}
	return k
}

func beliefFP(states map[string]*spec.State) string {
	keys := make([]string, 0, len(states))
	for k := range states {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fp, err := spec.Fingerprint(keys)
	if err != nil {
		panic(err)
	}
	return fp
}

type enumeration struct {
	engine  *engine
	nodes   map[string]*vBelief
	stateOf map[string]*spec.State
	root    string
}

func (e *engine) enumerate() (*enumeration, error) {
	en := &enumeration{
		engine:  e,
		nodes:   map[string]*vBelief{},
		stateOf: map[string]*spec.State{},
	}
	initKey := stateKey(e.chip.Initial)
	en.stateOf[initKey] = clone(e.chip.Initial)
	rootStates := map[string]*spec.State{initKey: clone(e.chip.Initial)}
	for _, alt := range e.chip.InitialAlternatives {
		ak := stateKey(alt)
		rootStates[ak] = clone(alt)
		en.stateOf[ak] = clone(alt)
	}
	rootHist := beliefFP(rootStates)
	root := &vBelief{
		key:    rootHist,
		states: rootStates,
		edges:  map[string]map[string]string{},
	}
	en.nodes[root.key] = root
	en.root = root.key

	queue := []string{root.key}
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		n := en.nodes[k]
		if e.terminalBelief(n) {
			continue
		}
		for _, a := range e.commonActions(n, e.chip.MaxBurns) {
			ak := actionKey(a)
			allOK := true
			groups := map[string]map[string]*spec.State{}
			for _, s := range n.states {
				ocs, err := e.outcomes(s, a)
				if err != nil {
					allOK = false
					break
				}
				for _, oc := range ocs {
					sk := stateKey(oc.state)
					en.stateOf[sk] = clone(oc.state)
					okFP := obsKey(oc.obs)
					if groups[okFP] == nil {
						groups[okFP] = map[string]*spec.State{}
					}
					if _, seen := groups[okFP][sk]; !seen {
						groups[okFP][sk] = clone(oc.state)
					}
				}
			}
			if !allOK {
				continue
			}
			n.edges[ak] = map[string]string{}
			for okFP, group := range groups {
				ck := beliefFP(group)
				child, exists := en.nodes[ck]
				if !exists {
					child = &vBelief{
						key:    ck,
						states: map[string]*spec.State{},
						obsKey: okFP,
						edges:  map[string]map[string]string{},
					}
					en.nodes[ck] = child
				}
				for sk, st := range group {
					if _, ok := child.states[sk]; !ok {
						child.states[sk] = st
					}
				}
				n.edges[ak][okFP] = ck
				if !exists && !e.terminalBelief(child) {
					queue = append(queue, ck)
				}
			}
		}
	}
	return en, nil
}

func (e *engine) terminalBelief(n *vBelief) bool {
	for _, s := range n.states {
		if !e.terminal(s) {
			return false
		}
	}
	return true
}

func (e *engine) commonActions(n *vBelief, budget int) []*spec.Action {
	count := map[string]int{}
	first := map[string]*spec.Action{}
	total := 0
	for _, s := range n.states {
		total++
		for _, a := range e.legalActions(s, budget) {
			k := actionKey(a)
			count[k]++
			if _, ok := first[k]; !ok {
				first[k] = a
			}
		}
	}
	var keys []string
	for k, c := range count {
		if c == total {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]*spec.Action, 0, len(keys))
	for _, k := range keys {
		out = append(out, first[k])
	}
	return out
}

// terminalReachable 独立检查终止信念是否可从根信念安全到达。
func (en *enumeration) terminalReachable() bool {
	seen := map[string]bool{en.root: true}
	queue := []string{en.root}
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		n := en.nodes[k]
		if en.engine.terminalBelief(n) {
			return true
		}
		for _, byObs := range n.edges {
			for _, child := range byObs {
				if !seen[child] {
					seen[child] = true
					queue = append(queue, child)
				}
			}
		}
	}
	return false
}
