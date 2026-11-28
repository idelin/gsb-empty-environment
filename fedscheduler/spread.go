package fedscheduler

import (
	"sort"
)

// group is an aggregated failure domain: the union of its member clusters.
type group struct {
	key      string
	weight   int64
	capacity int64
	members  []string
}

// AllocateWithSpread splits replicas across clusters while honoring weight
// proportionality, per-cluster hard caps and the supplied spread constraints.
//
// Placement is computed in two deterministic stages: replicas are first
// apportioned across failure domains (Region or Zone) under the domain-level
// bounds, then each domain's share is apportioned across its member clusters
// under their individual caps. Both stages use the recursive overflow +
// largest-remainder algorithm, so total conservation sum(alloc) == replicas
// always holds whenever a feasible plan exists.
func AllocateWithSpread(replicas int64, clusters []Cluster, constraints []SpreadConstraint) (*Allocation, error) {
	parts, base, totalCap, err := validateClusters(replicas, clusters)
	if err != nil {
		return nil, err
	}
	if len(constraints) == 0 {
		return Allocate(replicas, clusters)
	}
	if len(constraints) > 1 {
		return nil, invalidInput("at most one spread constraint is supported per call, got %d", len(constraints))
	}

	constraint := constraints[0]
	scope, err := normalizeScope(constraint)
	if err != nil {
		return nil, err
	}

	groups, err := buildGroups(clusters, scope, constraint)
	if err != nil {
		return nil, err
	}
	groupParts := make([]participant, 0, len(groups))
	groupBase := make(map[string]int64, len(groups))
	for _, g := range groups {
		groupParts = append(groupParts, participant{key: g.key, weight: g.weight, capacity: g.capacity})
		groupBase[g.key] = 0
	}

	lower, upper, err := groupBounds(constraint, replicas, len(groups))
	if err != nil {
		return nil, err
	}

	// Aggregate capacity shortage is diagnosed before constraint
	// feasibility so callers always receive the precise capacity error and
	// the maximum schedulable plan.
	if totalCap < replicas {
		return nil, insufficientError(replicas, parts, base)
	}

	if replicas == 0 {
		if lower > 0 {
			return nil, infeasibleConstraint(replicas, lower, upper)
		}
		return &Allocation{Replicas: zeroPlan(clusters), Total: 0, Saturated: []string{}, Rounds: 0}, nil
	}

	var groupNeed int64
	switch constraint.Mode {
	case SpreadReplicasRange:
		if replicas < lower*int64(len(groups)) || replicas > upper*int64(len(groups)) {
			return nil, infeasibleConstraint(replicas, lower, upper)
		}
		if err := checkGroupFeasibility(groupParts, lower, upper, replicas); err != nil {
			return nil, err
		}
		groupNeed = replicas
	case SpreadMinGroups:
		if int64(constraint.MinGroups) > int64(len(groups)) || int64(constraint.MinGroups) > replicas {
			return nil, invalidInput(
				"minGroups %d infeasible: only %d %ss and %d replicas available",
				constraint.MinGroups, len(groups), scope, replicas,
			)
		}
		if err := checkGroupFeasibility(groupParts, 0, replicas, replicas); err != nil {
			return nil, err
		}
		groupNeed = replicas
	default:
		return nil, invalidInput("unknown spread mode %q", constraint.Mode)
	}

	groupPlan, _, groupRounds, err := apportionGroups(groupParts, groupBase, groupNeed, lower, upper, constraint)
	if err != nil {
		return nil, err
	}

	plan := make(map[string]int64, len(clusters))
	var saturated []string
	clusterRounds := 0
	for _, g := range groups {
		memberParts := make([]participant, 0, len(g.members))
		memberBase := make(map[string]int64, len(g.members))
		for _, name := range g.members {
			p, ok := findParticipant(parts, name)
			if !ok {
				return nil, invalidInput("internal error: cluster %q missing", name)
			}
			memberParts = append(memberParts, p)
			memberBase[name] = 0
		}
		subPlan, subSat, subRounds := cappedWeighted(memberParts, memberBase, groupPlan[g.key])
		for name, v := range subPlan {
			plan[name] = v
		}
		saturated = append(saturated, subSat...)
		clusterRounds += subRounds
	}

	sort.Strings(saturated)
	return &Allocation{
		Replicas:  plan,
		Total:     replicas,
		Saturated: saturated,
		Rounds:    groupRounds + clusterRounds,
	}, nil
}

