package starjoin

import (
	"errors"
	"fmt"
	"hash/fnv"
)

// JoinEdge describes one fact-to-dimension FK->PK join plus the
// (optional) filter predicate applied to the dimension table.
type JoinEdge struct {
	DimTable  string
	FactKey   string // FK column on the fact table
	DimKey    string // PK column on the dimension table
	DimFilter func(Row) bool
}

// BitmapJoinFilter is the rewritten form of a filtered dimension:
// the surviving dimension keys are materialized as a bitmap so the
// fact table can be probed in O(1) per row per dimension.
type BitmapJoinFilter struct {
	DimTable string
	FactKey  string
	DimKey   string
	// Hashed is true when dimension keys are non-integer and the
	// bitmap is addressed by hash(key) % Size (Bloom-style; the
	// executor removes false positives during the exact join).
	Hashed bool

	bits *Bitmap
	keys int
}

// Keys returns how many dimension keys survived the filter.
func (f *BitmapJoinFilter) Keys() int { return f.keys }

// BitmapSize returns the addressable bit count of the filter.
func (f *BitmapJoinFilter) BitmapSize() uint64 { return f.bits.Size() }

// PopCount returns the number of set bits in the filter bitmap.
func (f *BitmapJoinFilter) PopCount() uint64 { return f.bits.Count() }

// Test reports whether a fact-table FK value may join with the
// filtered dimension. A false result is always exact (the row can
// be discarded); a true result may be a false positive only when
// Hashed is true.
func (f *BitmapJoinFilter) Test(key any) bool {
	if f.Hashed {
		return f.bits.Test(hashKey(key) % f.bits.Size())
	}
	id, ok := key.(int)
	if !ok {
		return true // unknown type: do not exclude
	}
	if id < 0 {
		return false
	}
	return f.bits.Test(uint64(id))
}

func hashKey(key any) uint64 {
	h := fnv.New64a()
	fmt.Fprint(h, key)
	return h.Sum64()
}

// Plan is the optimized star-join execution plan: one pass over the
// fact table applying every BitmapJoinFilter, then an exact hash
// join for the surviving rows.
type Plan struct {
	FactTable string
	Filters   []*BitmapJoinFilter

	edges []JoinEdge
	dims  map[string]*Table

	// Execution statistics (populated by Execute).
	Scanned      int // fact rows visited
	BitmapPassed int // fact rows surviving all bitmap filters
}

// Excluded returns how many fact rows the bitmap stage discarded.
func (p *Plan) Excluded() int { return p.Scanned - p.BitmapPassed }

// ExclusionRate returns Excluded()/Scanned().
func (p *Plan) ExclusionRate() float64 {
	if p.Scanned == 0 {
		return 0
	}
	return float64(p.Excluded()) / float64(p.Scanned)
}

// BuildPlan detects the star schema: the fact table is the largest
// table that does not appear as a dimension in any edge, and every
// edge's filtered dimension is rewritten into a BitmapJoinFilter.
func BuildPlan(tables map[string]*Table, edges []JoinEdge) (*Plan, error) {
	if len(edges) == 0 {
		return nil, errors.New("starjoin: no join edges provided")
	}
	isDim := make(map[string]bool, len(edges))
	for _, e := range edges {
		isDim[e.DimTable] = true
		if _, ok := tables[e.DimTable]; !ok {
			return nil, fmt.Errorf("starjoin: unknown dimension table %q", e.DimTable)
		}
	}
	var factName string
	var fact *Table
	for name, t := range tables {
		if isDim[name] {
			continue
		}
		if fact == nil || t.Len() > fact.Len() {
			fact, factName = t, name
		}
	}
	if fact == nil {
		return nil, errors.New("starjoin: no fact table found")
	}

	p := &Plan{FactTable: factName, edges: edges, dims: tables}
	for _, e := range edges {
		f := buildFilter(tables[e.DimTable], e)
		p.Filters = append(p.Filters, f)
	}
	return p, nil
}

