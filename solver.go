package netsec

import (
	"fmt"
	"strings"
)

// Anonymize computes the optimal global-recoding generalization for cfg.
//
// It searches the product lattice of per-column ancestor levels under two
// constraints: every equivalence class with fewer than K records is withheld
// in full, and the number of withheld records never exceeds MaxSuppressed.
// Among feasible level vectors it minimizes LevelCost + SuppressionCost,
// breaking ties by the lexicographically smallest level vector. Optimality is
// established by a branch-and-bound walk and independently verified by
// exhaustive enumeration of every lattice point.
//
// ErrNoFeasiblePublication is returned when no level vector is feasible; the
// returned Result is non-nil and documents the refusal. Structurally invalid
// inputs return ErrInvalidConfig with a nil result.
func Anonymize(cfg Config) (*Result, error) {
	if err := validate(cfg); err != nil {
		return nil, err
	}

	cols := len(cfg.Taxonomies)
	heights := make([]int, cols)
	normalized := make([][]string, len(cfg.Records))
	seenIDs := make(map[string]bool, len(cfg.Records))
	for row, record := range cfg.Records {
		if len(record.Quasi) != cols {
			return nil, fmt.Errorf("%w: record %d has %d quasi values, want %d", ErrInvalidConfig, row, len(record.Quasi), cols)
		}
		if record.ID == "" {
			return nil, fmt.Errorf("%w: record at index %d has an empty id", ErrInvalidConfig, row)
		}
		if seenIDs[record.ID] {
			return nil, fmt.Errorf("%w: duplicate record id %q", ErrInvalidConfig, record.ID)
		}
		seenIDs[record.ID] = true
		values := make([]string, cols)
		for col, taxonomy := range cfg.Taxonomies {
			value := record.Quasi[col]
			if _, ok := taxonomy.ancestor[value]; !ok {
				return nil, fmt.Errorf("%w: record %q column %q has unknown value %q", ErrInvalidConfig, record.ID, taxonomy.Name, value)
			}
			values[col] = value
		}
		normalized[row] = values
	}

	for col, taxonomy := range cfg.Taxonomies {
		heights[col] = taxonomy.height
		if len(cfg.LevelCosts[col]) != taxonomy.height+1 {
			return nil, fmt.Errorf("%w: column %q needs %d level costs, got %d", ErrInvalidConfig, taxonomy.Name, taxonomy.height+1, len(cfg.LevelCosts[col]))
		}
		for level, cost := range cfg.LevelCosts[col] {
			if cost < 0 {
				return nil, fmt.Errorf("%w: negative level cost for column %q level %d", ErrInvalidConfig, taxonomy.Name, level)
			}
		}
	}

	totalPoints := 1
	for _, height := range heights {
		totalPoints *= height + 1
	}

	engine := &engine{
		heights:     heights,
		records:     normalized,
		totalPoints: totalPoints,
		cache:       make(map[string]*evaluation),
	}
	engine.cfg = cfg
	engine.stats.LatticePoints = totalPoints
	engine.minFrom = make([][]int64, cols)
	for col, costs := range cfg.LevelCosts {
		minFrom := make([]int64, len(costs))
		minimum := costs[len(costs)-1]
		for level := len(costs) - 1; level >= 0; level-- {
			if costs[level] < minimum {
				minimum = costs[level]
			}
			minFrom[level] = minimum
		}
		engine.minFrom[col] = minFrom
	}

	best := engine.prunedSearch()
	enumBest := engine.fullEnumeration()

	if best == nil {
		return engine.refusalResult(enumBest), ErrNoFeasiblePublication
	}
	if enumBest == nil {
		return nil, fmt.Errorf("netsec: internal cross-check failure: pruned search found a feasible vector but enumeration did not")
	}
	if enumBest.totalCost != best.totalCost || lexCompareInt(enumBest.level, best.level) != 0 {
		return nil, fmt.Errorf("netsec: internal cross-check failure: pruned optimum %v (cost %d) != enumerated optimum %v (cost %d)",
			best.level, best.totalCost, enumBest.level, enumBest.totalCost)
	}

	accounted := engine.stats.Evaluated + engine.stats.BoundPruned
	if accounted != totalPoints {
		return nil, fmt.Errorf("netsec: internal search bookkeeping failure: %d evaluated/pruned points != %d lattice points", accounted, totalPoints)
	}
	if engine.enumerated != totalPoints {
		return nil, fmt.Errorf("netsec: internal cross-check failure: enumerated %d of %d lattice points", engine.enumerated, totalPoints)
	}

	return engine.publishResult(best), nil
}

func validate(cfg Config) error {
	cols := len(cfg.Taxonomies)
	if cols == 0 || cols > MaxQuasiIdentifiers {
		return fmt.Errorf("%w: need 1..%d quasi-identifier columns, got %d", ErrInvalidConfig, MaxQuasiIdentifiers, cols)
	}
	if len(cfg.LevelCosts) != cols {
		return fmt.Errorf("%w: %d level-cost rows for %d columns", ErrInvalidConfig, len(cfg.LevelCosts), cols)
	}
	if cfg.K < 2 {
		return fmt.Errorf("%w: k must be at least 2, got %d", ErrInvalidConfig, cfg.K)
	}
	if cfg.MaxSuppressed < 0 {
		return fmt.Errorf("%w: suppression budget must be non-negative, got %d", ErrInvalidConfig, cfg.MaxSuppressed)
	}
	if cfg.SuppressionCost < 0 {
		return fmt.Errorf("%w: suppression cost must be non-negative", ErrInvalidConfig)
	}
	if len(cfg.Records) == 0 {
		return fmt.Errorf("%w: no records supplied", ErrInvalidConfig)
	}
	for col, taxonomy := range cfg.Taxonomies {
		if taxonomy == nil {
			return fmt.Errorf("%w: column %d has no taxonomy", ErrInvalidConfig, col)
		}
	}
	return nil
}

