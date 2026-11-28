package fedscheduler

import (
	"math/big"
	"sort"
)

// participant is an internal allocation target: either a concrete cluster or
// an aggregated failure domain.
type participant struct {
	key      string
	weight   int64
	capacity int64
}

// remEntry carries the fractional remainder of a participant for deterministic
// largest-remainder ordering.
type remEntry struct {
	key string
	// remNum is the numerator of the fractional remainder when the share is
	// expressed as remNum/weight: floor is share/weight and the leftover
	// fraction is (share%weight)/weight.
	remNum int64
	weight int64
}

// grant is a single-round allocation decision for one participant.
type grant struct {
	p      participant
	floor  int64
	room   int64
	isSat  bool
	remNum int64
}

// cappedWeighted distributes need units across the participants while
// respecting base <= result <= base+capacity.
//
// It implements the recursive overflow algorithm: every round grants each
// unsaturated participant its floor(weight share), distributes the fractional
// remainders with the largest-remainder method (ties broken by ascending key),
// locks any participant that reaches its hard cap, and re-enters the loop with
// the overflow redistributed over the still-unsaturated participants by their
// relative weights.
//
// Preconditions (enforced by public callers): need >= 0, sum(capacity) >= need,
// every weight > 0 and every capacity >= 0.
func cappedWeighted(parts []participant, base map[string]int64, need int64) (map[string]int64, []string, int) {
	out := make(map[string]int64, len(parts))
	active := make([]participant, 0, len(parts))
	active = append(active, parts...)
	for _, p := range parts {
		out[p.key] = base[p.key]
	}

	var saturated []string
	rounds := 0

	for need > 0 {
		rounds++
		weightSum := big.NewInt(0)
		for _, p := range active {
			weightSum.Add(weightSum, big.NewInt(p.weight))
		}

		grants := make([]grant, len(active))
		var assigned int64
		rems := make([]remEntry, 0, len(active))

		for i, p := range active {
			shareNum := new(big.Int).Mul(big.NewInt(need), big.NewInt(p.weight))
			q, r := new(big.Int).QuoRem(shareNum, weightSum, new(big.Int))
			floor := q.Int64()
			rem := r.Int64()
			room := p.capacity
			g := grant{p: p, floor: floor, room: room}
			if floor >= room {
				// The fair share alone saturates (or, defensively,
				// overshoots) this participant: lock it at the cap.
				g.floor = room
				g.isSat = true
			} else {
				g.remNum = rem
				rems = append(rems, remEntry{key: p.key, remNum: rem, weight: p.weight})
			}
			grants[i] = g
			assigned += g.floor
		}

		leftover := need - assigned

		// Largest-remainder allocation of the fractional units, restricted
		// to participants that still have headroom after their floor grant.
		if leftover > 0 {
			sortRemainders(rems)
			for _, r := range rems {
				if leftover == 0 {
					break
				}
				idx := indexByKey(grants, r.key)
				if grants[idx].floor >= grants[idx].room {
					continue
				}
				grants[idx].floor++
				leftover--
				if grants[idx].floor >= grants[idx].room {
					grants[idx].isSat = true
				}
			}
		}

		var nextActive []participant
		for _, g := range grants {
			out[g.p.key] += g.floor
			if g.isSat {
				saturated = append(saturated, g.p.key)
				continue
			}
			nextActive = append(nextActive, participant{
				key:      g.p.key,
				weight:   g.p.weight,
				capacity: g.room - g.floor,
			})
		}

		need = leftover
		if need > 0 && len(nextActive) == 0 {
			// Unreachable given the sum(capacity) >= need precondition.
			panic("fedscheduler: internal error: overflow with no unsaturated participant")
		}
		active = nextActive
	}

	sort.Strings(saturated)
	return out, saturated, rounds
}

// indexByKey locates a grant by participant key.
func indexByKey(grants []grant, key string) int {
	for i := range grants {
		if grants[i].p.key == key {
			return i
		}
	}
	return -1
}