// buildFilter scans the dimension once, applies DimFilter and sets
// one bit per surviving key. Integer keys get an exact direct-map
// bitmap sized max(key)+1; other key types get a hashed bitmap.
func buildFilter(dim *Table, e JoinEdge) *BitmapJoinFilter {
	intKeys := true
	maxKey := -1
	for _, r := range dim.Rows {
		k, ok := r[e.DimKey].(int)
		if !ok {
			intKeys = false
			break
		}
		if k > maxKey {
			maxKey = k
		}
	}

	f := &BitmapJoinFilter{
		DimTable: e.DimTable,
		FactKey:  e.FactKey,
		DimKey:   e.DimKey,
		Hashed:   !intKeys,
	}
	if intKeys {
		f.bits = NewBitmap(uint64(maxKey + 1))
	} else {
		n := uint64(64)
		for n < uint64(len(dim.Rows))*4 {
			n *= 2
		}
		f.bits = NewBitmap(n)
	}

	for _, r := range dim.Rows {
		if e.DimFilter != nil && !e.DimFilter(r) {
			continue
		}
		f.keys++
		if intKeys {
			f.bits.Set(uint64(r[e.DimKey].(int)))
		} else {
			f.bits.Set(hashKey(r[e.DimKey]) % f.bits.Size())
		}
	}
	return f
}

// Execute runs the optimized plan: a single scan of the fact table
// applies all bitmap filters (the multi-dimensional pre-filter),
// then only the surviving rows are hash-joined with the filtered
// dimensions to produce exact results.
func (p *Plan) Execute(fact *Table) []Row {
	p.Scanned, p.BitmapPassed = 0, 0
	lookups := buildLookups(p.dims, p.edges)

	var out []Row
	dimRows := make([]Row, len(p.edges))
	for _, fr := range fact.Rows {
		p.Scanned++
		pass := true
		for _, f := range p.Filters {
			if !f.Test(fr[f.FactKey]) {
				pass = false
				break
			}
		}
		if !pass {
			continue
		}
		p.BitmapPassed++

		joinOK := true
		for i, e := range p.edges {
			dr, ok := lookups[i][fr[e.FactKey]]
			if !ok { // hashed-bitmap false positive
				joinOK = false
				break
			}
			dimRows[i] = dr
		}
		if !joinOK {
			continue
		}
		out = append(out, mergeRow(fr, p.edges, dimRows))
	}
	return out
}

// NaiveStats reports baseline join statistics.
type NaiveStats struct {
	Scanned int // fact rows visited
	Probed  int // hash-table probes performed
}

// NaiveJoin is the un-optimized baseline: a full fact-table scan
// with a hash probe per dimension for every row, no bitmap
// pre-filtering.
func NaiveJoin(fact *Table, dims map[string]*Table, edges []JoinEdge) ([]Row, NaiveStats) {
	lookups := buildLookups(dims, edges)
	var st NaiveStats
	var out []Row
	dimRows := make([]Row, len(edges))
	for _, fr := range fact.Rows {
		st.Scanned++
		joinOK := true
		for i, e := range edges {
			st.Probed++
			dr, ok := lookups[i][fr[e.FactKey]]
			if !ok {
				joinOK = false
				break
			}
			dimRows[i] = dr
		}
		if !joinOK {
			continue
		}
		out = append(out, mergeRow(fr, edges, dimRows))
	}
	return out, st
}

func buildLookups(dims map[string]*Table, edges []JoinEdge) []map[any]Row {
	lookups := make([]map[any]Row, len(edges))
	for i, e := range edges {
		lk := make(map[any]Row)
		for _, r := range dims[e.DimTable].Rows {
			if e.DimFilter != nil && !e.DimFilter(r) {
				continue
			}
			lk[r[e.DimKey]] = r
		}
		lookups[i] = lk
	}
	return lookups
}

func mergeRow(fr Row, edges []JoinEdge, dimRows []Row) Row {
	out := make(Row, len(fr)+4*len(edges))
	for k, v := range fr {
		out[k] = v
	}
	for i, e := range edges {
		for k, v := range dimRows[i] {
			out[e.DimTable+"."+k] = v
		}
	}
	return out
}
