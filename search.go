package mvrewrite

import (
	"errors"
	"fmt"
	"sort"
)

// SearchOptions controls finite counterexample enumeration.
type SearchOptions struct {
	// MaxRowsPerTable is the per-table upper bound; values above 2 are not
	// needed for the package's small-witness target and are clamped to 2.
	MaxRowsPerTable int `json:"max_rows_per_table"`
	// Domain is the base non-NULL finite domain.  Defaults to {0,1}.
	Domain []any `json:"domain,omitempty"`
	// ExtraDomain adds values, such as query constants or boundaries.
	ExtraDomain []any `json:"extra_domain,omitempty"`
	// MaxModels bounds complete database evaluations.
	MaxModels int `json:"max_models"`
	// Minimize greedily removes rows while the mismatch remains.
	Minimize bool `json:"minimize"`
}

// ErrSearchBudget indicates enumeration stopped at MaxModels without finding a
// concrete counterexample.  Structural results may still be returned.
var ErrSearchBudget = errors.New("mvrewrite: finite search model budget exhausted")

// DefaultSearchOptions returns at-most-two rows/table enumeration.
func DefaultSearchOptions() SearchOptions {
	return SearchOptions{MaxRowsPerTable: 2, Domain: []any{int64(0), int64(1)}, MaxModels: 50000, Minimize: true}
}

func normalizeSearch(o *SearchOptions) {
	if o.MaxRowsPerTable <= 0 || o.MaxRowsPerTable > 2 {
		o.MaxRowsPerTable = 2
	}
	if len(o.Domain) == 0 {
		o.Domain = []any{int64(0), int64(1)}
	}
	if o.MaxModels <= 0 {
		o.MaxModels = 50000
	}
}

// FindRewriteCounterexample exhaustively searches small legal instances for a
// query/view/compensation semantic mismatch.
func FindRewriteCounterexample(catalog *Catalog, q *Query, v *View, comp Compensation, opts SearchOptions) (*Counterexample, *ExhaustiveReport, error) {
	normalizeSearch(&opts)
	comp.SQL = comp.Filter.SQL()
	check := func(db *Database) (*Counterexample, error) {
		return CheckResult(catalog, q, v, comp, db)
	}
	cx, report, err := enumerateCounterexample(catalog, q, v, comp.Project, &comp, opts, check)
	if cx != nil && opts.Minimize {
		cx = minimizeCounterexample(cx, check)
		fillRowCounts(report, cx.Database)
	}
	if report != nil {
		report.MaxRowsPerTable = opts.MaxRowsPerTable
		report.Domain = publicDomain(opts)
	}
	return cx, report, err
}

// FindCoverageCounterexample searches for a query answer that the view cannot
// produce even before compensation.
func FindCoverageCounterexample(catalog *Catalog, q *Query, v *View, opts SearchOptions) (*Counterexample, *ExhaustiveReport, error) {
	normalizeSearch(&opts)
	check := func(db *Database) (*Counterexample, error) {
		return CheckCoverage(catalog, q, v, db)
	}
	cx, report, err := enumerateCounterexample(catalog, q, v, q.Select, nil, opts, check)
	if cx != nil && opts.Minimize {
		cx = minimizeCounterexample(cx, check)
		fillRowCounts(report, cx.Database)
	}
	if report != nil {
		report.MaxRowsPerTable = opts.MaxRowsPerTable
		report.Domain = publicDomain(opts)
	}
	return cx, report, err
}

