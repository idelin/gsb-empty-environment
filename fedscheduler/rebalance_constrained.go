package fedscheduler

// rebalanceConstrained implements KeepExisting under one spread constraint.
//
// The invariant maintained throughout is
//
//	sum(cur) + pool == replicas
//
// where pool counts replicas that have already been evicted and are waiting
// to be re-placed. Every eviction (cap shrink, domain upper bound, downscale,
// domain coverage) grows the pool; every placement consumes the pool first and
// only then adds brand-new replicas. This guarantees that a replica is moved
// only when the constraints leave no alternative, which is exactly the
// minimum-migration objective.
func rebalanceConstrained(replicas int64, clusters []Cluster, parts []participant, cur, orig map[string]int64, c SpreadConstraint) (*RebalanceResult, error) {
	scope, err := normalizeScope(c)
	if err != nil {
		return nil, err
	}
	groups, err := buildGroups(clusters, scope, c)
	if err != nil {
		return nil, err
	}
	lower, upper, err := groupBounds(c, replicas, len(groups))
	if err != nil {
		return nil, err
	}

	partByName := make(map[string]participant, len(parts))
	for _, p := range parts {
		partByName[p.key] = p
	}

	// Phase 1: enforce every cluster's hard cap. Shed replicas enter the pool.
	domainCur := make(map[string]int64, len(groups))
	var pool, evicted int64
	for _, g := range groups {
		for _, name := range g.members {
			p := partByName[name]
			if cur[name] > p.capacity {
				shed := cur[name] - p.capacity
				pool += shed
				evicted += shed
				cur[name] = p.capacity
			}
			domainCur[g.key] += cur[name]
		}
	}

	groupParts := make([]participant, 0, len(groups))
	for _, g := range groups {
		groupParts = append(groupParts, participant{key: g.key, weight: g.weight, capacity: g.capacity})
	}

	// Replicas not yet materialized anywhere (brand-new additions) are the
	// difference between R and placed+pooled replicas.
	unplaced := replicas - sumValues(domainCur) - pool

	// Phase 2: ReplicasRange domain upper bounds.
	if c.Mode == SpreadReplicasRange {
		for _, g := range groups {
			if domainCur[g.key] > upper {
				removed := removeFromGroup(g, partByName, cur, domainCur[g.key]-upper)
				domainCur[g.key] -= removed
				pool += removed
				evicted += removed
			}
		}
	}

	// Phase 3: ReplicasRange domain lower bounds. Deficient domains first use
	// their own headroom; if none exists, one replica is evicted from a donor
	// domain and immediately re-placed here.
	if c.Mode == SpreadReplicasRange && lower > 0 {
		for _, g := range groups {
			for domainCur[g.key] < lower {
				if placed, ok := addWithinGroup(g, partByName, cur, 1); ok && placed == 1 {
					domainCur[g.key]++
					if pool > 0 {
						pool--
					} else {
						unplaced--
					}
					continue
				}
				donor := pickDonorGroup(groups, domainCur, lower, g.key)
				if donor == "" {
					return nil, infeasibleConstraint(replicas, lower, upper)
				}
				donorGroup := findGroup(groups, donor)
				if removed := removeFromGroup(donorGroup, partByName, cur, 1); removed != 1 {
					return nil, infeasibleConstraint(replicas, lower, upper)
				}
				domainCur[donor]--
				pool++
				evicted++
				if placed, ok := addWithinGroup(g, partByName, cur, 1); !ok || placed != 1 {
					return nil, infeasibleConstraint(replicas, lower, upper)
				}
				domainCur[g.key]++
				pool--
			}
		}
	}

	// Phase 4: MinGroups coverage. Empty domains with capacity are seeded
	// using pool replicas first (a migration) and only then new replicas.
	if c.Mode == SpreadMinGroups {
		capableDomains := 0
		for _, g := range groups {
			if g.capacity >= 1 {
				capableDomains++
			}
		}
		if c.MinGroups > capableDomains || int64(c.MinGroups) > replicas {
			return nil, invalidInput(
				"minGroups %d infeasible: %d capable domains, %d replicas requested",
				c.MinGroups, capableDomains, replicas,
			)
		}
		for usedDomains(domainCur) < c.MinGroups {
			target := pickCoverageTarget(groups, domainCur)
			if target == "" {
				return nil, invalidInput("minGroups %d infeasible under current capacity", c.MinGroups)
			}
			g := findGroup(groups, target)
			// Fund the seed with the cheapest available source:
			//  1. a pooled, already-evicted replica (no new eviction);
			//  2. a brand-new unplaced replica (no eviction at all);
			//  3. an eviction from a donor domain holding >= 2 replicas,
			//     which exists by the pigeonhole principle whenever neither
			//     pool nor unplaced replicas remain (running == R >= groups).
			if pool == 0 && unplaced == 0 {
				donor := pickCoverageDonor(groups, domainCur, target)
				if donor == "" {
					return nil, invalidInput("minGroups %d infeasible: no donor replica available", c.MinGroups)
				}
				donorGroup := findGroup(groups, donor)
				if removed := removeFromGroup(donorGroup, partByName, cur, 1); removed != 1 {
					return nil, invalidInput("minGroups %d infeasible: donor eviction failed", c.MinGroups)
				}
				domainCur[donor]--
				evicted++
			}
			placed, ok := addWithinGroup(g, partByName, cur, 1)
			if !ok || placed != 1 {
				return nil, invalidInput("minGroups %d infeasible under current capacity", c.MinGroups)
			}
			domainCur[target] += placed
			if pool > 0 {
				pool--
			} else if unplaced > 0 {
				unplaced--
			}
		}
	}

	// Phase 5: reconcile the aggregate. Any downscale is taken from running
	// replicas; upscaling re-places pooled replicas first and then distributes
	// genuinely new replicas by weight under the domain upper bound.
	placedTotal := sumValues(domainCur)
	// First discard surplus when running + pooled replicas exceed R: the
	// pooled surplus is dropped for free (its eviction is already counted),
	// and any remaining surplus forces further evictions from running
	// replicas.
	if placedTotal+pool > replicas {
		excess := placedTotal + pool - replicas
		if excess <= pool {
			// Downscale is fully absorbed by dropping surplus pooled
			// replicas; those evictions are not migrations.
			pool -= excess
		} else {
			fromRunning := excess - pool
			removalFloor := lower
			if c.Mode == SpreadMinGroups {
				// Every covered domain must keep at least its single seed.
				removalFloor = 1
			}
			removeAcrossGroups(groups, domainCur, partByName, cur, fromRunning, removalFloor)
			evicted += fromRunning
			pool = 0
		}
		placedTotal = sumValues(domainCur)
	}

	// Everything still in the pool must be re-placed, together with the
	// genuinely new replicas needed to reach R.
	toPlace := replicas - placedTotal
	if toPlace < 0 {
		return nil, invalidInput("internal error: negative placement delta %d", toPlace)
	}
	if toPlace > 0 {
		if toPlace < pool {
			// More pooled replicas than placement slots: the extra pooled
			// replicas are dropped by the downscale and are not migrations.
			pool = toPlace
		}
		groupBase := cloneMap(domainCur)
		// Distribute only the relative headroom: zero-base participants with
		// relative capacities, then add grants on top of the immovable
		// domainCur floor (which preserves MinGroups coverage).
		effective := make([]participant, 0, len(groupParts))
		for _, p := range groupParts {
			absoluteCap := p.capacity
			if absoluteCap > upper {
				absoluteCap = upper
			}
			headroom := absoluteCap - groupBase[p.key]
			if headroom > 0 {
				effective = append(effective, participant{
					key:      p.key,
					weight:   p.weight,
					capacity: headroom,
				})
			}
		}
		var headroomCap int64
		for _, p := range effective {
			headroomCap += p.capacity
		}
		if headroomCap < toPlace {
			return nil, invalidInput("spread constraint infeasible: domain headroom %d below replicas to place %d", headroomCap, toPlace)
		}
		zeroBase := make(map[string]int64, len(effective))
		for _, p := range effective {
			zeroBase[p.key] = 0
		}
		grants, _, _ := cappedWeighted(effective, zeroBase, toPlace)
		for _, g := range groups {
			groupDelta := grants[g.key]
			if groupDelta > 0 {
				placed, ok := addWithinGroup(g, partByName, cur, groupDelta)
				if !ok || placed != groupDelta {
					return nil, infeasibleConstraint(replicas, lower, upper)
				}
				domainCur[g.key] += placed
			}
		}
	}
	// All pooled replicas that survive downscale are re-placed in this final
	// weighted fill; brand-new replicas (toPlace - pool) displace nothing.
	pool = 0
	unplaced = 0

	if sumValues(cur) != replicas {
		return nil, invalidInput("internal error: rebalance produced sum %d, want %d", sumValues(cur), replicas)
	}

	result := buildRebalanceResult(replicas, parts, cur, orig)
	// Migration count is defined as sum_i max(0, existing_i - new_i): every
	// evicted replica counts, including surplus replicas that a downscale
	// drops rather than re-places. evicted therefore equals both the actual
	// migration count and the constraint-forced minimum.
	result.Migrated = evicted
	result.MinEvictions = evicted
	return result, nil
}

