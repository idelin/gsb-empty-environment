package hexgrid

import (
	"errors"
	"fmt"
	"sort"
)

var (
	// ErrStartBlocked is returned when the start cell is an obstacle or
	// outside the finite map.
	ErrStartBlocked = errors.New("hexgrid: start cell is not walkable")
	// ErrTargetUnreachable is returned when a target was given but not
	// reached within K steps. The partial Result is still returned.
	ErrTargetUnreachable = errors.New("hexgrid: target not reachable within K")
	// ErrInvalidQuery marks malformed queries (e.g. negative K).
	ErrInvalidQuery = errors.New("hexgrid: invalid query")
)

// Stats reports the access volume of one query.
type Stats struct {
	PagesLoaded   int `json:"pagesLoaded"`   // page fetch attempts (cache misses)
	PageRetries   int `json:"pageRetries"`   // retries after ErrMissingPage / ErrStaleVersion
	CellsExpanded int `json:"cellsExpanded"` // BFS frontier expansions
}

// Query asks for every cell reachable from Start within K steps.
// Version 0 means the latest store version. If Target is set, the result
// also carries one shortest path to it, tie-broken lexicographically.
type Query struct {
	Start   Hex
	K       int
	Target  *Hex
	Version uint64
}

// Result is the outcome of a Query.
type Result struct {
	Version   uint64
	Reachable []Hex       // all reached cells, sorted lexicographically
	Dist      map[Hex]int // shortest step count per reached cell
	Parent    map[Hex]Hex // BFS parent giving the lexicographically smallest shortest path
	Path      []Hex       // path to Query.Target, if a target was given
	Stats     Stats       // access volume of this query
}

// PathTo reconstructs the lexicographically smallest shortest path from
// the start to any reached cell.
func (r *Result) PathTo(h Hex) ([]Hex, bool) {
	if _, ok := r.Dist[h]; !ok {
		return nil, false
	}
	var rev []Hex
	for cur := h; ; {
		rev = append(rev, cur)
		p, ok := r.Parent[cur]
		if !ok {
			break
		}
		cur = p
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev, true
}

// lookupFunc answers whether a cell is walkable at the query's version.
type lookupFunc func(Hex) (bool, error)

// runQuery is the single BFS core shared by the partitioned Store and the
// flat Reference, so both are guaranteed to compute the same thing.
//
// Layer ranks implement the lexicographic tie-break: within one BFS layer,
// cells are ordered by their path from the start (compared cell by cell
// with Hex.Less). A cell in the next layer picks the reachable predecessor
// with the smallest rank; because ranks are unique per layer, ordering the
// new layer by (predecessor rank, own coordinates) yields the ranks of the
// lexicographically smallest shortest paths.
func runQuery(q Query, version uint64, lookup lookupFunc, stats *Stats) (*Result, error) {
	if q.K < 0 {
		return nil, fmt.Errorf("%w: negative K", ErrInvalidQuery)
	}
	ok, err := lookup(q.Start)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: %v", ErrStartBlocked, q.Start)
	}

	res := &Result{
		Version: version,
		Dist:    map[Hex]int{q.Start: 0},
		Parent:  map[Hex]Hex{},
	}
	layer := []Hex{q.Start}
	rank := map[Hex]int{q.Start: 0}

	for d := 1; d <= q.K && len(layer) > 0; d++ {
		type candidate struct {
			pred     Hex
			predRank int
		}
		next := map[Hex]candidate{}
		for _, cell := range layer {
			stats.CellsExpanded++
			for _, nb := range cell.Neighbors() {
				if _, seen := res.Dist[nb]; seen {
					continue
				}
				walkable, err := lookup(nb)
				if err != nil {
					return nil, err
				}
				if !walkable {
					continue
				}
				r := rank[cell]
				if cur, ok := next[nb]; !ok || r < cur.predRank {
					next[nb] = candidate{pred: cell, predRank: r}
				}
			}
		}
		ordered := make([]Hex, 0, len(next))
		for h := range next {
			ordered = append(ordered, h)
		}
		sort.Slice(ordered, func(i, j int) bool {
			a, b := next[ordered[i]], next[ordered[j]]
			if a.predRank != b.predRank {
				return a.predRank < b.predRank
			}
			return ordered[i].Less(ordered[j])
		})
		nextRank := make(map[Hex]int, len(ordered))
		for i, h := range ordered {
			res.Dist[h] = d
			res.Parent[h] = next[h].pred
			nextRank[h] = i
		}
		layer = ordered
		rank = nextRank
	}

	res.Reachable = make([]Hex, 0, len(res.Dist))
	for h := range res.Dist {
		res.Reachable = append(res.Reachable, h)
	}
	sort.Slice(res.Reachable, func(i, j int) bool { return res.Reachable[i].Less(res.Reachable[j]) })
	res.Stats = *stats

	if q.Target != nil {
		path, ok := res.PathTo(*q.Target)
		if !ok {
			return res, fmt.Errorf("%w: %v", ErrTargetUnreachable, *q.Target)
		}
		res.Path = path
	}
	return res, nil
}

// Query runs a reachability query against the partitioned store at one
// pinned version. Pages are fetched lazily and cached per query; missing
// pages and stale partitions are retried up to MaxRetries times, then the
// whole query is rejected — versions are never mixed into a map that does
// not exist.
func (s *Store) Query(q Query) (*Result, error) {
	version := q.Version
	if version == 0 {
		version = s.version
	}
	stats := &Stats{}
	cache := map[PageID]map[Hex]bool{}
	lookup := func(h Hex) (bool, error) {
		pid, gid := s.layout.Locate(h)
		cells, cached := cache[gid]
		if !cached {
			var err error
			for attempt := 0; ; attempt++ {
				stats.PagesLoaded++
				cells, err = s.fetchPage(version, pid, gid)
				if err == nil {
					break
				}
				retriable := errors.Is(err, ErrMissingPage) || errors.Is(err, ErrStaleVersion)
				if !retriable || attempt >= s.MaxRetries {
					return false, fmt.Errorf("hexgrid: loading page %v of partition %v at version %d: %w", gid, pid, version, err)
				}
				stats.PageRetries++
			}
			cache[gid] = cells
		}
		return cells[h], nil
	}
	return runQuery(q, version, lookup, stats)
}
