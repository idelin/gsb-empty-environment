package mvrewrite

import (
	"fmt"
	"sort"
	"strings"
)

// Row is one evaluated tuple keyed by qualified column reference.
type Row map[ColRef]Value

// TableData is a physical base-table instance.  Row maps unqualified column
// names to Go values; nil means SQL NULL.
type TableData struct {
	Name string           `json:"name"`
	Rows []map[string]any `json:"rows"`
}

// Database is a finite base-table instance keyed by physical table name.
type Database struct {
	Tables map[string]*TableData `json:"tables"`
}

// NewDatabase creates an empty instance.
func NewDatabase() *Database { return &Database{Tables: map[string]*TableData{}} }

// Add appends a row to a physical table, creating it if necessary.
func (db *Database) Add(table string, row map[string]any) *Database {
	if db.Tables == nil {
		db.Tables = map[string]*TableData{}
	}
	t := db.Tables[table]
	if t == nil {
		t = &TableData{Name: table}
		db.Tables[table] = t
	}
	t.Rows = append(t.Rows, row)
	return db
}

// Tuple is an output tuple aligned with Cols.
type Tuple struct {
	Cols   []ColRef `json:"cols"`
	Values []Value  `json:"values"`
}

// AnyValues returns public Go values aligned with Cols.
func (t Tuple) AnyValues() []any {
	out := make([]any, len(t.Values))
	for i, v := range t.Values {
		out[i] = v.Any()
	}
	return out
}

func tupleKey(cols []ColRef, vals []Value) string {
	var b strings.Builder
	for i, c := range cols {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(c.Key())
		b.WriteByte('=')
		b.WriteString(vals[i].Key())
	}
	return b.String()
}

// Key returns the stable set-semantics key.
func (t Tuple) Key() string { return tupleKey(t.Cols, t.Values) }

// String renders a tuple.
func (t Tuple) String() string {
	parts := make([]string, len(t.Cols))
	for i := range t.Cols {
		parts[i] = t.Cols[i].String() + "=" + t.Values[i].String()
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// ResultSet is a deduplicated set of output tuples.
type ResultSet struct {
	Cols   []ColRef
	Tuples []Tuple
	index  map[string]struct{}
}

func newResultSet(cols []ColRef) *ResultSet {
	return &ResultSet{Cols: append([]ColRef(nil), cols...), index: map[string]struct{}{}}
}

func (rs *ResultSet) add(vals []Value) bool {
	key := tupleKey(rs.Cols, vals)
	if _, ok := rs.index[key]; ok {
		return false
	}
	rs.index[key] = struct{}{}
	rs.Tuples = append(rs.Tuples, Tuple{Cols: append([]ColRef(nil), rs.Cols...), Values: append([]Value(nil), vals...)})
	return true
}

// Keys returns sorted tuple keys for deterministic comparison/tests.
func (rs *ResultSet) Keys() []string {
	out := make([]string, 0, len(rs.Tuples))
	for _, t := range rs.Tuples {
		out = append(out, t.Key())
	}
	sort.Strings(out)
	return out
}

// EvalQuery evaluates a query under set semantics.
func EvalQuery(q *Query, db *Database) (*ResultSet, error) {
	if q == nil || q.Root == nil {
		return nil, fmt.Errorf("mvrewrite: nil query/root")
	}
	ec := newEvalContext(q.Schema, allRefs(q, nil))
	rows, err := evalNodeCtx(q.Root, db, ec)
	if err != nil {
		return nil, err
	}
	rows, err = evalPredicates(effectivePredicates(q.Where, q.NullReject), rows)
	if err != nil {
		return nil, err
	}
	return projectRows(rows, q.Select, q.distinct())
}

// EvalViewRows evaluates a view and projects its exposed columns while
// retaining duplicate occurrences.  Compensation is logically applied before
// the final DISTINCT projection.
func EvalViewRows(v *View, db *Database, catalog *Catalog) ([]Row, error) {
	if v == nil || v.Root == nil {
		return nil, fmt.Errorf("mvrewrite: nil view/root")
	}
	ec := newEvalContext(catalog, allRefs(nil, v))
	rows, err := evalNodeCtx(v.Root, db, ec)
	if err != nil {
		return nil, err
	}
	rows, err = evalPredicates(effectivePredicates(v.Where, v.NullReject), rows)
	if err != nil {
		return nil, err
	}
	if len(v.Select) == 0 {
		return rows, nil
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		nr := Row{}
		for _, c := range v.Select {
			val, ok := r[c]
			if !ok {
				return nil, fmt.Errorf("mvrewrite: view %q does not produce %s", v.ID, c)
			}
			nr[c] = val
		}
		out = append(out, nr)
	}
	return out, nil
}

// EvalView evaluates and deduplicates a view's declared output.
func EvalView(v *View, db *Database, catalog *Catalog) (*ResultSet, error) {
	rows, err := EvalViewRows(v, db, catalog)
	if err != nil {
		return nil, err
	}
	return projectRows(rows, v.Select, true)
}

// ApplyFilter evaluates a compensation DNF on exposed view rows.
func ApplyFilter(rows []Row, f DNF) ([]Row, error) {
	if len(f.Alternatives) == 0 {
		return nil, nil
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		ok, err := evalDNF(f, r)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, r)
		}
	}
	return out, nil
}

