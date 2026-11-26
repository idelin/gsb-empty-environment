package mvrewrite

import (
	"fmt"
	"sort"
	"strings"
)

// Counterexample is a concrete legal base-table instance that refutes a
// proposed rewrite.  Every physical table has at most MaxRows rows in the
// minimized witness returned by this package.
type Counterexample struct {
	Kind        ReasonCode `json:"kind"`
	Description string     `json:"description"`
	FilterSQL   string     `json:"filter_sql,omitempty"`

	Database *Database `json:"database"`

	// QueryRows and RewrittenRows are sorted public row representations.
	QueryRows     []map[string]any `json:"query_rows"`
	ViewRows      []map[string]any `json:"view_rows,omitempty"`
	RewrittenRows []map[string]any `json:"rewritten_rows,omitempty"`

	Missing []map[string]any `json:"missing,omitempty"`
	Extra   []map[string]any `json:"extra,omitempty"`

	// QueryKeys/RewrittenKeys make tests and reports stable.
	QueryKeys     []string `json:"query_keys"`
	RewrittenKeys []string `json:"rewritten_keys"`
}

// ExhaustiveReport summarizes finite-instance validation.
type ExhaustiveReport struct {
	InstancesTested int            `json:"instances_tested"`
	MaxRowsPerTable int            `json:"max_rows_per_table"`
	Domain          []any          `json:"domain,omitempty"`
	Found           bool           `json:"found"`
	Complete        bool           `json:"complete"`
	Minimal         bool           `json:"minimal"`
	RowCounts       map[string]int `json:"row_counts,omitempty"`
}

// MismatchError wraps a semantic counterexample.
type MismatchError struct {
	Counterexample *Counterexample
}

func (e *MismatchError) Error() string {
	if e == nil || e.Counterexample == nil {
		return "mvrewrite: semantic mismatch"
	}
	cx := e.Counterexample
	return fmt.Sprintf("mvrewrite: rewrite mismatch (%s): missing=%v extra=%v", cx.Kind, cx.Missing, cx.Extra)
}

// CheckResult evaluates query and compensated rewrite on one database and
// returns a minimized-format counterexample if they differ.
func CheckResult(catalog *Catalog, q *Query, v *View, comp Compensation, db *Database) (*Counterexample, error) {
	if q == nil || v == nil {
		return nil, fmt.Errorf("mvrewrite: query and view are required")
	}
	evalQ := q
	if q.Schema == nil && catalog != nil {
		qc := *q
		qc.Schema = catalog
		evalQ = &qc
	}
	queryRS, err := EvalQuery(evalQ, db)
	if err != nil {
		return nil, err
	}
	viewRS, err := EvalView(v, db, catalog)
	if err != nil {
		return nil, err
	}
	rewriteRS, err := Rewrite(v, comp, q.Select, db, catalog)
	if err != nil {
		return nil, err
	}
	if sameResultSet(queryRS, rewriteRS) {
		return nil, nil
	}
	missing, extra := diffResultSets(queryRS, rewriteRS)
	cx := &Counterexample{
		Kind:          ReasonCompensationMismatch,
		Description:   "query and compensated distinct view projection differ on this instance",
		FilterSQL:     comp.Filter.SQL(),
		Database:      db,
		QueryRows:     resultPublicRows(queryRS),
		ViewRows:      resultPublicRows(viewRS),
		RewrittenRows: resultPublicRows(rewriteRS),
		Missing:       tuplesPublic(missing),
		Extra:         tuplesPublic(extra),
		QueryKeys:     queryRS.Keys(),
		RewrittenKeys: rewriteRS.Keys(),
	}
	if len(extra) > 0 && len(missing) == 0 {
		cx.Description = "compensated view projection contains an answer rejected by the query"
	}
	if len(missing) > 0 {
		cx.Description = "compensated view projection loses a query answer"
	}
	return cx, nil
}