// sortRemainders orders remainder entries by descending fractional remainder
// (remNum/weight), with ascending key as the deterministic tie breaker.
// Fractions are compared exactly by cross multiplication using big.Int so no
// floating-point rounding can perturb the ordering.
func sortRemainders(rems []remEntry) {
	sort.Slice(rems, func(i, j int) bool {
		if rems[i].remNum == rems[j].remNum && rems[i].weight == rems[j].weight {
			return rems[i].key < rems[j].key
		}
		left := new(big.Int).Mul(big.NewInt(rems[i].remNum), big.NewInt(rems[j].weight))
		right := new(big.Int).Mul(big.NewInt(rems[j].remNum), big.NewInt(rems[i].weight))
		cmp := left.Cmp(right)
		if cmp != 0 {
			return cmp > 0
		}
		return rems[i].key < rems[j].key
	})
}

// Allocate splits replicas across clusters using static weights and hard
// capacity caps, with no spread constraints.
//
// It guarantees 0 <= Replicas[name] <= Capacity for every cluster and
// sum(Replicas) == replicas. When sum(Capacity) < replicas it returns an
// *InsufficientCapacityError (matched by errors.Is(err,
// ErrInsufficientCapacity)) whose MaxPlan saturates every cluster and is the
// unique maximum-sum plan.
func Allocate(replicas int64, clusters []Cluster) (*Allocation, error) {
	parts, base, totalCap, err := validateClusters(replicas, clusters)
	if err != nil {
		return nil, err
	}
	if replicas == 0 {
		return &Allocation{Replicas: zeroPlan(clusters), Total: 0, Saturated: []string{}, Rounds: 0}, nil
	}
	if totalCap < replicas {
		return nil, insufficientError(replicas, parts, base)
	}

	plan, saturated, rounds := cappedWeighted(parts, base, replicas)
	return &Allocation{
		Replicas:  plan,
		Total:     replicas,
		Saturated: saturated,
		Rounds:    rounds,
	}, nil
}

// validateClusters normalizes and validates the public cluster list and
// returns the internal participants, zero base and aggregate capacity.
func validateClusters(replicas int64, clusters []Cluster) ([]participant, map[string]int64, int64, error) {
	if replicas < 0 {
		return nil, nil, 0, invalidInput("replicas must be non-negative, got %d", replicas)
	}
	if len(clusters) == 0 {
		return nil, nil, 0, invalidInput("at least one cluster is required")
	}

	seen := make(map[string]struct{}, len(clusters))
	parts := make([]participant, 0, len(clusters))
	base := make(map[string]int64, len(clusters))
	var totalCap int64

	for _, c := range clusters {
		if c.Name == "" {
			return nil, nil, 0, invalidInput("cluster name must not be empty")
		}
		if _, dup := seen[c.Name]; dup {
			return nil, nil, 0, invalidInput("duplicate cluster name %q", c.Name)
		}
		seen[c.Name] = struct{}{}
		if c.Weight <= 0 {
			return nil, nil, 0, invalidInput("cluster %q weight must be positive, got %d", c.Name, c.Weight)
		}
		if c.Capacity < 0 {
			return nil, nil, 0, invalidInput("cluster %q capacity must be non-negative, got %d", c.Name, c.Capacity)
		}
		parts = append(parts, participant{key: c.Name, weight: c.Weight, capacity: c.Capacity})
		base[c.Name] = 0
		totalCap += c.Capacity
	}
	return parts, base, totalCap, nil
}

// zeroPlan returns a zero allocation for every cluster name.
func zeroPlan(clusters []Cluster) map[string]int64 {
	plan := make(map[string]int64, len(clusters))
	for _, c := range clusters {
		plan[c.Name] = 0
	}
	return plan
}

// insufficientError builds the diagnostic error carrying the maximum
// schedulable plan (base + capacity for every participant).
func insufficientError(requested int64, parts []participant, base map[string]int64) error {
	maxPlan := make(map[string]int64, len(parts))
	var capacity int64
	for _, p := range parts {
		maxPlan[p.key] = base[p.key] + p.capacity
		capacity += p.capacity
	}
	return &InsufficientCapacityError{
		Requested: requested,
		Capacity:  capacity,
		Deficit:   requested - capacity,
		MaxPlan:   maxPlan,
	}
}
