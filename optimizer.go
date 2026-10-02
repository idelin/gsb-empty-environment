package starjoin

import "fmt"

// JoinSpec declares one edge of the star: a dimension table joined to the
// fact table, plus the filter predicate applied to the dimension.
type JoinSpec struct {
	Dim       *Table    // dimension table (small)
	DimPK     string    // unique primary-key column of the dimension (int)
	FactFK    string    // foreign-key column of the fact table referencing DimPK
	Predicate Predicate // filter pushed down onto the dimension
}

// StarQuery is a central fact table joined to many dimensions.
type StarQuery struct {
	Fact  *Table
	Joins []JoinSpec
}

// BitmapJoinFilter is the physical filter built from one dimension: a bitmap
// over the dimension PK domain marking keys that survive the predicate.
type BitmapJoinFilter struct {
	DimName     string
	FactFK      string
	DimRows     int // total dimension rows
	MatchRows   int // dimension rows passing the predicate
	Selectivity float64
	bitmap      *Bitmap
	pkToRow     map[int64]int // matched PK -> dimension row index (for final join)
}

// Test reports whether a fact-row foreign key passes this filter.
func (f *BitmapJoinFilter) Test(fk int64) bool { return f.bitmap.Test(int(fk)) }

// Plan is the optimized physical plan: build-side bitmap filters plus a
// single filtered scan of the fact table.
type Plan struct {
	query   *StarQuery
	Filters []*BitmapJoinFilter
}

// Optimize validates the star shape and builds one bitmap join filter per
// dimension. It returns an error if the query is not a star pattern (fact
// table not the largest, non-unique dimension PK, or missing columns).
func Optimize(q *StarQuery) (*Plan, error) {
	if q.Fact == nil {
		return nil, fmt.Errorf("starjoin: nil fact table")
	}
	if len(q.Joins) == 0 {
		return nil, fmt.Errorf("starjoin: no dimension joins")
	}
	p := &Plan{query: q}
	for j := range q.Joins {
		spec := &q.Joins[j]
		if spec.Dim == nil {
			return nil, fmt.Errorf("starjoin: nil dimension table in join %d", j)
		}
		// Star-shape heuristic: the fact table must dominate every dimension.
		if spec.Dim.NRows > q.Fact.NRows {
			return nil, fmt.Errorf("starjoin: %s (%d rows) is larger than fact %s (%d rows): not a star pattern",
				spec.Dim.Name, spec.Dim.NRows, q.Fact.Name, q.Fact.NRows)
		}
		pk := spec.Dim.Col(spec.DimPK)
		if pk == nil || pk.Kind != IntCol {
			return nil, fmt.Errorf("starjoin: dimension %s lacks int primary key %q", spec.Dim.Name, spec.DimPK)
		}
		fk := q.Fact.Col(spec.FactFK)
		if fk == nil || fk.Kind != IntCol {
			return nil, fmt.Errorf("starjoin: fact %s lacks int foreign key %q", q.Fact.Name, spec.FactFK)
		}
		// Dimension PK must be unique for bitmap semijoin semantics.
		var maxPK int64
		seen := make(map[int64]struct{}, spec.Dim.NRows)
		for _, v := range pk.Ints {
			if _, dup := seen[v]; dup {
				return nil, fmt.Errorf("starjoin: duplicate primary key %d in dimension %s", v, spec.Dim.Name)
			}
			seen[v] = struct{}{}
			if v > maxPK {
				maxPK = v
			}
		}
		// Build phase: scan the small dimension once, evaluate the pushed-down
		// predicate, and light up matching PK bits.
		f := &BitmapJoinFilter{
			DimName: spec.Dim.Name,
			FactFK:  spec.FactFK,
			DimRows: spec.Dim.NRows,
			bitmap:  NewBitmap(int(maxPK) + 1),
			pkToRow: make(map[int64]int),
		}
		for i := 0; i < spec.Dim.NRows; i++ {
			if spec.Predicate == nil || spec.Predicate(spec.Dim, i) {
				f.bitmap.Set(int(pk.Ints[i]))
				f.pkToRow[pk.Ints[i]] = i
				f.MatchRows++
			}
		}
		f.Selectivity = float64(f.MatchRows) / float64(f.DimRows)
		p.Filters = append(p.Filters, f)
	}
	return p, nil
}