// FindQueryWitness finds a smallest legal instance on which the query emits
// at least one answer.  It is used when a required projected column is absent
// from a view and ordinary result projection cannot be evaluated.
func FindQueryWitness(catalog *Catalog, q *Query, v *View, opts SearchOptions) (*Counterexample, *ExhaustiveReport, error) {
	normalizeSearch(&opts)
	check := func(db *Database) (*Counterexample, error) {
		evalQ := q
		if q.Schema == nil && catalog != nil {
			qc := *q
			qc.Schema = catalog
			evalQ = &qc
		}
		rs, err := EvalQuery(evalQ, db)
		if err != nil {
			return nil, err
		}
		if len(rs.Tuples) == 0 {
			return nil, nil
		}
		return &Counterexample{
			Kind:        ReasonMissingOutput,
			Description: "query has an answer on this instance, but the view does not export a column required for projection or compensation",
			Database:    db,
			QueryRows:   resultPublicRows(rs),
			QueryKeys:   rs.Keys(),
		}, nil
	}
	cx, report, err := enumerateCounterexample(catalog, q, v, q.Select, nil, opts, check)
	if cx != nil && opts.Minimize {
		cx = minimizeCounterexample(cx, check)
		fillRowCounts(report, cx.Database)
	}
	return cx, report, err
}

type searchSchema struct {
	physicalTables []string
	columns        map[string][]string
	specs          map[string]map[string]ColumnSpec
	aliasPhysical  map[string]string
	constants      map[ColRef][]any
}

func buildSearchSchema(catalog *Catalog, q *Query, v *View, comp *Compensation) searchSchema {
	ss := searchSchema{
		columns:       map[string][]string{},
		specs:         map[string]map[string]ColumnSpec{},
		aliasPhysical: map[string]string{},
		constants:     map[ColRef][]any{},
	}
	scanSet := map[string]struct{}{}
	for _, s := range collectAllScans(q, []*View{v}) {
		scanSet[s.Physical] = struct{}{}
		ss.aliasPhysical[s.Alias] = s.Physical
		ss.addRefColumns(s.Physical, s.Alias, q, v)
	}
	if catalog != nil {
		for name, t := range catalog.Tables {
			if _, ok := scanSet[name]; !ok {
				continue
			}
			scanSet[name] = struct{}{}
			for _, c := range t.Columns {
				ss.columns[name] = append(ss.columns[name], c.Name)
				if ss.specs[name] == nil {
					ss.specs[name] = map[string]ColumnSpec{}
				}
				ss.specs[name][c.Name] = c
			}
		}
	}
	for name := range scanSet {
		ss.physicalTables = append(ss.physicalTables, name)
		colSet := map[string]struct{}{}
		cols := []string{}
		for _, c := range ss.columns[name] {
			if _, ok := colSet[c]; ok {
				continue
			}
			colSet[c] = struct{}{}
			cols = append(cols, c)
		}
		ss.columns[name] = cols
		sort.Strings(ss.columns[name])
	}
	sort.Strings(ss.physicalTables)

	addConst := func(p Predicate) {
		if p.Value != nil {
			ss.constants[p.Col] = append(ss.constants[p.Col], p.Value)
		}
		if p.High != nil {
			ss.constants[p.Col] = append(ss.constants[p.Col], p.High)
		}
	}
	for _, p := range allPredicates(q, v, comp) {
		addConst(p)
	}
	return ss
}

