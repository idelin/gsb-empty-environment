package pdp

import "fmt"

// This file is the independent verifier required by the library contract:
// it enumerates every legal permutation of the remaining pickup/delivery
// events (pickup before delivery, capacity respected at all times) and
// keeps the cheapest route, breaking ties by the visited node id sequence.
// It never decomposes the instance into per-request shortest paths.

// SolveBrute solves p by exhaustive enumeration of legal event
// permutations. It is the reference implementation used to verify Solve.
func SolveBrute(p Problem) (Route, error) {
	if err := p.validate(); err != nil {
		return Route{}, err
	}
	legs := make([]Leg, len(p.Requests))
	for i, r := range p.Requests {
		legs[i] = Leg{Request: r.ID, Pickup: r.Pickup, Drop: r.Drop, Weight: r.Weight}
	}
	return bruteLegs(p.Dist, p.Capacity, p.Start, p.Start, legs)
}

type bruteForce struct {
	dist     [][]int
	capacity int
	start    int
	legs     []Leg

	best     int
	bestOK   bool
	bestSeq  []int
	bestActs []action
	curSeq   []int
	curActs  []action
}

// bruteLegs enumerates every legal permutation of the events of legs,
// starting at pos with the already-picked legs on board, and returns the
// optimal route back to start.
func bruteLegs(dist [][]int, capacity, start, pos int, legs []Leg) (Route, error) {
	load := 0
	for _, l := range legs {
		if l.Weight < 0 {
			return Route{}, fmt.Errorf("pdp: negative weight for request %d", l.Request)
		}
		if l.Picked {
			load += l.Weight
		}
	}
	if load > capacity {
		return Route{}, ErrInfeasible
	}
	b := &bruteForce{dist: dist, capacity: capacity, start: start, legs: legs}
	picked := 0
	for i, l := range legs {
		if l.Picked {
			picked |= 1 << uint(i)
		}
	}
	b.enumerate(picked, 0, pos, load, 0)
	if !b.bestOK {
		return Route{}, ErrInfeasible
	}
	return buildRoute(start, legs, dpResult{
		ok:   true,
		cost: b.best,
		seq:  b.bestSeq,
		acts: b.bestActs,
	}), nil
}

// enumerate extends the current partial permutation with every legal next
// event. cost is the distance accumulated so far; load the current load.
func (b *bruteForce) enumerate(picked, done, pos, load, cost int) {
	all := (1 << uint(len(b.legs))) - 1
	if done == all {
		total := cost + b.dist[pos][b.start]
		if !b.bestOK || total < b.best || (total == b.best && lessSeq(b.curSeq, b.bestSeq)) {
			b.best = total
			b.bestOK = true
			b.bestSeq = append([]int(nil), b.curSeq...)
			b.bestActs = append([]action(nil), b.curActs...)
		}
		return
	}
	for i, leg := range b.legs {
		bit := 1 << uint(i)
		if picked&bit == 0 {
			if load+leg.Weight > b.capacity {
				continue
			}
			b.curSeq = append(b.curSeq, leg.Pickup)
			b.curActs = append(b.curActs, action{leg: i, kind: PickupStop})
			b.enumerate(picked|bit, done, leg.Pickup, load+leg.Weight, cost+b.dist[pos][leg.Pickup])
			b.curSeq = b.curSeq[:len(b.curSeq)-1]
			b.curActs = b.curActs[:len(b.curActs)-1]
		} else if done&bit == 0 {
			kind := DeliverStop
			if leg.Returning {
				kind = ReturnStop
			}
			b.curSeq = append(b.curSeq, leg.Drop)
			b.curActs = append(b.curActs, action{leg: i, kind: kind})
			b.enumerate(picked, done|bit, leg.Drop, load-leg.Weight, cost+b.dist[pos][leg.Drop])
			b.curSeq = b.curSeq[:len(b.curSeq)-1]
			b.curActs = b.curActs[:len(b.curActs)-1]
		}
	}
}
