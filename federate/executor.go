package federate

import (
	"sort"
)

type located struct {
	row   Row
	token uint64
}

type outRecord struct {
	row   Row
	aggs  map[string]Value
	token uint64
}

func baseToken(shard, idx int) uint64 {
	return splitmix64((uint64(shard)+1)<<32 | (uint64(idx) + 1))
}

func combineTokens(a, b uint64) uint64 {
	return splitmix64(a ^ (b + 0x9e3779b97f4a7c15))
}

func evalTrue(e Expr, row Row, token, seed uint64) bool {
	if e == nil {
		return true
	}
	return parseBool(e.Eval(&EvalEnv{Row: row, Token: token, Seed: seed}))
}

func scanShard(t *Table, shardID int, filter Expr, order []OrderItem, limit int64, seed uint64, stats *Stats, countTransfer bool) []located {
	sh := t.Shards[shardID]
	out := make([]located, 0, len(sh.Rows))
	for i, row := range sh.Rows {
		if stats != nil {
			stats.ShardRowsScanned++
		}
		tokenShard := shardID
		if t.Dist.Kind == ReplicatedKind {
			// Every replica holds the same logical row; keep its identity stable
			// across shard-local joins and the full-pull reference.
			tokenShard = 0
		}
		token := baseToken(tokenShard, i)
		if !evalTrue(filter, row, token, seed) {
			continue
		}
		out = append(out, located{row: row, token: token})
	}
	if len(order) > 0 {
		sort.SliceStable(out, func(i, j int) bool {
			return compareOrderValues(
				locatedOrderKeys(&out[i], order, seed),
				locatedOrderKeys(&out[j], order, seed),
				order, out[i].token, out[j].token) < 0
		})
	}
	if limit >= 0 && int64(len(out)) > limit {
		out = out[:limit]
	}
	if countTransfer && stats != nil {
		stats.RowsTransferred += len(out)
	}
	return out
}

func scanTable(t *Table, filter Expr, seed uint64, stats *Stats, countTransfer bool) []located {
	var out []located
	for _, id := range t.ScanShards() {
		out = append(out, scanShard(t, id, filter, nil, -1, seed, stats, false)...)
	}
	if countTransfer && stats != nil {
		stats.RowsTransferred += len(out)
	}
	return out
}

func locatedOrderKeys(rec *located, order []OrderItem, seed uint64) []Value {
	if len(order) == 0 {
		return nil
	}
	env := &EvalEnv{Row: rec.row, Token: rec.token, Seed: seed}
	vals := make([]Value, len(order))
	for i, o := range order {
		vals[i] = o.Expr.Eval(env)
	}
	return vals
}

func sortLocated(records []located, order []OrderItem, seed uint64) {
	if len(order) == 0 {
		return
	}
	sort.SliceStable(records, func(i, j int) bool {
		return compareOrderValues(
			locatedOrderKeys(&records[i], order, seed),
			locatedOrderKeys(&records[j], order, seed),
			order, records[i].token, records[j].token) < 0
	})
}

func limitLocated(records []located, limit int64) []located {
	if limit >= 0 && int64(len(records)) > limit {
		return records[:limit]
	}
	return records
}

func compareOrderValues(a, b []Value, order []OrderItem, aToken, bToken uint64) int {
	for i := range order {
		av, bv := a[i], b[i]
		if av.IsNull() || bv.IsNull() {
			switch {
			case av.IsNull() && bv.IsNull():
				continue
			case av.IsNull():
				if order[i].nullsFirst() {
					return -1
				}
				return 1
			default:
				if order[i].nullsFirst() {
					return 1
				}
				return -1
			}
		}
		c, ok := Compare(av, bv)
		if !ok || c == 0 {
			continue
		}
		if order[i].Desc {
			c = -c
		}
		return c
	}
	if aToken < bToken {
		return -1
	}
	if aToken > bToken {
		return 1
	}
	return 0
}

func joinedScope(left, right *Table) *Scope {
	return newScope(
		tableSchema{table: left.Name, schema: left.Schema},
		tableSchema{table: right.Name, schema: right.Schema},
	)
}

