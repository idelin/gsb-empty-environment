package recovery

import (
	"fmt"
	"math/big"
)

type state struct {
	txn   uint64
	pv    uint64
	block uint64
}

type best struct {
	benefit big.Int
	cost    big.Int
	count   big.Int
}

// Plan solves one ranked recovery-planning request.  rank is 1-based in
// lexicographic order: logical volume 0 first, candidates in their input order,
// then logical volume 1, and so on.  A nil rank means rank 1.
//
// A structurally valid request with no legal combination returns a Result with
// Feasible=false and Count=0 (and no error).  Malformed requests, unsupported
// sizes, over-wide frontiers and out-of-range ranks return errors.
func Plan(in Input, rank *big.Int) (*Result, error) {
	p, err := compile(in)
	if err != nil {
		return nil, err
	}
	initial := p.initialState()
	p.memo = make([]map[state]*best, p.n+1)
	for i := range p.memo {
		p.memo[i] = make(map[state]*best)
	}
	root := p.solve(0, initial)
	if root == nil {
		return &Result{
			Feasible:     false,
			Benefit:      new(big.Int),
			DownloadCost: new(big.Int),
			Count:        new(big.Int),
			Boundary:     p.boundary,
		}, nil
	}
	want := big.NewInt(1)
	if rank != nil {
		want.Set(rank)
	}
	if want.Sign() <= 0 || want.Cmp(&root.count) > 0 {
		return nil, fmt.Errorf("%w: rank %s, optimal combinations %s",
			ErrRankOutOfRange, want.String(), root.count.String())
	}
	cert, err := p.reconstruct(initial, root, want)
	if err != nil {
		return nil, err
	}
	return &Result{
		Feasible:     true,
		Benefit:      new(big.Int).Set(&root.benefit),
		DownloadCost: new(big.Int).Set(&root.cost),
		Count:        new(big.Int).Set(&root.count),
		Certificate:  cert,
		Boundary:     p.boundary,
	}, nil
}

// PlanBest returns rank 1.
func PlanBest(in Input) (*Result, error) {
	return Plan(in, nil)
}

// PlanRank is a convenience wrapper for an int64 rank.
func PlanRank(in Input, rank int64) (*Result, error) {
	return Plan(in, big.NewInt(rank))
}

func (p *planner) initialState() state {
	var st state
	for k, t := range p.layers[0].txns {
		st.txn |= uint64(1) << uint(2*k) // viable
		if p.required[t] {
			st.txn |= uint64(1) << uint(2*k+1) // must commit
		}
	}
	return st
}

func (p *planner) solve(i int, st state) *best {
	if i == p.n {
		if st != (state{}) {
			return nil
		}
		return &best{count: *big.NewInt(1)}
	}
	if m, ok := p.memo[i][st]; ok {
		return m
	}

	var out *best
	for ci := range p.cands[i] {
		ns, sb, sc, ok := p.transition(i, st, ci)
		if !ok {
			continue
		}
		child := p.solve(i+1, ns)
		if child == nil {
			continue
		}
		var tb, tc big.Int
		tb.Add(&sb, &child.benefit)
		tc.Add(&sc, &child.cost)
		if out == nil {
			out = &best{}
			out.benefit.Set(&tb)
			out.cost.Set(&tc)
			out.count.Set(&child.count)
			continue
		}
		switch compareValue(&tb, &tc, &out.benefit, &out.cost) {
		case 1:
			out.benefit.Set(&tb)
			out.cost.Set(&tc)
			out.count.Set(&child.count)
		case 0:
			out.count.Add(&out.count, &child.count)
		}
	}
	p.memo[i][st] = out
	return out
}

// compareValue orders (benefit, cost): larger benefit is better, then smaller
// cost is better.
func compareValue(ab, ac, bb, bc *big.Int) int {
	if c := ab.Cmp(bb); c != 0 {
		return c
	}
	if c := ac.Cmp(bc); c != 0 {
		return -c
	}
	return 0
}