// pickCoverageDonor chooses a domain (other than the empty target) holding at
// least two replicas, so donating one leaves the donor non-empty. The most
// weight-over-entitled domain is chosen first, ties by ascending name.
func pickCoverageDonor(groups []group, domainCur map[string]int64, exclude string) string {
	var donor string
	for _, g := range groups {
		if g.key == exclude || domainCur[g.key] < 2 {
			continue
		}
		if donor == "" {
			donor = g.key
			continue
		}
		donorGroup := findGroup(groups, donor)
		cmp := compareRatio(domainCur[g.key], g.weight, domainCur[donor], donorGroup.weight)
		if cmp > 0 || (cmp == 0 && g.key < donor) {
			donor = g.key
		}
	}
	return donor
}

// removeFromGroup evicts units from clusters inside one domain, choosing the
// most weight-over-entitled clusters first. It returns the removed count.
func removeFromGroup(g group, partByName map[string]participant, cur map[string]int64, units int64) int64 {
	memberParts := make([]participant, 0, len(g.members))
	for _, name := range g.members {
		memberParts = append(memberParts, partByName[name])
	}
	before := sumNamed(cur, g.members)
	evictFromClusters(memberParts, cur, units)
	return before - sumNamed(cur, g.members)
}

// addWithinGroup places units into clusters inside one domain by weight on
// unsaturated clusters. It returns the added count and whether the full
// request was satisfied.
func addWithinGroup(g group, partByName map[string]participant, cur map[string]int64, units int64) (int64, bool) {
	memberParts := make([]participant, 0, len(g.members))
	for _, name := range g.members {
		memberParts = append(memberParts, partByName[name])
	}
	var headroom int64
	for _, p := range memberParts {
		if room := p.capacity - cur[p.key]; room > 0 {
			headroom += room
		}
	}
	if headroom == 0 {
		return 0, false
	}
	placed := units
	if placed > headroom {
		placed = headroom
	}
	fillByWeight(memberParts, cur, placed)
	return placed, placed == units
}