type evaluation struct {
	level      []int
	buckets    map[string]*bucket
	levelCost  int64
	suppressed int
	suppCost   int64
	totalCost  int64
	feasible   bool
}

type bucket struct {
	key       []string
	recordIdx []int
}

type engine struct {
	cfg         Config
	heights     []int
	records     [][]string
	totalPoints int
	minFrom     [][]int64

	stats        SearchStats
	enumerated   int
	best         *evaluation
	bestCost     int64
	enumBest     *evaluation
	enumBestCost int64
	cache        map[string]*evaluation
}

func (e *engine) evaluate(level []int) *evaluation {
	key := levelKey(level)
	if cached, ok := e.cache[key]; ok {
		return cached
	}

	buckets := make(map[string]*bucket)
	var levelCost int64
	for col, l := range level {
		levelCost += e.cfg.LevelCosts[col][l]
	}

	for row, values := range e.records {
		generalized := make([]string, len(level))
		var builder strings.Builder
		for col, value := range values {
			node, err := e.cfg.Taxonomies[col].generalizeAt(value, level[col])
			if err != nil {
				panic(err)
			}
			generalized[col] = node
			builder.WriteString(node)
			builder.WriteByte(0)
		}
		bucketKey := builder.String()
		group, ok := buckets[bucketKey]
		if !ok {
			group = &bucket{key: generalized}
			buckets[bucketKey] = group
		}
		group.recordIdx = append(group.recordIdx, row)
	}

	suppressed := 0
	for _, group := range buckets {
		if len(group.recordIdx) < e.cfg.K {
			suppressed += len(group.recordIdx)
		}
	}
	suppCost := int64(suppressed) * e.cfg.SuppressionCost
	ev := &evaluation{
		level:      append([]int(nil), level...),
		buckets:    buckets,
		levelCost:  levelCost,
		suppressed: suppressed,
		suppCost:   suppCost,
		totalCost:  levelCost + suppCost,
		feasible:   suppressed <= e.cfg.MaxSuppressed,
	}
	e.cache[key] = ev
	return ev
}

// prunedSearch performs the canonical DFS of the level lattice. Each point is
// reached exactly once via edges that never lower the first-changed column,
// which keeps the evaluated/pruned accounting exact.
func (e *engine) prunedSearch() *evaluation {
	zero := make([]int, len(e.heights))
	e.dfsPruned(zero, 0)
	return e.best
}

func (e *engine) dfsPruned(level []int, startDim int) {
	ev := e.evaluate(level)
	e.stats.Evaluated++

	// Every descendant pays at least this level cost. If it already exceeds
	// the incumbent's total cost, no descendant can improve the objective.
	// Suppression is deliberately excluded from the bound: the caller's level
	// costs are not assumed monotonic, so pruning stays conservative and sound.
	if e.best != nil && e.levelCostLowerBound(ev.level, startDim) > e.bestCost {
		e.stats.BoundPruned += e.countSubtree(level, startDim)
		return
	}

	if ev.feasible {
		if e.best == nil || ev.totalCost < e.bestCost ||
			(ev.totalCost == e.bestCost && lexCompareInt(ev.level, e.best.level) < 0) {
			e.best = ev
			e.bestCost = ev.totalCost
		}
	}

	for col := startDim; col < len(e.heights); col++ {
		if level[col] < e.heights[col] {
			child := append([]int(nil), level...)
			child[col]++
			e.dfsPruned(child, col)
		}
	}
}

// levelCostLowerBound is the smallest level cost reachable inside the
// subtree. Columns before startDim are fixed; the rest use suffix minimums of
// the caller-supplied costs.
func (e *engine) levelCostLowerBound(level []int, startDim int) int64 {
	var bound int64
	for col := 0; col < startDim; col++ {
		bound += e.cfg.LevelCosts[col][level[col]]
	}
	for col := startDim; col < len(e.heights); col++ {
		bound += e.minFrom[col][level[col]]
	}
	return bound
}

// countSubtree returns the number of lattice points below level in the
// canonical DFS, excluding level itself. It walks exactly the same edges as
// dfsPruned, so pruned-point accounting matches the traversal even when a
// column has already reached its tree height.
func (e *engine) countSubtree(level []int, startDim int) int {
	count := 0
	for col := startDim; col < len(e.heights); col++ {
		if level[col] < e.heights[col] {
			child := append([]int(nil), level...)
			child[col]++
			count += 1 + e.countSubtree(child, col)
		}
	}
	return count
}

func (e *engine) fullEnumeration() *evaluation {
	zero := make([]int, len(e.heights))
	e.dfsEnumerate(zero, 0)
	return e.enumBest
}

func (e *engine) dfsEnumerate(level []int, startDim int) {
	ev := e.evaluate(level)
	e.enumerated++
	if ev.feasible && (e.enumBest == nil || ev.totalCost < e.enumBestCost ||
		(ev.totalCost == e.enumBestCost && lexCompareInt(ev.level, e.enumBest.level) < 0)) {
		e.enumBest = ev
		e.enumBestCost = ev.totalCost
	}
	for col := startDim; col < len(e.heights); col++ {
		if level[col] < e.heights[col] {
			child := append([]int(nil), level...)
			child[col]++
			e.dfsEnumerate(child, col)
		}
	}
}