func joinLocated(leftRows, rightRows []located, jt JoinType, on Expr, scope *Scope, leftWidth, rightWidth int, seed uint64) []located {
	out := make([]located, 0, len(leftRows))
	matchedRight := make([]bool, len(rightRows))
	for _, l := range leftRows {
		matched := false
		for ri, r := range rightRows {
			vals := make([]Value, 0, len(l.row.Values)+len(r.row.Values))
			vals = append(vals, l.row.Values...)
			vals = append(vals, r.row.Values...)
			row := Row{
				Values: vals,
				scope:  scope,
			}
			tok := combineTokens(l.token, r.token)
			if evalTrue(on, row, tok, seed) {
				matched = true
				matchedRight[ri] = true
				out = append(out, located{row: row, token: tok})
			}
		}
		if !matched && jt.preservesLeft() {
			row := Row{
				Values: append(append([]Value(nil), l.row.Values...), nullValues(rightWidth)...),
				scope:  scope,
			}
			out = append(out, located{row: row, token: l.token})
		}
	}
	if jt.preservesRight() {
		for ri, r := range rightRows {
			if matchedRight[ri] {
				continue
			}
			row := Row{
				Values: append(nullValues(leftWidth), r.row.Values...),
				scope:  scope,
			}
			out = append(out, located{row: row, token: r.token})
		}
	}
	return out
}

func nullValues(n int) []Value {
	vals := make([]Value, n)
	for i := range vals {
		vals[i] = Null()
	}
	return vals
}

func recordsFromLocated(records []located) []outRecord {
	out := make([]outRecord, len(records))
	for i := range records {
		out[i] = outRecord{row: records[i].row, token: records[i].token}
	}
	return out
}

func recordsFromGroups(groups []*groupRecord) []outRecord {
	out := make([]outRecord, len(groups))
	for i, g := range groups {
		out[i] = outRecord{row: g.rep, aggs: g.values, token: g.token}
	}
	return out
}

func projectRecords(records []outRecord, q *Query, scope *Scope, join bool, seed uint64) (*Result, error) {
	items := q.Select
	orderItems := resolvedOrderItems(q)
	result := &Result{}
	if len(items) == 0 {
		for _, bc := range scope.columns() {
			expr := ColOf(bc.Table, bc.Column.Name)
			name := bc.Column.Name
			if join {
				name = bc.Table + "." + bc.Column.Name
			}
			items = append(items, SelectItem{Expr: expr, Alias: name})
		}
	}
	result.Columns = make([]string, len(items))
	for i, item := range items {
		if item.Alias != "" {
			result.Columns[i] = item.Alias
		} else if ref, ok := item.Expr.(*ColumnRef); ok && join && ref.Table != "" {
			result.Columns[i] = ref.Table + "." + ref.Name
		} else {
			result.Columns[i] = item.Expr.String()
		}
	}

	type projected struct {
		values []Value
		order  []Value
		token  uint64
	}
	rows := make([]projected, 0, len(records))
	for _, rec := range records {
		env := &EvalEnv{Row: rec.row, Aggs: rec.aggs, Token: rec.token, Seed: seed}
		vals := make([]Value, len(items))
		for i, item := range items {
			vals[i] = item.Expr.Eval(env)
		}
		order := make([]Value, len(orderItems))
		for i, o := range orderItems {
			order[i] = o.Expr.Eval(env)
		}
		rows = append(rows, projected{values: vals, order: order, token: rec.token})
	}
	if len(orderItems) > 0 {
		sort.SliceStable(rows, func(i, j int) bool {
			return compareOrderValues(rows[i].order, rows[j].order, orderItems, rows[i].token, rows[j].token) < 0
		})
	}
	start := int64(0)
	if q.Offset > 0 {
		if q.Offset >= int64(len(rows)) {
			start = int64(len(rows))
		} else {
			start = q.Offset
		}
	}
	end := int64(len(rows))
	if q.Limit >= 0 && start+q.Limit < end {
		end = start + q.Limit
	}
	for _, r := range rows[start:end] {
		result.Rows = append(result.Rows, r.values)
	}
	return result, nil
}
