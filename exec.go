package decor

import (
	"fmt"
	"strconv"
	"strings"
)

// Row is one tuple. Keys are "alias.column" for scanned tables, or plain
// column names after projection.
type Row map[string]interface{}

// DB is a tiny in-memory catalog: table name -> unqualified rows.
type DB struct {
	Tables map[string][]Row
}

// NewDB creates an empty in-memory database.
func NewDB() *DB { return &DB{Tables: map[string][]Row{}} }

// AddTable registers a table with unqualified column names.
func (db *DB) AddTable(name string, rows []Row) { db.Tables[name] = rows }

func qualify(r Row, alias string) Row {
	out := make(Row, len(r))
	for k, v := range r {
		out[alias+"."+k] = v
	}
	return out
}

// resolve finds a column value in a row, honoring optional table qualifiers.
func resolve(r Row, ref *ColumnRef) (interface{}, bool) {
	if ref.Table != "" {
		if v, ok := r[ref.Table+"."+ref.Name]; ok {
			return v, true
		}
		// A qualified reference must not silently match another table's
		// column. Only fall back to the bare name when the row carries no
		// columns from that qualifier at all (e.g. projected output).
		prefix := ref.Table + "."
		for k := range r {
			if strings.HasPrefix(k, prefix) {
				return nil, false
			}
		}
		v, ok := r[ref.Name]
		return v, ok
	}
	if v, ok := r[ref.Name]; ok {
		return v, true
	}
	suffix := "." + ref.Name
	for k, v := range r {
		if strings.HasSuffix(k, suffix) {
			return v, true
		}
	}
	return nil, false
}

// resolveScope resolves a column against a scope: a stack of rows from
// outermost to innermost query block. Inner rows shadow outer ones.
func resolveScope(scope []Row, ref *ColumnRef) (interface{}, bool) {
	for i := len(scope) - 1; i >= 0; i-- {
		if v, ok := resolve(scope[i], ref); ok {
			return v, true
		}
	}
	return nil, false
}

func toFloat(v interface{}) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case int:
		return float64(x), true
	}
	return 0, false
}

func compare(op string, l, r interface{}) (bool, error) {
	if lf, ok := toFloat(l); ok {
		rf, ok := toFloat(r)
		if !ok {
			return false, fmt.Errorf("cannot compare number with %T", r)
		}
		switch op {
		case "=":
			return lf == rf, nil
		case "<>", "!=":
			return lf != rf, nil
		case "<":
			return lf < rf, nil
		case ">":
			return lf > rf, nil
		case "<=":
			return lf <= rf, nil
		case ">=":
			return lf >= rf, nil
		}
	}
	ls, lok := l.(string)
	rs, rok := r.(string)
	if lok && rok {
		switch op {
		case "=":
			return ls == rs, nil
		case "<>", "!=":
			return ls != rs, nil
		case "<":
			return ls < rs, nil
		case ">":
			return ls > rs, nil
		case "<=":
			return ls <= rs, nil
		case ">=":
			return ls >= rs, nil
		}
	}
	return false, fmt.Errorf("cannot compare %T and %T with %s", l, r, op)
}

func literalValue(l *Literal) (interface{}, error) {
	if l.Kind == LitString {
		return l.Value, nil
	}
	if strings.Contains(l.Value, ".") {
		return strconv.ParseFloat(l.Value, 64)
	}
	return strconv.ParseInt(l.Value, 10, 64)
}

// evalCtx carries the database, an inner-predicate evaluation counter, and
// a cache of materialized subquery FROM rows shared across outer rows.
type evalCtx struct {
	db    *DB
	evals *int
	cache map[*SelectStmt][]Row
}

func newEvalCtx(db *DB, evals *int) *evalCtx {
	return &evalCtx{db: db, evals: evals, cache: map[*SelectStmt][]Row{}}
}

// subqueryRows materializes the subquery's FROM clause once per statement.
func (c *evalCtx) subqueryRows(sub *SelectStmt) []Row {
	if rows, ok := c.cache[sub]; ok {
		return rows
	}
	rows := crossRows(sub.From, c.db)
	c.cache[sub] = rows
	return rows
}

