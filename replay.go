package hvsched

import (
	"fmt"
	"math/big"
)

// Replay performs an exact forward replay for len(p.Observations) emissions.
// The returned emissions are what the supplied weights actually schedule;
// compare them with p.Observations using Verify.
func Replay(p Problem, weights WeightVector) (*ReplayResult, error) {
	return ReplayCount(p, weights, len(p.Observations))
}

// ReplayCount replays exactly n emissions without requiring placeholder
// observations in p.
func ReplayCount(p Problem, weights WeightVector, n int) (*ReplayResult, error) {
	if n < 0 {
		return nil, fmt.Errorf("hvsched: replay emission count %d is negative", n)
	}
	c, err := compileProblem(p)
	if err != nil {
		return nil, err
	}
	wv, err := weights.values(c)
	if err != nil {
		return nil, err
	}
	if err := c.checkLayerSums(wv); err != nil {
		return nil, err
	}
	return c.replay(wv, n)
}

// Verify reports whether the weights reproduce every observed leaf and any
// supplied exact completion tag.
func Verify(p Problem, weights WeightVector) (bool, error) {
	c, err := compileProblem(p)
	if err != nil {
		return false, err
	}
	wv, err := weights.values(c)
	if err != nil {
		return false, err
	}
	// A wrong layer total is a failed candidate match, not a malformed
	// diagnostic problem.
	if err := c.checkLayerSums(wv); err != nil {
		return false, nil
	}
	res, err := c.replay(wv, len(p.Observations))
	if err != nil {
		return false, nil
	}
	for i, obs := range p.Observations {
		if i >= len(res.Emissions) || res.Emissions[i].Leaf != obs.Leaf {
			return false, nil
		}
		if finish := obs.completionConstraint(); finish != nil {
			node := obs.FinishNode
			if node == "" {
				node = obs.Leaf
			}
			tag := res.Emissions[i].Finishes[node]
			if tag == nil || tag.Cmp(finish) != 0 {
				return false, nil
			}
		}
	}
	return true, nil
}

func (w WeightVector) values(c *compiled) ([]uint64, error) {
	if len(w) != c.nVar {
		return nil, fmt.Errorf("hvsched: got %d weights, want %d", len(w), c.nVar)
	}
	out := make([]uint64, c.nVar)
	for i, x := range w {
		if x < c.p.Vars[i].Min || x > c.p.Vars[i].Max {
			return nil, fmt.Errorf("hvsched: weight %q=%d outside [%d,%d]", c.varName[i], x, c.p.Vars[i].Min, c.p.Vars[i].Max)
		}
		out[i] = x
	}
	return out, nil
}

func (c *compiled) checkLayerSums(wv []uint64) error {
	for layer, idxs := range c.groupVars {
		target, ok := c.p.LayerSums[layer]
		if !ok {
			continue
		}
		var sum uint64
		for _, i := range idxs {
			sum += wv[i]
		}
		var fixed uint64
		for i, n := range c.p.Nodes {
			if i != c.root && n.Layer == layer && !c.weight[i].isVar {
				fixed += c.weight[i].fixed
			}
		}
		if sum+fixed != target {
			return fmt.Errorf("hvsched: layer %q weights sum to %d, want %d", layer, sum+fixed, target)
		}
	}
	return nil
}

func nodeWeight(ref weightRef, wv []uint64) uint64 {
	if ref.isVar {
		return wv[ref.varIdx]
	}
	return ref.fixed
}

func ratMax(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) >= 0 {
		return new(big.Rat).Set(a)
	}
	return new(big.Rat).Set(b)
}

type concreteChoice struct {
	scheduler int
	child     int
	leaf      int
	length    uint64
	start     *big.Rat
	finish    *big.Rat
	sub       *concreteChoice
}

type concreteState struct {
	vtime []*big.Rat
	last  []*big.Rat
	wv    []uint64
}

func (c *compiled) newConcreteState(wv []uint64) *concreteState {
	st := &concreteState{
		vtime: make([]*big.Rat, len(c.p.Nodes)),
		last:  make([]*big.Rat, len(c.p.Nodes)),
		wv:    wv,
	}
	for i := range c.p.Nodes {
		st.vtime[i] = new(big.Rat)
		st.last[i] = new(big.Rat)
	}
	return st
}

