package gin

import (
	"sort"
)

// Match is one query candidate. Recheck is true when exact heap verification is
// required because the result is Maybe. Lossy records that the candidate came
// from a lossy page bitmap.
type Match struct {
	TID     TID
	Truth   Truth
	Recheck bool
	Lossy   bool
}

// NeedsRecheck is an alias for Recheck.
func (m Match) NeedsRecheck() bool {
	return m.Recheck
}

// Page returns the heap page containing the match.
func (m Match) Page() uint64 {
	return TIDPage(m.TID)
}

type queryConfig struct {
	maxExactTIDs int
	maxExactSet  bool
}

// QueryOption customizes one query execution.
type QueryOption func(*queryConfig)

// WithQueryMaxExactTIDs overrides the index-wide exact-TID memory limit for
// one query. Zero disables lossy conversion.
func WithQueryMaxExactTIDs(limit int) QueryOption {
	return func(config *queryConfig) {
		if limit < 0 {
			limit = 0
		}
		config.maxExactTIDs = limit
		config.maxExactSet = true
	}
}

// WithoutQueryLossyPages disables lossy conversion for one query.
func WithoutQueryLossyPages() QueryOption {
	return WithQueryMaxExactTIDs(0)
}

// Query executes a predicate scan. pred may be a Predicate, *Expr, string,
// []string, ConsistencyFunc, MapConsistencyFunc, or nil. args may contain
// QueryOption values, an int exact-TID limit, or a custom consistency function
// to combine with a string/[]string key set.
func (idx *Index) Query(pred any, args ...any) []Match {
	config := queryConfig{}
	var consistency any
	for _, arg := range args {
		switch value := arg.(type) {
		case QueryOption:
			if value != nil {
				value(&config)
			}
		case int:
			WithQueryMaxExactTIDs(value)(&config)
		case ConsistencyFunc, MapConsistencyFunc, func(KeyLookup) Truth, func(map[string]Truth) Truth:
			consistency = value
		}
	}

	var predicate Predicate
	if consistency != nil {
		keys := predicateKeySet(pred)
		predicate = Custom(keys, consistency, WithFullScan(len(keys) == 0))
	} else {
		predicate = normalizePredicate(pred, nil)
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	maxExact := idx.config.MaxExactTIDsPerScan
	if config.maxExactSet {
		maxExact = config.maxExactTIDs
	}
	return idx.queryLocked(predicate, maxExact)
}

// Search is the statically typed form of Query.
func (idx *Index) Search(predicate Predicate, options ...QueryOption) []Match {
	args := make([]any, len(options))
	for i, option := range options {
		args[i] = option
	}
	return idx.Query(predicate, args...)
}

// QueryTIDs returns only the candidate TIDs, in ascending order. Callers that
// need recheck information should use Query.
func (idx *Index) QueryTIDs(pred any, args ...any) []TID {
	matches := idx.Query(pred, args...)
	tids := make([]TID, len(matches))
	for i, match := range matches {
		tids[i] = match.TID
	}
	return tids
}

// QueryKeys combines a key set with a custom tri-state consistency function.
func (idx *Index) QueryKeys(keys []string, consistency any, args ...any) []Match {
	all := make([]any, 0, len(args)+1)
	all = append(all, consistency)
	all = append(all, args...)
	return idx.Query(keys, all...)
}

// QueryWithMaxExactTIDs executes a query with a per-scan exact-TID limit.
func (idx *Index) QueryWithMaxExactTIDs(pred any, limit int) []Match {
	return idx.Query(pred, limit)
}

// Evaluate evaluates a predicate for one exact TID without applying the lossy
// page limit. It is useful when a caller rechecks a Maybe result.
func (idx *Index) Evaluate(pred any, tid TID) Truth {
	predicate := normalizePredicate(pred, nil)
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if !idx.live[tid] {
		return False
	}
	lookup := func(key string) Truth {
		if _, ok := idx.active[tid][key]; ok {
			return True
		}
		return False
	}
	return normalizeTruth(predicate.Eval(lookup))
}

func (idx *Index) queryLocked(predicate Predicate, maxExactTIDs int) []Match {
	if predicate == nil {
		return nil
	}
	if expr, ok := predicate.(*Expr); ok && expr.Err() != nil {
		return nil
	}

	keys := predicate.ScanKeys()
	keyScans := idx.keyScansLocked(keys)

	candidates := make(map[TID]struct{})
	if predicate.ScanAll() {
		for tid, live := range idx.live {
			if live {
				candidates[tid] = struct{}{}
			}
		}
	} else {
		for _, scan := range keyScans {
			for tid := range scan.tids {
				candidates[tid] = struct{}{}
			}
		}
	}

	lossyPages := lossifyCandidates(candidates, maxExactTIDs)

	matches := make([]Match, 0, len(candidates)+len(lossyPages)*2)
	for tid := range candidates {
		lookup := func(key string) Truth {
			if scan := keyScans[key]; scan != nil {
				if _, ok := scan.tids[tid]; ok {
					return True
				}
			}
			return False
		}
		truth := normalizeTruth(predicate.Eval(lookup))
		if truth == False {
			continue
		}
		matches = append(matches, Match{
			TID:     tid,
			Truth:   truth,
			Recheck: truth == Maybe,
		})
	}

	for page := range lossyPages {
		lookup := func(key string) Truth {
			if scan := keyScans[key]; scan != nil {
				if _, ok := scan.pages[page]; ok {
					return Maybe
				}
			}
			return False
		}
		truth := normalizeTruth(predicate.Eval(lookup))
		if truth == False {
			continue
		}
		for _, tid := range idx.liveTIDsOnPageLocked(page) {
			matches = append(matches, Match{
				TID:     tid,
				Truth:   truth,
				Recheck: truth == Maybe,
				Lossy:   true,
			})
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].TID != matches[j].TID {
			return matches[i].TID < matches[j].TID
		}
		if matches[i].Recheck != matches[j].Recheck {
			return !matches[i].Recheck
		}
		return !matches[i].Lossy && matches[j].Lossy
	})
	return dedupeMatches(matches)
}