// evalScalar evaluates a non-boolean expression against a scope.
func evalScalar(e Expr, scope []Row) (interface{}, error) {
	switch x := e.(type) {
	case *Literal:
		return literalValue(x)
	case *ColumnRef:
		if v, ok := resolveScope(scope, x); ok {
			return v, nil
		}
		return nil, fmt.Errorf("unknown column %s", RenderExpr(x))
	}
	return nil, fmt.Errorf("not a scalar expression: %s", RenderExpr(e))
}

// evalBool evaluates a boolean expression. Subqueries are evaluated
// naively: the inner query is re-scanned for every outer row — exactly
// the per-row behavior decorrelation eliminates.
func evalBool(e Expr, scope []Row, ctx *evalCtx) (bool, error) {
	switch x := e.(type) {
	case *BinaryExpr:
		switch x.Op {
		case "AND":
			l, err := evalBool(x.Left, scope, ctx)
			if err != nil || !l {
				return l, err
			}
			return evalBool(x.Right, scope, ctx)
		case "OR":
			l, err := evalBool(x.Left, scope, ctx)
			if err != nil || l {
				return l, err
			}
			return evalBool(x.Right, scope, ctx)
		default:
			lv, err := evalScalar(x.Left, scope)
			if err != nil {
				return false, err
			}
			rv, err := evalScalar(x.Right, scope)
			if err != nil {
				return false, err
			}
			return compare(x.Op, lv, rv)
		}
	case *NotExpr:
		v, err := evalBool(x.X, scope, ctx)
		return !v, err
	case *ExistsExpr:
		return evalExists(x.Subquery, scope, ctx)
	case *InExpr:
		if x.Subquery == nil {
			lv, err := evalScalar(x.Left, scope)
			if err != nil {
				return false, err
			}
			for _, le := range x.List {
				rv, err := evalScalar(le, scope)
				if err != nil {
					return false, err
				}
				eq, err := compare("=", lv, rv)
				if err != nil {
					return false, err
				}
				if eq {
					return !x.Negated, nil
				}
			}
			return x.Negated, nil
		}
		return evalInSubquery(x, scope, ctx)
	}
	return false, fmt.Errorf("not a boolean expression: %s", RenderExpr(e))
}

// evalExists naively runs the subquery for one outer row (nested loop).
func evalExists(sub *SelectStmt, outer []Row, ctx *evalCtx) (bool, error) {
	scope := make([]Row, len(outer)+1)
	copy(scope, outer)
	for _, inner := range ctx.subqueryRows(sub) {
		if sub.Where == nil {
			return true, nil
		}
		*ctx.evals++
		scope[len(outer)] = inner
		ok, err := evalBool(sub.Where, scope, ctx)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil // EXISTS stops at first match
		}
	}
	return false, nil
}

// evalInSubquery naively runs an IN subquery for one outer row.
func evalInSubquery(x *InExpr, outer []Row, ctx *evalCtx) (bool, error) {
	lv, err := evalScalar(x.Left, outer)
	if err != nil {
		return false, err
	}
	sub := x.Subquery
	scope := make([]Row, len(outer)+1)
	copy(scope, outer)
	for _, inner := range ctx.subqueryRows(sub) {
		scope[len(outer)] = inner
		if sub.Where != nil {
			*ctx.evals++
			ok, err := evalBool(sub.Where, scope, ctx)
			if err != nil {
				return false, err
			}
			if !ok {
				continue
			}
		}
		rv, err := evalScalar(sub.Items[0].Expr, scope)
		if err != nil {
			return false, err
		}
		eq, err := compare("=", lv, rv)
		if err != nil {
			return false, err
		}
		if eq {
			return true, nil
		}
	}
	return false, nil
}