// normalizeScope validates the constraint scope and returns it, defaulting to
// Region when unset.
func normalizeScope(c SpreadConstraint) (SpreadScope, error) {
	switch c.Scope {
	case "":
		return ScopeRegion, nil
	case ScopeRegion, ScopeZone:
		return c.Scope, nil
	default:
		return "", invalidInput("unknown spread scope %q", c.Scope)
	}
}

// buildGroups aggregates clusters into failure domains. Group weight is the
// sum of member weights and group capacity the sum of member capacities,
// which keeps the two-level apportionment weight-consistent.
func buildGroups(clusters []Cluster, scope SpreadScope, c SpreadConstraint) ([]group, error) {
	byKey := make(map[string]*group)
	var orderedKeys []string

	for _, cl := range clusters {
		domainKey := cl.Region
		if scope == ScopeZone {
			domainKey = cl.Zone
		}
		if domainKey == "" {
			return nil, invalidInput("cluster %q has empty %s required by the spread constraint", cl.Name, scope)
		}
		g, ok := byKey[domainKey]
		if !ok {
			g = &group{key: domainKey}
			byKey[domainKey] = g
			orderedKeys = append(orderedKeys, domainKey)
		}
		g.weight += cl.Weight
		g.capacity += cl.Capacity
		g.members = append(g.members, cl.Name)
	}

	sort.Strings(orderedKeys)
	out := make([]group, 0, len(orderedKeys))
	for _, k := range orderedKeys {
		g := byKey[k]
		sort.Strings(g.members)
		out = append(out, *g)
	}
	return out, nil
}

// groupBounds translates a constraint into the per-domain [lower, upper]
// integer bounds used by the bounded apportionment routine.
func groupBounds(c SpreadConstraint, replicas int64, groupCount int) (int64, int64, error) {
	switch c.Mode {
	case SpreadReplicasRange:
		if c.MinReplicas < 0 {
			return 0, 0, invalidInput("minReplicas must be non-negative, got %d", c.MinReplicas)
		}
		if c.MaxReplicas != 0 && c.MaxReplicas < c.MinReplicas {
			return 0, 0, invalidInput("maxReplicas %d is below minReplicas %d", c.MaxReplicas, c.MinReplicas)
		}
		upper := c.MaxReplicas
		if upper == 0 {
			upper = replicas
		}
		return c.MinReplicas, upper, nil
	case SpreadMinGroups:
		if c.MinGroups <= 0 {
			return 0, 0, invalidInput("minGroups must be positive, got %d", c.MinGroups)
		}
		return 0, replicas, nil
	default:
		return 0, 0, invalidInput("unknown spread mode %q", c.Mode)
	}
}

// infeasibleConstraint reports that no distribution can satisfy the domain
// bounds for the requested replica count.
func infeasibleConstraint(replicas, lower, upper int64) error {
	return invalidInput(
		"spread constraint infeasible for %d replicas under per-domain bounds [%d, %d]",
		replicas, lower, upper,
	)
}