func lossifyCandidates(candidates map[TID]struct{}, maxExactTIDs int) map[uint64]struct{} {
	if maxExactTIDs <= 0 || len(candidates) <= maxExactTIDs {
		return nil
	}

	pageTIDs := make(map[uint64][]TID)
	for tid := range candidates {
		page := TIDPage(tid)
		pageTIDs[page] = append(pageTIDs[page], tid)
	}
	pages := make([]uint64, 0, len(pageTIDs))
	for page := range pageTIDs {
		pages = append(pages, page)
	}
	sort.Slice(pages, func(i, j int) bool {
		left, right := len(pageTIDs[pages[i]]), len(pageTIDs[pages[j]])
		if left != right {
			return left > right
		}
		return pages[i] < pages[j]
	})

	lossy := make(map[uint64]struct{})
	exactCount := len(candidates)
	for _, page := range pages {
		if exactCount <= maxExactTIDs {
			break
		}
		lossy[page] = struct{}{}
		for _, tid := range pageTIDs[page] {
			delete(candidates, tid)
		}
		exactCount -= len(pageTIDs[page])
	}
	return lossy
}

func dedupeMatches(matches []Match) []Match {
	if len(matches) < 2 {
		return matches
	}
	write := 1
	for read := 1; read < len(matches); read++ {
		if matches[read].TID == matches[write-1].TID {
			continue
		}
		matches[write] = matches[read]
		write++
	}
	return matches[:write]
}

func predicateKeySet(pred any) []string {
	switch value := pred.(type) {
	case Predicate:
		return value.ScanKeys()
	case *Expr:
		return value.ScanKeys()
	case string:
		return []string{value}
	case []string:
		return uniqueSortedStrings(value)
	default:
		return nil
	}
}