func mergeRows(a, b Row) Row {
	out := make(Row, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// crossRows produces qualified rows for a FROM clause (cartesian product).
func crossRows(from []TableRef, db *DB) []Row {
	var rows []Row
	for _, t := range from {
		base, ok := db.Tables[t.Name]
		if !ok {
			return nil
		}
		alias := t.EffectiveName()
		var qualified []Row
		for _, r := range base {
			qualified = append(qualified, qualify(r, alias))
		}
		if rows == nil {
			rows = qualified
			continue
		}
		var crossed []Row
		for _, l := range rows {
			for _, r := range qualified {
				crossed = append(crossed, mergeRows(l, r))
			}
		}
		rows = crossed
	}
	return rows
}

// EvalNaive executes a (possibly correlated) query the naive way: for every
// outer row, each subquery in the WHERE clause is re-executed from scratch.
// It returns the matching rows and the number of inner predicate
// evaluations, which exposes the per-row cost decorrelation removes.
func EvalNaive(stmt *SelectStmt, db *DB) ([]Row, int, error) {
	evals := 0
	ctx := newEvalCtx(db, &evals)
	var out []Row
	for _, r := range crossRows(stmt.From, db) {
		if stmt.Where == nil {
			out = append(out, r)
			continue
		}
		ok, err := evalBool(stmt.Where, []Row{r}, ctx)
		if err != nil {
			return nil, 0, err
		}
		if ok {
			out = append(out, r)
		}
	}
	return out, evals, nil
}

// Execute runs a logical plan against the in-memory database.
func Execute(n Node, db *DB) ([]Row, error) {
	switch x := n.(type) {
	case *Scan:
		base, ok := db.Tables[x.Table]
		if !ok {
			return nil, fmt.Errorf("unknown table %s", x.Table)
		}
		out := make([]Row, 0, len(base))
		for _, r := range base {
			out = append(out, qualify(r, x.Alias))
		}
		return out, nil
	case *Filter:
		rows, err := Execute(x.Child, db)
		if err != nil {
			return nil, err
		}
		zero := 0
		ctx := newEvalCtx(db, &zero)
		var out []Row
		for _, r := range rows {
			ok, err := evalBool(x.Pred, []Row{r}, ctx)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, r)
			}
		}
		return out, nil
	case *Project:
		rows, err := Execute(x.Child, db)
		if err != nil {
			return nil, err
		}
		out := make([]Row, 0, len(rows))
		for _, r := range rows {
			nr := Row{}
			for _, c := range x.Cols {
				v, ok := resolve(r, c)
				if !ok {
					return nil, fmt.Errorf("unknown column %s", RenderExpr(c))
				}
				nr[c.Name] = v
			}
			out = append(out, nr)
		}
		return out, nil
	case *Distinct:
		rows, err := Execute(x.Child, db)
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		var out []Row
		for _, r := range rows {
			k := rowKey(r)
			if !seen[k] {
				seen[k] = true
				out = append(out, r)
			}
		}
		return out, nil
	case *Limit:
		rows, err := Execute(x.Child, db)
		if err != nil {
			return nil, err
		}
		if len(rows) > x.N {
			rows = rows[:x.N]
		}
		return rows, nil
	case *HashSemiJoin:
		left, err := Execute(x.Left, db)
		if err != nil {
			return nil, err
		}
		right, err := Execute(x.Right, db)
		if err != nil {
			return nil, err
		}
		// Build: hash the (deduplicated) right side once.
		table := make(map[string]struct{}, len(right))
		for _, r := range right {
			k, err := keyOf(r, x.RightKeys)
			if err != nil {
				return nil, err
			}
			table[k] = struct{}{}
		}
		// Probe: one hash lookup per left row.
		var out []Row
		for _, l := range left {
			k, err := keyOf(l, x.LeftKeys)
			if err != nil {
				return nil, err
			}
			if _, ok := table[k]; ok {
				out = append(out, l)
			}
		}
		return out, nil
	case *CrossJoin:
		left, err := Execute(x.Left, db)
		if err != nil {
			return nil, err
		}
		right, err := Execute(x.Right, db)
		if err != nil {
			return nil, err
		}
		var out []Row
		for _, l := range left {
			for _, r := range right {
				out = append(out, mergeRows(l, r))
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown plan node %T", n)
}

func keyOf(r Row, cols []*ColumnRef) (string, error) {
	parts := make([]string, len(cols))
	for i, c := range cols {
		v, ok := resolve(r, c)
		if !ok {
			return "", fmt.Errorf("unknown column %s", RenderExpr(c))
		}
		parts[i] = fmt.Sprintf("%#v", v)
	}
	return strings.Join(parts, "\x00"), nil
}

func rowKey(r Row) string {
	keys := make([]string, 0, len(r))
	for k := range r {
		keys = append(keys, k)
	}
	// simple deterministic ordering
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + fmt.Sprintf("%#v", r[k])
	}
	return strings.Join(parts, "\x00")
}
