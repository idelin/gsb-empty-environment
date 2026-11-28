package fedscheduler

import (
	"math/big"
	"sort"
)

// Rebalance re-places replicas for an already running workload.
//
// In KeepExisting mode every existing replica that still fits inside the new
// hard caps and spread constraints is frozen. Evictions happen only when a
// cluster's capacity shrank below its current load, an aggregate downscale
// requires it, or a domain constraint forces it. Every replica freed by an
// eviction enters a pool that is re-placed (by weight on unsaturated targets)
// before any brand-new replica is added, so the number of migrated replicas
// sum_i max(0, existing_i - new_i) stays at the constraint-forced minimum.
//
// When sum(Capacity) < replicas the call fails with
// *InsufficientCapacityError carrying the maximum schedulable plan.
func Rebalance(replicas int64, clusters []Cluster, existing map[string]int64, mode RebalanceMode) (*RebalanceResult, error) {
	return RebalanceWithSpread(replicas, clusters, existing, mode, nil)
}

// RebalanceWithSpread is Rebalance with an optional spread constraint.
func RebalanceWithSpread(replicas int64, clusters []Cluster, existing map[string]int64, mode RebalanceMode, constraints []SpreadConstraint) (*RebalanceResult, error) {
	parts, _, totalCap, err := validateClusters(replicas, clusters)
	if err != nil {
		return nil, err
	}
	if mode != KeepExisting {
		return nil, invalidInput("unsupported rebalance mode %q (only KeepExisting is implemented)", mode)
	}

	cur, err := normalizeExisting(clusters, existing)
	if err != nil {
		return nil, err
	}
	orig := cloneMap(cur)

	if len(constraints) > 1 {
		return nil, invalidInput("at most one spread constraint is supported per call, got %d", len(constraints))
	}

	if totalCap < replicas {
		base := make(map[string]int64, len(parts))
		for _, p := range parts {
			base[p.key] = 0
		}
		return nil, insufficientError(replicas, parts, base)
	}

	if len(constraints) == 0 {
		return rebalanceUnconstrained(replicas, parts, cur, orig)
	}
	return rebalanceConstrained(replicas, clusters, parts, cur, orig, constraints[0])
}

// normalizeExisting validates the previous distribution and fills zero for
// every cluster that is absent from the map.
func normalizeExisting(clusters []Cluster, existing map[string]int64) (map[string]int64, error) {
	cur := make(map[string]int64, len(clusters))
	known := make(map[string]struct{}, len(clusters))
	for _, c := range clusters {
		known[c.Name] = struct{}{}
		cur[c.Name] = 0
	}
	for name, v := range existing {
		if _, ok := known[name]; !ok {
			return nil, invalidInput("existing distribution references unknown cluster %q", name)
		}
		if v < 0 {
			return nil, invalidInput("existing replicas for cluster %q must be non-negative, got %d", name, v)
		}
		cur[name] = v
	}
	return cur, nil
}

// rebalanceUnconstrained implements KeepExisting without spread constraints.
func rebalanceUnconstrained(replicas int64, parts []participant, cur, orig map[string]int64) (*RebalanceResult, error) {
	// Phase 1: hard-cap enforcement. A cluster whose capacity shrank below
	// its current load must shed the excess; these evictions are unavoidable.
	for _, p := range parts {
		if cur[p.key] > p.capacity {
			cur[p.key] = p.capacity
		}
	}

	total := sumValues(cur)

	// Phase 2: aggregate reconciliation against the requested replica count.
	switch {
	case total > replicas:
		// Downscale: remove from the most weight-over-entitled clusters first.
		evictFromClusters(parts, cur, total-replicas)
	case total < replicas:
		// Upscale: fill spare capacity by weight on unsaturated clusters.
		fillByWeight(parts, cur, replicas-total)
	}

	return buildRebalanceResult(replicas, parts, cur, orig), nil
}

// evictFromClusters removes total units from the clusters with the highest
// current-to-weight ratio (ties by ascending name).
func evictFromClusters(parts []participant, cur map[string]int64, total int64) {
	for total > 0 {
		candidates := make([]participant, 0, len(parts))
		for _, p := range parts {
			if cur[p.key] > 0 {
				candidates = append(candidates, p)
			}
		}
		sort.Slice(candidates, func(i, j int) bool {
			cmp := compareRatio(cur[candidates[i].key], candidates[i].weight,
				cur[candidates[j].key], candidates[j].weight)
			if cmp != 0 {
				return cmp > 0
			}
			return candidates[i].key < candidates[j].key
		})
		cur[candidates[0].key]--
		total--
	}
}

// fillByWeight distributes need additional replicas by weight, restricted to
// clusters that still have headroom below their hard cap.
func fillByWeight(parts []participant, cur map[string]int64, need int64) {
	if need <= 0 {
		return
	}
	effective := make([]participant, 0, len(parts))
	for _, p := range parts {
		if headroom := p.capacity - cur[p.key]; headroom > 0 {
			effective = append(effective, participant{key: p.key, weight: p.weight, capacity: headroom})
		}
	}
	base := make(map[string]int64, len(effective))
	grants, _, _ := cappedWeighted(effective, base, need)
	for k, v := range grants {
		cur[k] += v
	}
}

// compareRatio compares aNum/aDen vs bNum/bDen exactly by big.Int cross
// multiplication. Returns >0, 0 or <0.
func compareRatio(aNum, aDen, bNum, bDen int64) int {
	left := new(big.Int).Mul(big.NewInt(aNum), big.NewInt(bDen))
	right := new(big.Int).Mul(big.NewInt(bNum), big.NewInt(aDen))
	return left.Cmp(right)
}

// buildRebalanceResult assembles the result and computes migration metrics
// against the original distribution.
func buildRebalanceResult(replicas int64, parts []participant, cur, orig map[string]int64) *RebalanceResult {
	plan := make(map[string]int64, len(parts))
	var migrated, added int64
	for _, p := range parts {
		plan[p.key] = cur[p.key]
		if orig[p.key] > cur[p.key] {
			migrated += orig[p.key] - cur[p.key]
		}
		if cur[p.key] > orig[p.key] {
			added += cur[p.key] - orig[p.key]
		}
	}
	return &RebalanceResult{
		Allocation: Allocation{
			Replicas:  plan,
			Total:     replicas,
			Saturated: saturatedNames(parts, cur),
			Migrated:  migrated,
			Added:     added,
		},
		Previous:     orig,
		MinEvictions: migrated,
	}
}

// saturatedNames lists clusters pinned at their hard cap in the final plan.
func saturatedNames(parts []participant, cur map[string]int64) []string {
	var out []string
	for _, p := range parts {
		if cur[p.key] >= p.capacity && p.capacity > 0 {
			out = append(out, p.key)
		}
	}
	sort.Strings(out)
	return out
}

// sumValues sums all map values.
func sumValues(m map[string]int64) int64 {
	var s int64
	for _, v := range m {
		s += v
	}
	return s
}

// sumNamed sums the map values for the given keys.
func sumNamed(m map[string]int64, keys []string) int64 {
	var s int64
	for _, k := range keys {
		s += m[k]
	}
	return s
}

// cloneMap returns a shallow copy of m.
func cloneMap(m map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