// Rewrite evaluates V, applies compensation, and performs dedup projection.
func Rewrite(v *View, comp Compensation, projectCols []ColRef, db *Database, catalog *Catalog) (*ResultSet, error) {
	rows, err := EvalViewRows(v, db, catalog)
	if err != nil {
		return nil, err
	}
	rows, err = ApplyFilter(rows, comp.Filter)
	if err != nil {
		return nil, err
	}
	if len(projectCols) == 0 {
		projectCols = comp.Project
	}
	return projectRows(rows, projectCols, true)
}

func projectRows(rows []Row, cols []ColRef, distinct bool) (*ResultSet, error) {
	rs := newResultSet(cols)
	for _, r := range rows {
		vals := make([]Value, len(cols))
		for i, c := range cols {
			v, ok := r[c]
			if !ok {
				return nil, fmt.Errorf("mvrewrite: projection column %s is not present", c)
			}
			vals[i] = v
		}
		if distinct {
			rs.add(vals)
		} else {
			key := tupleKey(cols, vals)
			rs.Tuples = append(rs.Tuples, Tuple{Cols: append([]ColRef(nil), cols...), Values: append([]Value(nil), vals...)})
			if rs.index == nil {
				rs.index = map[string]struct{}{}
			}
			rs.index[fmt.Sprintf("%s#%d", key, len(rs.Tuples))] = struct{}{}
		}
	}
	if !distinct {
		rs.index = nil
	}
	return rs, nil
}

type evalContext struct {
	catalog *Catalog
	hints   map[ColRef]struct{}
}

func newEvalContext(catalog *Catalog, refs []ColRef) *evalContext {
	ec := &evalContext{catalog: catalog, hints: map[ColRef]struct{}{}}
	for _, r := range refs {
		ec.hints[r] = struct{}{}
	}
	return ec
}

func evalNodeCtx(n *Node, db *Database, ec *evalContext) ([]Row, error) {
	if n == nil {
		return nil, fmt.Errorf("mvrewrite: nil node")
	}
	switch n.Kind {
	case ScanNode:
		return evalScan(n, db)
	case InnerJoin, LeftJoin:
		left, err := evalNodeCtx(n.Left, db, ec)
		if err != nil {
			return nil, err
		}
		right, err := evalNodeCtx(n.Right, db, ec)
		if err != nil {
			return nil, err
		}
		joined := joinRows(n, left, right, ec)
		return evalPredicates(effectivePredicates(n.Where, n.NullReject), joined)
	default:
		return nil, fmt.Errorf("mvrewrite: unknown node kind %q", n.Kind)
	}
}

func evalScan(n *Node, db *Database) ([]Row, error) {
	physical := n.Table
	if physical == "" {
		physical = n.Alias
	}
	alias := n.Alias
	if alias == "" {
		alias = physical
	}
	if db == nil || db.Tables == nil {
		return nil, fmt.Errorf("mvrewrite: missing database")
	}
	td, ok := db.Tables[physical]
	if !ok {
		// Also allow instances to be keyed directly by alias for simple specs.
		td, ok = db.Tables[alias]
		if !ok {
			return nil, fmt.Errorf("mvrewrite: missing table instance %q", physical)
		}
	}
	out := make([]Row, 0, len(td.Rows))
	for _, source := range td.Rows {
		r := Row{}
		for name, raw := range source {
			v, err := NormalizeValue(raw)
			if err != nil {
				return nil, err
			}
			r[C(alias, name)] = v
		}
		out = append(out, r)
	}
	return evalPredicates(effectivePredicates(n.Where, n.NullReject), out)
}

func nullRejectPreds(refs []ColRef) []Predicate {
	out := make([]Predicate, 0, len(refs))
	for _, ref := range refs {
		out = append(out, NotNull(ref))
	}
	return out
}

func effectivePredicates(base []Predicate, reject []ColRef) []Predicate {
	if len(reject) == 0 {
		return base
	}
	out := append(append([]Predicate(nil), base...), nullRejectPreds(reject)...)
	return out
}

func joinRows(n *Node, left, right []Row, ec *evalContext) []Row {
	rightCols := nodeColumnsContext(n.Right, ec)
	out := []Row{}
	for _, l := range left {
		matches := 0
		for _, r := range right {
			if !matchesEquality(n.On, l, r) {
				continue
			}
			matches++
			combined := cloneRow(l)
			mergeRow(combined, r)
			out = append(out, combined)
		}
		if matches == 0 && n.Kind == LeftJoin {
			combined := cloneRow(l)
			for _, c := range rightCols {
				combined[c] = NullValue()
			}
			out = append(out, combined)
		}
	}
	if n.Kind == InnerJoin {
		return out
	}
	return out
}

func matchesEquality(eqs []Equality, left, right Row) bool {
	for _, eq := range eqs {
		lv, lok := lookupAnySide(eq.Left, left, right)
		rv, rok := lookupAnySide(eq.Right, left, right)
		if !lok || !rok {
			return false
		}
		if !ValuesEqual(lv, rv) {
			return false
		}
	}
	return true
}