// CheckCoverage verifies that every query answer has an identical projected
// view answer before compensation.  This detects rows lost by extra inner joins
// or stronger view predicates.
func CheckCoverage(catalog *Catalog, q *Query, v *View, db *Database) (*Counterexample, error) {
	evalQ := q
	if q.Schema == nil && catalog != nil {
		qc := *q
		qc.Schema = catalog
		evalQ = &qc
	}
	queryRS, err := EvalQuery(evalQ, db)
	if err != nil {
		return nil, err
	}
	viewRows, err := EvalViewRows(v, db, catalog)
	if err != nil {
		return nil, err
	}
	viewRS, err := projectRows(viewRows, q.Select, true)
	if err != nil {
		return nil, err
	}
	missing, _ := diffResultSets(queryRS, viewRS)
	if len(missing) == 0 {
		return nil, nil
	}
	viewFullRS, _ := EvalView(v, db, catalog)
	cx := &Counterexample{
		Kind:        ReasonViewLosesRows,
		Description: "view cannot produce a query answer even before compensation; an extra inner join or stronger predicate loses a row",
		Database:    db,
		QueryRows:   resultPublicRows(queryRS),
		Missing:     tuplesPublic(missing),
		QueryKeys:   queryRS.Keys(),
	}
	if viewFullRS != nil {
		cx.ViewRows = resultPublicRows(viewFullRS)
	}
	return cx, nil
}

func sameResultSet(a, b *ResultSet) bool {
	if a == nil || b == nil {
		return a == b
	}
	ak, bk := a.Keys(), b.Keys()
	if len(ak) != len(bk) {
		return false
	}
	for i := range ak {
		if ak[i] != bk[i] {
			return false
		}
	}
	return true
}

func diffResultSets(expected, actual *ResultSet) (missing, extra []Tuple) {
	exp := map[string]Tuple{}
	act := map[string]Tuple{}
	for _, t := range expected.Tuples {
		exp[t.Key()] = t
	}
	for _, t := range actual.Tuples {
		act[t.Key()] = t
	}
	for k, t := range exp {
		if _, ok := act[k]; !ok {
			missing = append(missing, t)
		}
	}
	for k, t := range act {
		if _, ok := exp[k]; !ok {
			extra = append(extra, t)
		}
	}
	sortTuples(missing)
	sortTuples(extra)
	return missing, extra
}

func sortTuples(ts []Tuple) {
	sort.Slice(ts, func(i, j int) bool { return ts[i].Key() < ts[j].Key() })
}

func resultPublicRows(rs *ResultSet) []map[string]any {
	if rs == nil {
		return nil
	}
	out := make([]map[string]any, 0, len(rs.Tuples))
	for _, t := range rs.Tuples {
		out = append(out, tuplePublic(t))
	}
	sort.Slice(out, func(i, j int) bool {
		return publicKey(out[i]) < publicKey(out[j])
	})
	return out
}

func tuplesPublic(ts []Tuple) []map[string]any {
	out := make([]map[string]any, 0, len(ts))
	for _, t := range ts {
		out = append(out, tuplePublic(t))
	}
	sort.Slice(out, func(i, j int) bool { return publicKey(out[i]) < publicKey(out[j]) })
	return out
}

func tuplePublic(t Tuple) map[string]any {
	m := map[string]any{}
	for i, c := range t.Cols {
		m[c.String()] = t.Values[i].Any()
	}
	return m
}

func publicKey(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(fmt.Sprint(m[k]))
		b.WriteByte(';')
	}
	return b.String()
}

// CloneDatabase returns a deep-ish copy safe for minimization bookkeeping.
func CloneDatabase(db *Database) *Database {
	if db == nil {
		return nil
	}
	out := NewDatabase()
	for name, td := range db.Tables {
		cp := &TableData{Name: name, Rows: make([]map[string]any, len(td.Rows))}
		for i, row := range td.Rows {
			nr := map[string]any{}
			for k, v := range row {
				nr[k] = v
			}
			cp.Rows[i] = nr
		}
		out.Tables[name] = cp
	}
	return out
}