func (c *compiled) replay(wv []uint64, emissions int) (*ReplayResult, error) {
	byBefore := make([][]ArrivalSpec, emissions+1)
	queues := make(map[int]*packetQueue)
	for i := range c.p.Nodes {
		if len(c.children[i]) == 0 {
			queues[i] = &packetQueue{}
		}
	}
	for _, a := range c.p.Arrivals {
		before := a.Before
		if before > emissions {
			continue
		}
		byBefore[before] = append(byBefore[before], a)
	}

	st := c.newConcreteState(wv)
	out := &ReplayResult{Emissions: make([]Emission, 0, emissions)}

	for t := 0; t < emissions; t++ {
		for _, a := range byBefore[t] {
			queues[c.id[a.Leaf]].add(a.Count, a.Length)
		}
		heads := make(map[int]uint64)
		for leaf, q := range queues {
			if length, ok := q.head(); ok {
				heads[leaf] = length
			}
		}
		active := c.activeNodes(heads)
		choice, err := c.chooseConcrete(c.root, heads, active, st)
		if err != nil {
			return nil, fmt.Errorf("emission %d: %w", t, err)
		}

		info := Emission{
			Leaf:           c.p.Nodes[choice.leaf].ID,
			Finishes:       make(map[string]*big.Rat),
			SchedulerTimes: make(map[string]*big.Rat),
		}
		cur := choice
		scheduler := c.root
		for cur != nil {
			childID := c.p.Nodes[cur.child].ID
			info.Finishes[childID] = new(big.Rat).Set(cur.finish)
			st.last[cur.child] = new(big.Rat).Set(cur.finish)
			if c.p.Clock == ClockFinishTag {
				st.vtime[scheduler] = new(big.Rat).Set(cur.finish)
				info.SchedulerTimes[c.p.Nodes[scheduler].ID] = new(big.Rat).Set(cur.finish)
			} else {
				st.vtime[scheduler] = new(big.Rat).Set(cur.start)
				info.SchedulerTimes[c.p.Nodes[scheduler].ID] = new(big.Rat).Set(cur.start)
			}
			if len(c.children[cur.child]) == 0 {
				break
			}
			scheduler = cur.child
			cur = cur.sub
		}
		info.Finish = new(big.Rat).Set(info.Finishes[c.p.Nodes[choice.leaf].ID])

		queues[choice.leaf].pop()
		out.Emissions = append(out.Emissions, info)
	}
	return out, nil
}

func (c *compiled) chooseConcrete(scheduler int, heads map[int]uint64, active []bool, st *concreteState) (*concreteChoice, error) {
	var best *concreteChoice
	var bestFinish *big.Rat

	for _, child := range c.children[scheduler] {
		if !active[child] {
			continue
		}

		choice := &concreteChoice{scheduler: scheduler, child: child}
		if len(c.children[child]) == 0 {
			length, ok := heads[child]
			if !ok {
				return nil, fmt.Errorf("active leaf %s has no head packet", c.p.Nodes[child].ID)
			}
			choice.leaf = child
			choice.length = length
		} else {
			sub, err := c.chooseConcrete(child, heads, active, st)
			if err != nil {
				return nil, err
			}
			choice.sub = sub
			choice.leaf = sub.leaf
			choice.length = sub.length
		}

		choice.start = ratMax(st.vtime[scheduler], st.last[child])
		w := nodeWeight(c.weight[child], st.wv)
		if w == 0 {
			return nil, fmt.Errorf("node %q has zero weight", c.p.Nodes[child].ID)
		}
		choice.finish = new(big.Rat).Add(
			choice.start,
			new(big.Rat).SetFrac(
				new(big.Int).SetUint64(choice.length),
				new(big.Int).SetUint64(w),
			),
		)

		if best == nil {
			best = choice
			bestFinish = choice.finish
			continue
		}
		cmp := choice.finish.Cmp(bestFinish)
		if cmp < 0 || (cmp == 0 && c.tie[child] < c.tie[best.child]) {
			best = choice
			bestFinish = choice.finish
		}
	}

	if best == nil {
		return nil, fmt.Errorf("scheduler %q has no active child", c.p.Nodes[scheduler].ID)
	}
	return best, nil
}