// transition applies candidate ci at layer i.  It returns the new state and
// the exact benefit/cost contributed while processing this layer.
func (p *planner) transition(i int, st state, ci int) (state, big.Int, big.Int, bool) {
	var zero big.Int
	cur := &p.layers[i]
	next := &p.layers[i+1]
	c := &p.cands[i][ci]

	pvPos := cur.pvPos[c.pv]
	if pvPos >= 0 && st.pv&(uint64(1)<<uint(pvPos)) != 0 {
		return state{}, zero, zero, false
	}

	active := cur.txns
	var viable [maxTxnStatusItems]bool
	var must [maxTxnStatusItems]bool
	var mustClose [maxTxnStatusItems]bool
	var mustLater [maxTxnStatusItems]bool

	for k, t := range active {
		bits := (st.txn >> uint(2*k)) & 3
		viable[k] = bits&1 != 0
		must[k] = bits&2 != 0 || p.required[t]
	}

	// Viability changes are monotone: one bad volume/segment is permanent.
	for k, t := range active {
		own := c.own[t]
		wrong := c.wrong[t]
		if p.partMask[t]&(uint64(1)<<uint(i)) != 0 {
			if own != 1 || wrong != 0 {
				viable[k] = false
			}
		} else if own != 0 || wrong != 0 {
			viable[k] = false
		}
		if must[k] && !viable[k] {
			return state{}, zero, zero, false
		}
		if p.deadline[t] == i {
			mustClose[k] = must[k]
		}
	}

	// Every viable transaction closing here is committed.  Its direct
	// prerequisites must close here or remain resident.
	for k, t := range active {
		if p.deadline[t] != i || !viable[k] {
			continue
		}
		for _, u := range p.prereqs[t] {
			pos := cur.txnPos[u]
			if pos < 0 {
				return state{}, zero, zero, false
			}
			if p.deadline[u] == i {
				mustClose[pos] = true
			} else if p.deadline[u] > i {
				mustLater[pos] = true
			} else {
				return state{}, zero, zero, false
			}
		}
	}

	var ben, cost big.Int
	for k, t := range active {
		if p.deadline[t] == i {
			if mustClose[k] && !viable[k] {
				return state{}, zero, zero, false
			}
			if viable[k] {
				ben.Add(&ben, p.benefit[t])
			}
			continue
		}
		if (must[k] || mustLater[k]) && !viable[k] {
			return state{}, zero, zero, false
		}
	}

	// Content blocks are paid for when first referenced by a selected
	// candidate.  The state retains only blocks still reachable later.
	for _, b := range c.blocks {
		if pos := cur.blkPos[b]; pos >= 0 && st.block&(uint64(1)<<uint(pos)) != 0 {
			continue
		}
		cost.Add(&cost, p.blockCost[b])
	}

	var ns state
	for k, t := range next.txns {
		var v, m bool
		if pos := cur.txnPos[t]; pos >= 0 && p.deadline[t] > i {
			v = viable[pos]
			m = must[pos] || mustLater[pos] || p.required[t]
		} else {
			// Introduced at layer i+1.  Nothing before its start can affect
			// it, so it is pristine except for the caller's requirement.
			v = true
			m = p.required[t]
		}
		if m && !v {
			return state{}, zero, zero, false
		}
		if v {
			ns.txn |= uint64(1) << uint(2*k)
		}
		if m {
			ns.txn |= uint64(1) << uint(2*k+1)
		}
	}

	for k, x := range next.pvs {
		used := false
		if pos := cur.pvPos[x]; pos >= 0 && st.pv&(uint64(1)<<uint(pos)) != 0 {
			used = true
		} else if x == c.pv {
			used = true
		}
		if used {
			ns.pv |= uint64(1) << uint(k)
		}
	}

	for k, b := range next.blocks {
		downloaded := false
		if pos := cur.blkPos[b]; pos >= 0 && st.block&(uint64(1)<<uint(pos)) != 0 {
			downloaded = true
		} else if _, ok := c.blockSet[b]; ok {
			downloaded = true
		}
		if downloaded {
			ns.block |= uint64(1) << uint(k)
		}
	}

	return ns, ben, cost, true
}

func (p *planner) reconstruct(initial state, root *best, rank *big.Int) (*Certificate, error) {
	st := initial
	prefixBenefit := new(big.Int)
	prefixCost := new(big.Int)
	r := new(big.Int).Set(rank)
	selection := make([]int, p.n)

	for i := 0; i < p.n; i++ {
		targetBenefit := new(big.Int).Sub(&root.benefit, prefixBenefit)
		targetCost := new(big.Int).Sub(&root.cost, prefixCost)
		chosen := -1
		for ci := range p.cands[i] {
			ns, sb, sc, ok := p.transition(i, st, ci)
			if !ok {
				continue
			}
			child := p.solve(i+1, ns)
			if child == nil {
				continue
			}
			var tb, tc big.Int
			tb.Add(&sb, &child.benefit)
			tc.Add(&sc, &child.cost)
			if tb.Cmp(targetBenefit) != 0 || tc.Cmp(targetCost) != 0 {
				continue
			}
			if r.Cmp(&child.count) > 0 {
				r.Sub(r, &child.count)
				continue
			}
			chosen = ci
			selection[i] = ci
			st = ns
			prefixBenefit.Add(prefixBenefit, &sb)
			prefixCost.Add(prefixCost, &sc)
			break
		}
		if chosen < 0 {
			return nil, fmt.Errorf("recovery: internal rank reconstruction failure at layer %d", i)
		}
	}

	cert := &Certificate{
		Rank:         new(big.Int).Set(rank),
		Benefit:      new(big.Int).Set(&root.benefit),
		DownloadCost: new(big.Int).Set(&root.cost),
		Choices:      make([]Choice, 0, p.n),
	}
	for v := 0; v < p.n; v++ {
		ci := selection[v]
		c := &p.cands[v][ci]
		inCand := &p.volumes[v].Candidates[ci]
		blocks := make([]BlockID, len(c.blocks))
		for k, b := range c.blocks {
			blocks[k] = p.blocks[b]
		}
		cert.Choices = append(cert.Choices, Choice{
			LogicalVolume:      v,
			LogicalVolumeName:  p.volumes[v].Name,
			Candidate:          ci,
			CandidateID:        inCand.ID,
			PhysicalVolume:     p.pvIDs[c.pv],
			VolumeGeneration:   p.pvs[c.pv].generation,
			Node:               c.node,
			SnapshotGeneration: c.snapshotGeneration,
			SnapshotDigest:     c.digest,
			Blocks:             blocks,
		})
	}
	cert.Committed = p.committedFor(selection)
	return cert, nil
}

func (p *planner) committedFor(selection []int) []TxnID {
	var out []TxnID
	for t := range p.txnIDs {
		ok := true
		for v := 0; v < p.n; v++ {
			c := &p.cands[v][selection[v]]
			own := c.own[t]
			wrong := c.wrong[t]
			if p.partMask[t]&(uint64(1)<<uint(v)) != 0 {
				if own != 1 || wrong != 0 {
					ok = false
					break
				}
			} else if own != 0 || wrong != 0 {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, p.txnIDs[t])
		}
	}
	return out
}