func lookupAnySide(ref ColRef, rows ...Row) (Value, bool) {
	for _, r := range rows {
		if v, ok := r[ref]; ok {
			return v, true
		}
	}
	return Value{}, false
}

func cloneRow(r Row) Row {
	out := make(Row, len(r))
	for k, v := range r {
		out[k] = v
	}
	return out
}

func mergeRow(dst Row, src Row) {
	for k, v := range src {
		dst[k] = v
	}
}

func evalPredicates(preds []Predicate, rows []Row) ([]Row, error) {
	if len(preds) == 0 {
		return rows, nil
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		ok := true
		for _, p := range preds {
			tri, err := evalPredicate(p, r)
			if err != nil {
				return nil, err
			}
			if tri != triTrue {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, r)
		}
	}
	return out, nil
}

type triState int

const (
	triFalse triState = iota
	triTrue
	triUnknown
)

func evalDNF(f DNF, r Row) (bool, error) {
	if len(f.Alternatives) == 0 {
		return false, nil
	}
	for _, conj := range f.Alternatives {
		ok := true
		for _, p := range conj {
			tri, err := evalPredicate(p, r)
			if err != nil {
				return false, err
			}
			if tri != triTrue {
				ok = false
				break
			}
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func evalPredicate(p Predicate, r Row) (triState, error) {
	v, ok := r[p.Col]
	if !ok {
		return triUnknown, fmt.Errorf("mvrewrite: predicate column %s is not available in evaluated row", p.Col)
	}
	switch p.Op {
	case OpIsNull:
		if v.Null {
			return triTrue, nil
		}
		return triFalse, nil
	case OpNotNull:
		if v.Null {
			return triFalse, nil
		}
		return triTrue, nil
	}
	if v.Null {
		return triUnknown, nil
	}

	target, err := NormalizeValue(p.Value)
	if err != nil {
		return triUnknown, err
	}
	switch p.Op {
	case OpEq:
		if ValuesEqual(v, target) {
			return triTrue, nil
		}
		return triFalse, nil
	case OpNe:
		if ValuesEqual(v, target) {
			return triFalse, nil
		}
		return triTrue, nil
	case OpLt, OpLe, OpGt, OpGe, OpBetween, OpNotBetween:
		if p.Op == OpBetween || p.Op == OpNotBetween {
			high, err := NormalizeValue(p.High)
			if err != nil {
				return triUnknown, err
			}
			cmpLow, err := CompareValues(v, target)
			if err != nil {
				return triUnknown, err
			}
			cmpHigh, err := CompareValues(v, high)
			if err != nil {
				return triUnknown, err
			}
			in := cmpLow >= 0 && cmpHigh <= 0
			if p.Op == OpBetween {
				return triBool(in), nil
			}
			return triBool(!in), nil
		}
		cmp, err := CompareValues(v, target)
		if err != nil {
			return triUnknown, err
		}
		ok := false
		switch p.Op {
		case OpLt:
			ok = cmp < 0
		case OpLe:
			ok = cmp <= 0
		case OpGt:
			ok = cmp > 0
		case OpGe:
			ok = cmp >= 0
		}
		return triBool(ok), nil
	default:
		return triUnknown, fmt.Errorf("mvrewrite: unsupported predicate op %q", p.Op)
	}
}

func triBool(b bool) triState {
	if b {
		return triTrue
	}
	return triFalse
}

// nodeColumns returns all qualified columns that can be emitted by a node.
// Catalog declarations are used for empty scans and padded right subtrees.
func nodeColumns(n *Node, catalog *Catalog) []ColRef {
	return nodeColumnsContext(n, &evalContext{catalog: catalog, hints: map[ColRef]struct{}{}})
}

func nodeColumnsContext(n *Node, ec *evalContext) []ColRef {
	if n == nil {
		return nil
	}
	if n.Kind == ScanNode {
		alias := n.Alias
		if alias == "" {
			alias = n.Table
		}
		physical := n.Table
		if physical == "" {
			physical = alias
		}
		if ec != nil && ec.catalog != nil {
			if t, ok := ec.catalog.table(physical); ok {
				out := make([]ColRef, 0, len(t.Columns))
				for _, c := range t.Columns {
					out = append(out, C(alias, c.Name))
				}
				return out
			}
		}
		if ec != nil {
			var out []ColRef
			for ref := range ec.hints {
				if ref.Table == alias {
					out = append(out, ref)
				}
			}
			if len(out) > 0 {
				sort.Slice(out, func(i, j int) bool { return out[i].Column < out[j].Column })
				return out
			}
		}
		return append([]ColRef(nil), n.Select...)
	}
	out := []ColRef{}
	out = append(out, nodeColumnsContext(n.Left, ec)...)
	out = append(out, nodeColumnsContext(n.Right, ec)...)
	return out
}

// NodeColumns is the exported schema-column helper.
func NodeColumns(n *Node, catalog *Catalog) []ColRef { return nodeColumns(n, catalog) }