// JoinedRow is one output tuple: the fact row index and, per join, the
// matched dimension row index.
type JoinedRow struct {
	FactRow int
	DimRows []int
}

// Stats captures the semijoin-reduction effectiveness of a plan execution.
type Stats struct {
	DimRowsScanned  int     // rows read while building bitmap filters
	FactRowsScanned int     // fact rows probed against the bitmaps (always all)
	RowsAfterFilter int     // fact rows surviving the combined bitmap filter
	Eliminated      int     // fact rows discarded without any join work
	EliminatedPct   float64 // Eliminated / FactRowsScanned * 100
	OutputRows      int     // final joined tuples
}

// Execute runs the optimized plan: one pass over the fact table probing all
// bitmap filters, then a hash join only for the surviving rows.
func (p *Plan) Execute() *Result {
	q := p.query
	res := &Result{Rows: make([]JoinedRow, 0, 1024)}
	for _, f := range p.Filters {
		res.Stats.DimRowsScanned += f.DimRows
	}
	res.Stats.FactRowsScanned = q.Fact.NRows

	fkCols := make([][]int64, len(p.Filters))
	for i, f := range p.Filters {
		fkCols[i] = q.Fact.Col(f.FactFK).Ints
	}

	// Probe phase: combined multi-dimensional bitmap filter, single scan.
	survivors := make([]int, 0, 1024)
	for r := 0; r < q.Fact.NRows; r++ {
		pass := true
		for j, f := range p.Filters {
			if !f.Test(fkCols[j][r]) {
				pass = false
				break
			}
		}
		if pass {
			survivors = append(survivors, r)
		}
	}
	res.Stats.RowsAfterFilter = len(survivors)
	res.Stats.Eliminated = q.Fact.NRows - len(survivors)
	if q.Fact.NRows > 0 {
		res.Stats.EliminatedPct = 100 * float64(res.Stats.Eliminated) / float64(q.Fact.NRows)
	}

	// Join phase: hash-join only the survivors against matched dim rows.
	for _, r := range survivors {
		jr := JoinedRow{FactRow: r, DimRows: make([]int, len(p.Filters))}
		for j, f := range p.Filters {
			jr.DimRows[j] = f.pkToRow[fkCols[j][r]]
		}
		res.Rows = append(res.Rows, jr)
	}
	res.Stats.OutputRows = len(res.Rows)
	return res
}

// Result is the output of executing a plan.
type Result struct {
	Rows  []JoinedRow
	Stats Stats
}

// ExecuteNaive runs the unoptimized plan: full hash join of every fact row
// against every dimension, applying dimension predicates only after joining.
// It exists as a correctness oracle and cost baseline for the optimizer.
func ExecuteNaive(q *StarQuery) *Result {
	res := &Result{}
	joins := make([]struct {
		pkToRow map[int64]int
		fk      []int64
		pred    Predicate
		dim     *Table
	}, len(q.Joins))
	for j, spec := range q.Joins {
		m := make(map[int64]int, spec.Dim.NRows)
		for i, v := range spec.Dim.Col(spec.DimPK).Ints {
			m[v] = i
		}
		joins[j].pkToRow = m
		joins[j].fk = q.Fact.Col(spec.FactFK).Ints
		joins[j].pred = spec.Predicate
		joins[j].dim = spec.Dim
		res.Stats.DimRowsScanned += spec.Dim.NRows
	}
	res.Stats.FactRowsScanned = q.Fact.NRows
	// Every fact row is joined (hash probe + tuple materialization) before
	// any predicate is applied: no early elimination.
	for r := 0; r < q.Fact.NRows; r++ {
		jr := JoinedRow{FactRow: r, DimRows: make([]int, len(joins))}
		ok := true
		for j := range joins {
			dimRow, hit := joins[j].pkToRow[joins[j].fk[r]]
			if !hit {
				ok = false
				break
			}
			jr.DimRows[j] = dimRow
		}
		if !ok {
			continue
		}
		res.Stats.RowsAfterFilter++ // joined tuples produced pre-predicate
		for j := range joins {
			if p := joins[j].pred; p != nil && !p(joins[j].dim, jr.DimRows[j]) {
				ok = false
				break
			}
		}
		if ok {
			res.Rows = append(res.Rows, jr)
		}
	}
	res.Stats.Eliminated = 0
	res.Stats.OutputRows = len(res.Rows)
	return res
}
