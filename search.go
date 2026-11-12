package netsec

import (
	"math/bits"
	"sort"
)

// MinimalSetIndices enumerates every minimum-cardinality plugin combination
// whose identification rate under metric reaches threshold. The returned
// masks select plugins by Analysis index; ErrUnreachable means even the full
// combination is below the threshold.
func (a *Analysis) MinimalSetIndices(threshold float64, metric Metric) ([]uint32, error) {
	full := uint32(1)<<uint(len(a.plugins)) - 1
	bayes, singleton := a.rates(full)
	fullRate := metricRate(bayes, singleton, metric)
	if !reaches(fullRate, threshold) {
		return nil, ErrUnreachable
	}

	// The empty combination always forms the one-block partition; check it
	// explicitly before any subset search.
	if emptyBayes, emptySingleton := a.rates(0); reaches(metricRate(emptyBayes, emptySingleton, metric), threshold) {
		return []uint32{0}, nil
	}

	// Greedy supplies a feasible upper bound on the required cardinality,
	// letting the branch-and-bound search prune superset branches early.
	best := bits.OnesCount32(a.greedyIndices(threshold, metric))
	if best == 0 {
		best = len(a.plugins)
	}

	state := &searchState{
		analysis:  a,
		threshold: threshold,
		metric:    metric,
		best:      best,
	}
	state.dfs(0, 0, 0)
	sort.Slice(state.solutions, func(i, j int) bool { return state.solutions[i] < state.solutions[j] })
	return state.solutions, nil
}

// MinimalSets is MinimalSetIndices with each result translated to plugin names.
func (a *Analysis) MinimalSets(threshold float64, metric Metric) ([][]string, error) {
	masks, err := a.MinimalSetIndices(threshold, metric)
	if err != nil {
		return nil, err
	}
	sets := make([][]string, len(masks))
	for idx, mask := range masks {
		sets[idx] = a.PluginNames(mask)
	}
	return sets, nil
}

type searchState struct {
	analysis  *Analysis
	threshold float64
	metric    Metric
	best      int
	solutions []uint32
}

// dfs performs a full-subset enumeration with pruning. chosen is the mask of
// committed plugins and next is the smallest index still undecided, so every
// subset is reached exactly once. The greedy bound prunes branches that
// cannot beat the best cardinality already on record.
func (s *searchState) dfs(chosen uint32, next int, used int) {
	if used == s.best {
		bayes, singleton := s.analysis.rates(chosen)
		if reaches(metricRate(bayes, singleton, s.metric), s.threshold) {
			s.solutions = append(s.solutions, chosen)
		}
		return
	}

	remaining := s.analysis.PluginCount() - next
	if used+remaining < s.best {
		return
	}

	for plugin := next; plugin < s.analysis.PluginCount(); plugin++ {
		mask := chosen | 1<<uint(plugin)
		bayes, singleton := s.analysis.rates(mask)
		rate := metricRate(bayes, singleton, s.metric)

		// Any solution extending mask would have more than best plugins.
		if used+1 > s.best {
			continue
		}
		if reaches(rate, s.threshold) {
			if used+1 < s.best {
				s.best = used + 1
				s.solutions = s.solutions[:0]
			}
			s.solutions = append(s.solutions, mask)
			continue
		}

		// A deeper branch can still reach s.best plugins only if enough
		// undecided plugins remain.
		if plugin+1 < s.analysis.PluginCount() && used+1 < s.best {
			s.dfs(mask, plugin+1, used+1)
		}
	}
}

// greedyIndices adds at each step the plugin that most increases the current
// identification rate (ties keep the smallest index) until the threshold is
// met, producing one feasible combination rather than every optimum.
func (a *Analysis) greedyIndices(threshold float64, metric Metric) uint32 {
	var chosen uint32
	used := 0
	for used < len(a.plugins) {
		bayes, singleton := a.rates(chosen)
		if reaches(metricRate(bayes, singleton, metric), threshold) {
			break
		}

		bestPlugin := -1
		bestRate := -1.0
		for plugin := 0; plugin < len(a.plugins); plugin++ {
			if chosen&(1<<uint(plugin)) != 0 {
				continue
			}
			candidateBayes, candidateSingleton := a.rates(chosen | 1<<uint(plugin))
			rate := metricRate(candidateBayes, candidateSingleton, metric)
			if rate > bestRate {
				bestRate = rate
				bestPlugin = plugin
			}
		}
		if bestPlugin < 0 {
			break
		}
		chosen |= 1 << uint(bestPlugin)
		used++
	}
	return chosen
}

// GreedySet returns the plugin names of the greedy feasible combination.
func (a *Analysis) GreedySet(threshold float64, metric Metric) ([]string, error) {
	full := uint32(1)<<uint(len(a.plugins)) - 1
	bayes, singleton := a.rates(full)
	if !reaches(metricRate(bayes, singleton, metric), threshold) {
		return nil, ErrUnreachable
	}
	mask := a.greedyIndices(threshold, metric)
	return a.PluginNames(mask), nil
}

func metricRate(bayes, singleton float64, metric Metric) float64 {
	if metric == MetricSingleton {
		return singleton
	}
	return bayes
}
