package federate

import (
	"context"
	"fmt"
)

// Reference evaluates a query by pulling every required row to one machine and
// running a plain, non-pushdown execution. It is the correctness oracle for the
// federated planner/executor.
func Reference(ctx context.Context, cat *Catalog, q *Query) (*Result, error) {
	if cat == nil {
		return nil, fmt.Errorf("nil catalog")
	}
	if err := q.validate(cat); err != nil {
		return nil, err
	}
	left, _ := cat.Table(q.From)
	var right *Table
	if q.Join != nil {
		right, _ = cat.Table(q.Join.Table)
	}
	if err := validateQueryColumns(q, left, right); err != nil {
		return nil, err
	}
	seed := q.Seed
	var records []located
	var scope *Scope
	join := right != nil
	if !join {
		records = scanTable(left, nil, seed, nil, false)
		records = filterLocated(records, q.Where, seed)
		scope = newScope(tableSchema{table: left.Name, schema: left.Schema})
	} else {
		leftRows := scanTable(left, nil, seed, nil, false)
		rightRows := scanTable(right, nil, seed, nil, false)
		lw, rw := len(left.Schema.Columns), len(right.Schema.Columns)
		scope = joinedScope(left, right)
		records = joinLocated(leftRows, rightRows, q.Join.Type, q.Join.On, scope, lw, rw, seed)
		records = filterLocated(records, q.Where, seed)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	aggs := q.aggregates()
	if len(q.GroupBy) > 0 || len(aggs) > 0 {
		gs := accumulateGroups(records, q.GroupBy, aggs, seed)
		finalizeGroups(gs, aggs)
		groups := applyHavingGroups(gs.order, q.Having, seed)
		return projectRecords(recordsFromGroups(groups), q, scope, join, seed)
	}
	return projectRecords(recordsFromLocated(records), q, scope, join, seed)
}
