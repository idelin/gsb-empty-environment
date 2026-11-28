package federate

import (
	"context"
	"fmt"
)

// Execute plans and runs a query on the simulated federation.
func Execute(ctx context.Context, cat *Catalog, q *Query) (*Result, *Stats, error) {
	plan, err := NewPlanner(cat).Plan(q)
	if err != nil {
		return nil, nil, err
	}
	return ExecutePlan(ctx, cat, plan)
}

// ExecutePlan runs a previously produced plan.
func ExecutePlan(ctx context.Context, cat *Catalog, plan *Plan) (*Result, *Stats, error) {
	if plan == nil || plan.Query == nil {
		return nil, nil, fmt.Errorf("nil plan")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	q := plan.Query
	if plan.Aggregation == nil || plan.TopK == nil || (q.Join != nil && plan.Join == nil) {
		cp := *plan
		if cp.Aggregation == nil {
			cp.Aggregation = &AggregationPlan{Mode: AggNone}
		}
		if cp.TopK == nil {
			cp.TopK = &TopKPlan{ShardLimit: -1, CoordinatorLimit: q.Limit, CoordinatorOffset: q.Offset}
		}
		if q.Join != nil && cp.Join == nil {
			cp.Join = &JoinPlan{Reason: "no join decision in plan; falling back to coordinator"}
		}
		plan = &cp
	}
	if q.Join == nil {
		return executeSingle(ctx, cat, plan)
	}
	return executeJoin(ctx, cat, plan)
}

func executeSingle(ctx context.Context, cat *Catalog, plan *Plan) (*Result, *Stats, error) {
	q := plan.Query
	t, ok := cat.Table(q.From)
	if !ok {
		return nil, nil, fmt.Errorf("table %q not found", q.From)
	}
	seed := q.Seed
	stats := &Stats{}
	aggs := q.aggregates()
	mode := plan.Aggregation.Mode
	if plan.Filter.Coordinator != nil && mode != AggNone {
		mode = AggCoordinator
	}
	scope := newScope(tableSchema{table: t.Name, schema: t.Schema})
	orderItems := resolvedOrderItems(q)

	switch mode {
	case AggNone:
		var all []located
		for _, id := range t.ScanShards() {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			order := []OrderItem(nil)
			limit := int64(-1)
			if plan.TopK.Pushed {
				order = orderItems
				limit = plan.TopK.ShardLimit
			}
			all = append(all, scanShard(t, id, plan.Filter.Shard, order, limit, seed, stats, true)...)
		}
		all = filterLocated(all, plan.Filter.Coordinator, seed)
		res, err := projectRecords(recordsFromLocated(all), q, scope, false, seed)
		stats.CoordinatorRows = len(resRows(res))
		return res, stats, err

	case AggSingleStage:
		var groups []*groupRecord
		for _, id := range t.ScanShards() {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			rows := scanShard(t, id, plan.Filter.Shard, nil, -1, seed, stats, false)
			gs := accumulateGroups(rows, q.GroupBy, aggs, seed)
			finalizeGroups(gs, aggs)
			local := gs.order
			if plan.Aggregation.HavingPushdown {
				local = applyHavingGroups(local, q.Having, seed)
			}
			if plan.TopK.Pushed {
				local = sortLimitGroups(local, orderItems, plan.TopK.ShardLimit, seed)
			}
			groups = append(groups, local...)
			stats.RowsTransferred += len(local)
		}
		if !plan.Aggregation.HavingPushdown {
			groups = applyHavingGroups(groups, q.Having, seed)
		}
		res, err := projectRecords(recordsFromGroups(groups), q, scope, false, seed)
		stats.CoordinatorRows = len(resRows(res))
		return res, stats, err

	case AggPartialFinal:
		var parts []*groupSet
		for _, id := range t.ScanShards() {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			rows := scanShard(t, id, plan.Filter.Shard, nil, -1, seed, stats, false)
			gs := accumulateGroups(rows, q.GroupBy, aggs, seed)
			parts = append(parts, gs)
			stats.RowsTransferred += len(gs.order)
			stats.PartialGroups += len(gs.order)
		}
		merged := mergeGroupSets(parts, aggs)
		finalizeGroups(merged, aggs)
		groups := applyHavingGroups(merged.order, q.Having, seed)
		res, err := projectRecords(recordsFromGroups(groups), q, scope, false, seed)
		stats.CoordinatorRows = len(resRows(res))
		return res, stats, err

	case AggCoordinator:
		all := scanTable(t, plan.Filter.Shard, seed, stats, true)
		all = filterLocated(all, plan.Filter.Coordinator, seed)
		gs := accumulateGroups(all, q.GroupBy, aggs, seed)
		finalizeGroups(gs, aggs)
		groups := applyHavingGroups(gs.order, q.Having, seed)
		res, err := projectRecords(recordsFromGroups(groups), q, scope, false, seed)
		stats.CoordinatorRows = len(resRows(res))
		return res, stats, err

	default:
		return nil, nil, fmt.Errorf("unknown aggregation mode %q", mode)
	}
}

func executeJoin(ctx context.Context, cat *Catalog, plan *Plan) (*Result, *Stats, error) {
	q := plan.Query
	left, ok := cat.Table(q.From)
	if !ok {
		return nil, nil, fmt.Errorf("table %q not found", q.From)
	}
	right, ok := cat.Table(q.Join.Table)
	if !ok {
		return nil, nil, fmt.Errorf("table %q not found", q.Join.Table)
	}
	seed := q.Seed
	stats := &Stats{}
	aggs := q.aggregates()
	scope := joinedScope(left, right)
	lw, rw := len(left.Schema.Columns), len(right.Schema.Columns)
	mode := plan.Aggregation.Mode
	if plan.Filter.Coordinator != nil && mode != AggNone {
		mode = AggCoordinator
	}
	orderItems := resolvedOrderItems(q)

	if !plan.Join.Pushed {
		leftRows := scanTable(left, plan.Filter.Left, seed, stats, true)
		rightRows := scanTable(right, plan.Filter.Right, seed, stats, true)
		joined := joinLocated(leftRows, rightRows, q.Join.Type, q.Join.On, scope, lw, rw, seed)
		joined = filterLocated(joined, plan.Filter.Coordinator, seed)
		return finishJoined(joined, plan, scope, mode, aggs, seed, stats)
	}

	var raw []located
	var groups []*groupRecord
	var parts []*groupSet
	for _, shardID := range joinShardIDs(left, right) {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		ls, rs := shardID, shardID
		var replicaOK bool
		switch {
		case left.Dist.Kind == ReplicatedKind && right.Dist.Kind == ReplicatedKind:
			ls = 0
			rs, replicaOK = replicaShardForNode(right, left.Shards[ls].Node)
		case left.Dist.Kind == ReplicatedKind:
			rs = shardID
			ls, replicaOK = replicaShardForNode(left, right.Shards[rs].Node)
		case right.Dist.Kind == ReplicatedKind:
			ls = shardID
			rs, replicaOK = replicaShardForNode(right, left.Shards[ls].Node)
		default:
			replicaOK = true
		}
		if !replicaOK {
			return nil, nil, fmt.Errorf("no local replica on required node for shard %d", shardID)
		}
		leftRows := scanShard(left, ls, plan.Filter.Left, nil, -1, seed, stats, false)
		rightRows := scanShard(right, rs, plan.Filter.Right, nil, -1, seed, stats, false)
		joined := joinLocated(leftRows, rightRows, q.Join.Type, q.Join.On, scope, lw, rw, seed)
		switch mode {
		case AggNone:
			if plan.TopK.Pushed {
				sortLocated(joined, orderItems, seed)
				joined = limitLocated(joined, plan.TopK.ShardLimit)
			}
			raw = append(raw, joined...)
			stats.RowsTransferred += len(joined)
		case AggSingleStage:
			gs := accumulateGroups(joined, q.GroupBy, aggs, seed)
			finalizeGroups(gs, aggs)
			local := gs.order
			if plan.Aggregation.HavingPushdown {
				local = applyHavingGroups(local, q.Having, seed)
			}
			if plan.TopK.Pushed {
				local = sortLimitGroups(local, orderItems, plan.TopK.ShardLimit, seed)
			}
			groups = append(groups, local...)
			stats.RowsTransferred += len(local)
		case AggPartialFinal:
			gs := accumulateGroups(joined, q.GroupBy, aggs, seed)
			parts = append(parts, gs)
			stats.RowsTransferred += len(gs.order)
			stats.PartialGroups += len(gs.order)
		case AggCoordinator:
			raw = append(raw, joined...)
			stats.RowsTransferred += len(joined)
		}
	}

	switch mode {
	case AggNone:
		raw = filterLocated(raw, plan.Filter.Coordinator, seed)
		return finishJoined(raw, plan, scope, mode, aggs, seed, stats)
	case AggSingleStage:
		if plan.Filter.Coordinator != nil {
			return nil, nil, fmt.Errorf("internal: single-stage join aggregation with coordinator filter")
		}
		if !plan.Aggregation.HavingPushdown {
			groups = applyHavingGroups(groups, q.Having, seed)
		}
		res, err := projectRecords(recordsFromGroups(groups), q, scope, true, seed)
		stats.CoordinatorRows = len(resRows(res))
		return res, stats, err
	case AggPartialFinal:
		if plan.Filter.Coordinator != nil {
			return nil, nil, fmt.Errorf("internal: partial join aggregation with coordinator filter")
		}
		merged := mergeGroupSets(parts, aggs)
		finalizeGroups(merged, aggs)
		groups = applyHavingGroups(merged.order, q.Having, seed)
		res, err := projectRecords(recordsFromGroups(groups), q, scope, true, seed)
		stats.CoordinatorRows = len(resRows(res))
		return res, stats, err
	case AggCoordinator:
		raw = filterLocated(raw, plan.Filter.Coordinator, seed)
		return finishJoined(raw, plan, scope, mode, aggs, seed, stats)
	default:
		return nil, nil, fmt.Errorf("unknown aggregation mode %q", mode)
	}
}

func replicaShardForNode(t *Table, node string) (int, bool) {
	for _, sh := range t.Shards {
		if sh.Node == node {
			return sh.ID, true
		}
	}
	return 0, false
}

func finishJoined(records []located, plan *Plan, scope *Scope, mode AggMode, aggs []*AggExpr, seed uint64, stats *Stats) (*Result, *Stats, error) {
	q := plan.Query
	if mode == AggNone {
		res, err := projectRecords(recordsFromLocated(records), q, scope, true, seed)
		stats.CoordinatorRows = len(resRows(res))
		return res, stats, err
	}
	gs := accumulateGroups(records, q.GroupBy, aggs, seed)
	finalizeGroups(gs, aggs)
	groups := applyHavingGroups(gs.order, q.Having, seed)
	res, err := projectRecords(recordsFromGroups(groups), q, scope, true, seed)
	stats.CoordinatorRows = len(resRows(res))
	return res, stats, err
}

func joinShardIDs(left, right *Table) []int {
	switch {
	case left.Dist.Kind == ReplicatedKind && right.Dist.Kind == ReplicatedKind:
		return []int{0}
	case left.Dist.Kind == ReplicatedKind:
		return right.ScanShards()
	case right.Dist.Kind == ReplicatedKind:
		return left.ScanShards()
	default:
		n := len(left.Shards)
		if len(right.Shards) < n {
			n = len(right.Shards)
		}
		ids := make([]int, n)
		for i := range ids {
			ids[i] = i
		}
		return ids
	}
}

func filterLocated(records []located, filter Expr, seed uint64) []located {
	if filter == nil {
		return records
	}
	out := records[:0]
	for _, rec := range records {
		if evalTrue(filter, rec.row, rec.token, seed) {
			out = append(out, rec)
		}
	}
	return out
}

func resRows(r *Result) [][]Value {
	if r == nil {
		return nil
	}
	return r.Rows
}