// apportionGroups distributes need across domains while enforcing per-domain
// bounds. In MinGroups mode it first seeds one replica into MinGroups domains
// (the highest weight domains, ties by name) so that coverage is guaranteed,
// then runs the bounded apportionment for the remaining units.
func apportionGroups(parts []participant, base map[string]int64, need, lower, upper int64, c SpreadConstraint) (map[string]int64, []string, int, error) {
	workBase := make(map[string]int64, len(base))
	for k, v := range base {
		workBase[k] = v
	}
	workParts := make([]participant, len(parts))
	copy(workParts, parts)

	if c.Mode == SpreadMinGroups {
		seeded, err := seedMinGroups(workParts, workBase, int64(c.MinGroups))
		if err != nil {
			return nil, nil, 0, err
		}
		need -= seeded
	}

	// Enforce lower bounds first.
	if lower > 0 {
		for i := range workParts {
			mandatory := lower - workBase[workParts[i].key]
			if mandatory <= 0 {
				continue
			}
			if mandatory > workParts[i].capacity {
				return nil, nil, 0, invalidInput(
					"spread constraint infeasible: domain %q cannot hold required %d replicas (capacity %d)",
					workParts[i].key, lower, workParts[i].capacity,
				)
			}
			workBase[workParts[i].key] += mandatory
			workParts[i].capacity -= mandatory
			need -= mandatory
		}
	}

	// Shrink every effective cap so base+grant cannot exceed the upper bound.
	for i := range workParts {
		headroom := upper - workBase[workParts[i].key]
		if headroom < 0 {
			return nil, nil, 0, invalidInput(
				"spread constraint infeasible: domain %q already above maxReplicas", workParts[i].key,
			)
		}
		if workParts[i].capacity > headroom {
			workParts[i].capacity = headroom
		}
	}

	var totalCap int64
	for _, p := range workParts {
		totalCap += p.capacity
	}
	if totalCap < need {
		return nil, nil, 0, invalidInput(
			"spread constraint infeasible: remaining domain headroom %d below unplaced replicas %d",
			totalCap, need,
		)
	}

	plan, saturated, rounds := cappedWeighted(workParts, workBase, need)
	return plan, saturated, rounds, nil
}

// seedMinGroups places one replica in each of seedCount domains, choosing the
// highest-weight domains first (ties by ascending name). It returns the
// number of seeded replicas and updates parts/base in place.
func seedMinGroups(parts []participant, base map[string]int64, seedCount int64) (int64, error) {
	eligible := make([]participant, 0, len(parts))
	for _, p := range parts {
		if p.capacity >= 1 {
			eligible = append(eligible, p)
		}
	}
	if int64(len(eligible)) < seedCount {
		return 0, invalidInput(
			"minGroups infeasible: only %d domains have spare capacity, need %d",
			len(eligible), seedCount,
		)
	}
	sort.Slice(eligible, func(i, j int) bool {
		if eligible[i].weight != eligible[j].weight {
			return eligible[i].weight > eligible[j].weight
		}
		return eligible[i].key < eligible[j].key
	})

	var seeded int64
	for i := int64(0); i < seedCount; i++ {
		key := eligible[i].key
		base[key]++
		for j := range parts {
			if parts[j].key == key {
				parts[j].capacity--
				break
			}
		}
		seeded++
	}
	return seeded, nil
}

// findParticipant looks up a participant by key.
func findParticipant(parts []participant, key string) (participant, bool) {
	for _, p := range parts {
		if p.key == key {
			return p, true
		}
	}
	return participant{}, false
}

// checkGroupFeasibility verifies that domain capacities can simultaneously
// satisfy the per-domain lower bound and the aggregate request once each
// domain is clipped to its upper bound.
func checkGroupFeasibility(parts []participant, lower, upper, replicas int64) error {
	var effectiveCap int64
	for _, p := range parts {
		if p.capacity < lower {
			return invalidInput(
				"spread constraint infeasible: domain %q capacity %d below minReplicas %d",
				p.key, p.capacity, lower,
			)
		}
		capAtUpper := p.capacity
		if capAtUpper > upper {
			capAtUpper = upper
		}
		effectiveCap += capAtUpper
	}
	if effectiveCap < replicas {
		return invalidInput(
			"spread constraint infeasible: effective domain capacity %d below requested replicas %d",
			effectiveCap, replicas,
		)
	}
	return nil
}