// pickDonorGroup selects a domain (other than exclude) that can give up one
// replica while remaining at or above lower. Domains with the highest
// current-to-weight ratio are chosen first, ties by ascending name.
func pickDonorGroup(groups []group, domainCur map[string]int64, lower int64, exclude string) string {
	var donor string
	for _, g := range groups {
		if g.key == exclude || domainCur[g.key] <= lower {
			continue
		}
		if donor == "" {
			donor = g.key
			continue
		}
		donorGroup := findGroup(groups, donor)
		cmp := compareRatio(domainCur[g.key], g.weight, domainCur[donor], donorGroup.weight)
		if cmp > 0 || (cmp == 0 && g.key < donor) {
			donor = g.key
		}
	}
	return donor
}

// usedDomains counts domains currently carrying at least one replica.
func usedDomains(domainCur map[string]int64) int {
	used := 0
	for _, v := range domainCur {
		if v > 0 {
			used++
		}
	}
	return used
}

// pickCoverageTarget chooses the highest-weight currently empty domain with
// spare capacity (ties by ascending name).
func pickCoverageTarget(groups []group, domainCur map[string]int64) string {
	var target string
	var bestWeight int64
	for _, g := range groups {
		if domainCur[g.key] != 0 || g.capacity < 1 {
			continue
		}
		if target == "" || g.weight > bestWeight || (g.weight == bestWeight && g.key < target) {
			target = g.key
			bestWeight = g.weight
		}
	}
	return target
}

// removeAcrossGroups performs a downscale by repeatedly evicting one replica
// from the most weight-over-entitled domain that still exceeds its lower
// bound, then evicting inside that domain from its most over-entitled cluster.
func removeAcrossGroups(groups []group, domainCur map[string]int64, partByName map[string]participant, cur map[string]int64, units, lower int64) {
	for units > 0 {
		var target string
		for _, g := range groups {
			if domainCur[g.key] <= lower {
				continue
			}
			if target == "" {
				target = g.key
				continue
			}
			tg := findGroup(groups, target)
			cmp := compareRatio(domainCur[g.key], g.weight, domainCur[target], tg.weight)
			if cmp > 0 || (cmp == 0 && g.key < target) {
				target = g.key
			}
		}
		if target == "" {
			// Feasible inputs never reach this branch.
			return
		}
		g := findGroup(groups, target)
		removed := removeFromGroup(g, partByName, cur, 1)
		domainCur[target] -= removed
		units -= removed
	}
}

// findGroup looks up a group by key.
func findGroup(groups []group, key string) group {
	for _, g := range groups {
		if g.key == key {
			return g
		}
	}
	return group{}
}