func (ss *searchSchema) addRefColumns(physical, alias string, q *Query, v *View) {
	colSet := map[string]struct{}{}
	for _, c := range ss.columns[physical] {
		colSet[c] = struct{}{}
	}
	var consider func(ref ColRef)
	consider = func(ref ColRef) {
		if ref.Table == alias || ref.Table == physical {
			colSet[ref.Column] = struct{}{}
		}
	}
	for _, ref := range allRefs(q, v) {
		consider(ref)
	}
	cols := make([]string, 0, len(colSet))
	for c := range colSet {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	ss.columns[physical] = cols
}

func allPredicates(q *Query, v *View, comp *Compensation) []Predicate {
	var out []Predicate
	var walk func(n *Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		out = append(out, effectivePredicates(n.Where, n.NullReject)...)
		for _, e := range n.On {
			_ = e
		}
		walk(n.Left)
		walk(n.Right)
	}
	if q != nil {
		out = append(out, effectivePredicates(q.Where, q.NullReject)...)
		walk(q.Root)
	}
	if v != nil {
		out = append(out, effectivePredicates(v.Where, v.NullReject)...)
		walk(v.Root)
	}
	if comp != nil {
		for _, alt := range comp.Filter.Alternatives {
			out = append(out, alt...)
		}
	}
	return out
}

func enumerateCounterexample(catalog *Catalog, q *Query, v *View, project []ColRef, comp *Compensation, opts SearchOptions, check func(*Database) (*Counterexample, error)) (*Counterexample, *ExhaustiveReport, error) {
	ss := buildSearchSchema(catalog, q, v, comp)

	report := &ExhaustiveReport{MaxRowsPerTable: opts.MaxRowsPerTable, Minimal: opts.Minimize}
	var lastErr error

	// cap=0 checks the all-empty instance, then cap=1, then cap=2.  This makes
	// the first witness as small as possible under the chosen domain.
	maxCap := opts.MaxRowsPerTable
	for cap := 0; cap <= maxCap; cap++ {
		tableStates := map[string][][]map[string]any{}
		for _, table := range ss.physicalTables {
			rows, err := ss.rowChoices(catalog, table, opts)
			if err != nil {
				return nil, report, err
			}
			states := subsetStates(rows, cap, ss.primaryKeys(table))
			tableStates[table] = states
		}

		tables := append([]string(nil), ss.physicalTables...)
		db := NewDatabase()
		var found *Counterexample
		var budget bool
		var rec func(i int) bool
		rec = func(i int) bool {
			if i == len(tables) {
				report.InstancesTested++
				if opts.MaxModels > 0 && report.InstancesTested > opts.MaxModels {
					budget = true
					return true
				}
				cx, err := check(db)
				if err != nil {
					lastErr = err
					return false
				}
				if cx != nil {
					found = cx
					return true
				}
				return false
			}
			name := tables[i]
			for _, state := range tableStates[name] {
				db.Tables[name] = &TableData{Name: name, Rows: state}
				if rec(i + 1) {
					return true
				}
			}
			delete(db.Tables, name)
			return false
		}
		rec(0)
		if found != nil {
			report.Found = true
			report.Complete = false
			fillRowCounts(report, found.Database)
			return found, report, nil
		}
		if budget {
			report.Complete = false
			return nil, report, ErrSearchBudget
		}
	}
	report.Found = false
	report.Complete = true
	return nil, report, lastErr
}

func (ss searchSchema) primaryKeys(table string) []string {
	var pks []string
	for name, spec := range ss.specs[table] {
		if spec.PrimaryKey {
			pks = append(pks, name)
		}
	}
	sort.Strings(pks)
	return pks
}

func (ss searchSchema) rowChoices(catalog *Catalog, table string, opts SearchOptions) ([]map[string]any, error) {
	cols := ss.columns[table]
	if len(cols) == 0 {
		return []map[string]any{{}}, nil
	}
	domains := make([][]any, len(cols))
	for i, col := range cols {
		spec, hasSpec := ss.specs[table][col]
		var specPtr *ColumnSpec
		if hasSpec {
			specPtr = &spec
		}
		nullable := true
		if hasSpec && (spec.NotNull || spec.PrimaryKey) {
			nullable = false
		}
		constants := ss.constantsFor(table, col)
		domains[i] = columnDomain(specPtr, nullable, constants, opts)
	}

	rows := []map[string]any{}
	var gen func(i int, row map[string]any)
	gen = func(i int, row map[string]any) {
		if i == len(cols) {
			cp := map[string]any{}
			for k, v := range row {
				cp[k] = v
			}
			rows = append(rows, cp)
			return
		}
		col := cols[i]
		for _, val := range domains[i] {
			row[col] = val
			gen(i+1, row)
		}
	}
	gen(0, map[string]any{})
	return rows, nil
}

func (ss searchSchema) constantsFor(physical, col string) []any {
	var out []any
	for ref, vals := range ss.constants {
		phys, ok := ss.aliasPhysical[ref.Table]
		if !ok {
			phys = ref.Table
		}
		if phys == physical && ref.Column == col {
			out = append(out, vals...)
		}
	}
	return out
}

func columnDomain(spec *ColumnSpec, nullable bool, constants []any, opts SearchOptions) []any {
	var out []any
	seen := map[string]struct{}{}
	add := func(v any) {
		nv, err := NormalizeValue(v)
		if err != nil {
			return
		}
		k := nv.Key()
		if _, ok := seen[k]; ok {
			return
		}
		seen[k] = struct{}{}
		out = append(out, nv.Any())
	}
	var kind valueKind
	for _, c := range constants {
		nv, err := NormalizeValue(c)
		if err != nil {
			continue
		}
		kind = nv.kind
		add(c)
		// Add one-step boundaries useful for strict range predicates.
		if nv.kind == kindInt {
			add(Int(nv.I - 1))
			add(Int(nv.I + 1))
		}
	}
	stringType := (spec != nil && spec.Type == ColumnTypeString) || kind == kindString
	if stringType {
		add("a")
		add("b")
		for _, c := range constants {
			add(c)
		}
	} else {
		for _, v := range opts.Domain {
			add(v)
		}
		for _, v := range opts.ExtraDomain {
			add(v)
		}
	}
	if kind == kindString && !stringType {
		add("x")
		add("y")
	}
	if nullable {
		add(nil)
	}
	// Keep Cartesian products bounded while retaining 0/1, constants, and nil.
	if len(out) > 5 {
		trimmed := []any{}
		for _, v := range out {
			if v == nil || len(trimmed) < 4 {
				trimmed = append(trimmed, v)
			}
		}
		out = trimmed
	}
	return out
}

func subsetStates(rows []map[string]any, cap int, pks []string) [][]map[string]any {
	if cap < 0 {
		return nil
	}
	var states [][]map[string]any
	states = append(states, []map[string]any{})
	if cap >= 1 {
		for _, r := range rows {
			states = append(states, []map[string]any{r})
		}
	}
	if cap >= 2 {
		for i := 0; i < len(rows); i++ {
			for j := i + 1; j < len(rows); j++ {
				if pkConflict(rows[i], rows[j], pks) {
					continue
				}
				states = append(states, []map[string]any{rows[i], rows[j]})
			}
		}
	}
	return states
}

func pkConflict(a, b map[string]any, pks []string) bool {
	if len(pks) == 0 {
		return false
	}
	allEqual := true
	for _, pk := range pks {
		av, aok := a[pk]
		bv, bok := b[pk]
		if !aok || !bok {
			allEqual = false
			continue
		}
		va, _ := NormalizeValue(av)
		vb, _ := NormalizeValue(bv)
		if !ValuesEqual(va, vb) {
			allEqual = false
		}
	}
	return allEqual
}

func minimizeCounterexample(cx *Counterexample, check func(*Database) (*Counterexample, error)) *Counterexample {
	if cx == nil || cx.Database == nil {
		return cx
	}
	current := CloneDatabase(cx.Database)
	changed := true
	for changed {
		changed = false
		tables := make([]string, 0, len(current.Tables))
		for name := range current.Tables {
			tables = append(tables, name)
		}
		sort.Strings(tables)
		for _, name := range tables {
			td := current.Tables[name]
			for i := 0; i < len(td.Rows); i++ {
				candidate := CloneDatabase(current)
				cdt := candidate.Tables[name]
				cdt.Rows = append(append([]map[string]any{}, cdt.Rows[:i]...), cdt.Rows[i+1:]...)
				newCX, err := check(candidate)
				if err == nil && newCX != nil {
					current = candidate
					changed = true
					break
				}
			}
			if changed {
				break
			}
		}
	}
	finalCX, err := check(current)
	if err == nil && finalCX != nil {
		finalCX.Database = current
		return finalCX
	}
	return cx
}

func fillRowCounts(rep *ExhaustiveReport, db *Database) {
	if rep == nil || db == nil {
		return
	}
	rep.RowCounts = map[string]int{}
	total := 0
	for name, t := range db.Tables {
		n := 0
		if t != nil {
			n = len(t.Rows)
		}
		rep.RowCounts[name] = n
		total += n
	}
	rep.Minimal = total <= 2*len(rep.RowCounts)
}

func publicDomain(opts SearchOptions) []any {
	out := append([]any{}, opts.Domain...)
	out = append(out, opts.ExtraDomain...)
	return out
}

var _ = fmt.Sprintf
