package federate

import (
	"fmt"
	"math"
	"strings"
)

// AggMode describes where grouping is evaluated.
type AggMode string

const (
	AggNone         AggMode = "none"
	AggSingleStage  AggMode = "single_stage"
	AggPartialFinal AggMode = "partial_final"
	AggCoordinator  AggMode = "coordinator"
)

// PartialKind describes the local aggregate state emitted by a shard.
type PartialKind string

const (
	PartialComplete    PartialKind = "complete"
	PartialCount       PartialKind = "count"
	PartialSum         PartialKind = "sum"
	PartialSumCount    PartialKind = "sum_count"
	PartialMin         PartialKind = "min"
	PartialMax         PartialKind = "max"
	PartialDistinctSet PartialKind = "distinct_set"
)

// FinalKind describes the coordinator merge for a partial aggregate.
type FinalKind string

const (
	FinalComplete      FinalKind = "complete"
	FinalSumCounts     FinalKind = "sum_counts"
	FinalSum           FinalKind = "sum"
	FinalAvg           FinalKind = "avg"
	FinalMin           FinalKind = "min"
	FinalMax           FinalKind = "max"
	FinalCountDistinct FinalKind = "count_distinct"
	FinalSumDistinct   FinalKind = "sum_distinct"
	FinalAvgDistinct   FinalKind = "avg_distinct"
)

// AggSpec documents the two-stage rewrite of one aggregate.
type AggSpec struct {
	Agg          *AggExpr
	Key          string
	Partial      PartialKind
	Final        FinalKind
	Decomposable bool
	Note         string
}

// AggregationPlan is the inspectable grouping/pushdown decision.
type AggregationPlan struct {
	Mode           AggMode
	GroupBy        []Expr
	Specs          []AggSpec
	Colocated      bool
	HavingPushdown bool
	Reason         string
}

// FilterPlan records which WHERE conjuncts execute remotely.
type FilterPlan struct {
	Shard       Expr
	Left        Expr
	Right       Expr
	Coordinator Expr
	Pushed      bool
	Reason      string
}

// JoinPlan records the co-location decision for the optional join.
type JoinPlan struct {
	Pushed       bool
	Colocated    bool
	Layout       string
	VolatileFree bool
	NullSafe     bool
	Reason       string
}

// TopKPlan records the corrected LIMIT/OFFSET rewrite.
type TopKPlan struct {
	Pushed            bool
	ShardLimit        int64
	ShardOffset       int64
	CoordinatorLimit  int64
	CoordinatorOffset int64
	Reason            string
}

// Decision is a human-readable planner audit entry.
type Decision struct {
	Rule   string
	Pushed bool
	Detail string
}

// Plan is the output of the federated pushdown planner.
type Plan struct {
	Query       *Query
	Filter      FilterPlan
	Join        *JoinPlan
	Aggregation *AggregationPlan
	TopK        *TopKPlan
	Decisions   []Decision
}

// Planner optimises logical queries for a catalog.
type Planner struct {
	Catalog *Catalog
}

func NewPlanner(cat *Catalog) *Planner {
	return &Planner{Catalog: cat}
}

// Plan validates a query and produces all pushdown decisions.
func (p *Planner) Plan(q *Query) (*Plan, error) {
	if p == nil || p.Catalog == nil {
		return nil, fmt.Errorf("planner has no catalog")
	}
	if err := q.validate(p.Catalog); err != nil {
		return nil, err
	}
	left, _ := p.Catalog.Table(q.From)
	var right *Table
	if q.Join != nil {
		right, _ = p.Catalog.Table(q.Join.Table)
	}
	if err := validateQueryColumns(q, left, right); err != nil {
		return nil, err
	}
	if q.Limit >= 0 && q.Offset > math.MaxInt64-q.Limit {
		return nil, fmt.Errorf("LIMIT + OFFSET overflows int64")
	}

	plan := &Plan{Query: q}
	plan.Join = evaluateJoin(q, left, right)
	plan.Filter = buildFilterPlan(q, left, right)

	hasGrouping := len(q.GroupBy) > 0 || queryHasAggregate(q)
	if hasGrouping {
		plan.Aggregation = buildAggregationPlan(q, left, right, plan.Join, plan.Filter.Coordinator)
	} else {
		plan.Aggregation = &AggregationPlan{Mode: AggNone, Reason: "no GROUP BY or aggregates"}
	}
	plan.TopK = buildTopKPlan(q, plan)

	plan.Decisions = append(plan.Decisions,
		Decision{Rule: "filter_pushdown", Pushed: plan.Filter.Pushed, Detail: plan.Filter.Reason},
	)
	if plan.Join != nil {
		plan.Decisions = append(plan.Decisions,
			Decision{Rule: "join_pushdown", Pushed: plan.Join.Pushed, Detail: plan.Join.Reason},
		)
	}
	plan.Decisions = append(plan.Decisions,
		Decision{Rule: "aggregation", Pushed: plan.Aggregation.Mode == AggSingleStage || plan.Aggregation.Mode == AggPartialFinal, Detail: plan.Aggregation.Reason},
		Decision{Rule: "having_pushdown", Pushed: plan.Aggregation.HavingPushdown, Detail: havingReason(plan.Aggregation)},
		Decision{Rule: "topk", Pushed: plan.TopK.Pushed, Detail: plan.TopK.Reason},
	)
	return plan, nil
}

func havingReason(a *AggregationPlan) string {
	if a == nil || a.Mode == AggNone {
		return "no HAVING"
	}
	if a.HavingPushdown {
		return "co-located groups; HAVING evaluated on shards"
	}
	return "HAVING must be evaluated at coordinator after final aggregation"
}

func validateQueryColumns(q *Query, left, right *Table) error {
	var ts []tableSchema
	ts = append(ts, tableSchema{table: left.Name, schema: left.Schema})
	if right != nil {
		ts = append(ts, tableSchema{table: right.Name, schema: right.Schema})
	}
	scope := newScope(ts...)
	check := func(e Expr) error {
		if e == nil {
			return nil
		}
		for _, ref := range ColumnRefs(e) {
			if _, ok := scope.resolve(ref.Table, ref.Name); !ok {
				if ref.Table == "" && scope != nil && scope.ambiguous[ref.Name] {
					return fmt.Errorf("ambiguous column reference %q", ref.Name)
				}
				return fmt.Errorf("unknown column %s", ref.String())
			}
		}
		return nil
	}
	exprs := []Expr{q.Where, q.Having, q.JoinOn()}
	for _, g := range q.GroupBy {
		exprs = append(exprs, g)
	}
	for _, s := range q.Select {
		exprs = append(exprs, s.Expr)
	}
	for _, o := range q.OrderBy {
		if _, ok := o.Expr.(*ColumnRef); ok && resolveAliasExpr(q, o.Expr) != o.Expr {
			continue
		}
		exprs = append(exprs, o.Expr)
	}
	for _, e := range exprs {
		if err := check(e); err != nil {
			return err
		}
	}
	return nil
}

func (q *Query) JoinOn() Expr {
	if q.Join == nil {
		return nil
	}
	return q.Join.On
}

func queryContainsVolatile(q *Query) bool {
	check := func(e Expr) bool { return e != nil && e.Volatility() == Volatile }
	if check(q.Where) || check(q.Having) || check(q.JoinOn()) {
		return true
	}
	for _, g := range q.GroupBy {
		if check(g) {
			return true
		}
	}
	for _, s := range q.Select {
		if check(s.Expr) {
			return true
		}
	}
	for _, o := range q.OrderBy {
		if check(o.Expr) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Join analysis

type joinPair struct {
	Left      *ColumnRef
	Right     *ColumnRef
	LeftColl  string
	RightColl string
	IsString  bool
}

func evaluateJoin(q *Query, left, right *Table) *JoinPlan {
	if right == nil {
		return nil
	}
	jp := &JoinPlan{
		Colocated:    false,
		Layout:       left.Dist.Signature() + " <> " + right.Dist.Signature(),
		VolatileFree: !queryContainsVolatile(q),
		NullSafe:     true,
	}
	var reasons []string
	fail := func(reason string) {
		reasons = append(reasons, reason)
	}

	if !jp.VolatileFree {
		fail("query contains a VOLATILE expression; join cannot be reordered/pushed")
	}
	if (q.Join.Type == RightJoin || q.Join.Type == FullJoin) && len(q.OrderBy) == 0 &&
		len(joinShardIDs(left, right)) > 1 {
		fail("right/full join without ORDER BY would expose shard-block output order")
	}
	if left.Dist.Kind == ReplicatedKind && right.Dist.Kind != ReplicatedKind &&
		len(q.OrderBy) == 0 && len(right.ScanShards()) > 1 {
		fail("replicated-left join without ORDER BY would expose shard-block output order")
	}
	if left.Dist.Kind == ReplicatedKind && right.Dist.Kind != ReplicatedKind && q.Join.Type.preservesLeft() {
		fail("outer join preserving a replicated side would duplicate NULL-extended rows per shard")
	}
	if right.Dist.Kind == ReplicatedKind && left.Dist.Kind != ReplicatedKind && q.Join.Type.preservesRight() {
		fail("outer join preserving a replicated side would duplicate NULL-extended rows per shard")
	}

	pairs, err := joinEqualityPairs(q.Join.On, left, right)
	if err != nil {
		fail(err.Error())
		pairs = nil
	}
	if len(pairs) == 0 {
		fail("JOIN ON has no equality between left and right columns")
	}

	// Collation safety: a non-binary or differing collation can make two
	// strings equal even though they hash to different shards.
	for _, pair := range pairs {
		if pair.IsString && !CollationNamesCompatible(pair.LeftColl, pair.RightColl) {
			fail(fmt.Sprintf("string join key collation is not shard-compatible (%q vs %q)",
				pair.LeftColl, pair.RightColl))
		}
	}

	// Outer joins: be conservative around nullable keys and NULL extension.
	if q.Join.Type.outer() {
		jp.NullSafe = true
		for _, pair := range pairs {
			lc, _ := left.Schema.Column(pair.Left.Name)
			rc, _ := right.Schema.Column(pair.Right.Name)
			if lc.Nullable || rc.Nullable {
				jp.NullSafe = false
				fail("outer join key is nullable; NULL-extension rows could be mis-routed")
				break
			}
		}
	}

	layoutOK := true
	switch {
	case left.Dist.Kind == ReplicatedKind && right.Dist.Kind == ReplicatedKind:
		jp.Layout = "both tables replicated"
	case left.Dist.Kind == ReplicatedKind:
		if left.Dist.ShardCount != right.Dist.ShardCount {
			layoutOK = false
			fail(fmt.Sprintf("replicated table has %d copies but sharded table has %d shards",
				left.Dist.ShardCount, right.Dist.ShardCount))
		} else {
			jp.Layout = "replicated left on every shard of right"
		}
	case right.Dist.Kind == ReplicatedKind:
		if right.Dist.ShardCount != left.Dist.ShardCount {
			layoutOK = false
			fail(fmt.Sprintf("replicated table has %d copies but sharded table has %d shards",
				right.Dist.ShardCount, left.Dist.ShardCount))
		} else {
			jp.Layout = "replicated right on every shard of left"
		}
	default:
		if !CompatibleLayout(left.Dist, right.Dist) {
			layoutOK = false
			fail("tables use different shard functions or shard mappings")
		} else {
			jp.Layout = left.Dist.Signature()
		}
	}

	if layoutOK {
		if !sameNodePlacement(left, right) {
			layoutOK = false
			fail("shard placement nodes differ; shards are not physically co-located")
		}
	}
	if layoutOK {
		if left.Dist.Kind != ReplicatedKind && right.Dist.Kind != ReplicatedKind {
			if !positionallyCoversShardKeys(left, right, pairs) {
				fail("join equality does not map composite shard keys positionally")
			}
		} else {
			if left.Dist.Kind != ReplicatedKind && !coversShardKey(left, left.Name, pairs) {
				fail("join equality does not cover left table's distribution key")
			}
			if right.Dist.Kind != ReplicatedKind && !coversShardKey(right, right.Name, pairs) {
				fail("join equality does not cover right table's distribution key")
			}
		}
	}

	jp.Colocated = len(reasons) == 0
	jp.Pushed = jp.Colocated
	if jp.Pushed {
		jp.Reason = "compatible layout, equality covers shard keys, no volatile/unsafe collation"
	} else {
		jp.Reason = strings.Join(reasons, "; ")
	}
	return jp
}

func sameNodePlacement(left, right *Table) bool {
	switch {
	case left.Dist.Kind == ReplicatedKind && right.Dist.Kind == ReplicatedKind:
		return left.Shards[0].Node == right.Shards[0].Node
	case left.Dist.Kind == ReplicatedKind:
		nodes := make(map[string]struct{}, len(left.Shards))
		for _, sh := range left.Shards {
			nodes[sh.Node] = struct{}{}
		}
		for _, sh := range right.Shards {
			if _, ok := nodes[sh.Node]; !ok {
				return false
			}
		}
		return true
	case right.Dist.Kind == ReplicatedKind:
		nodes := make(map[string]struct{}, len(right.Shards))
		for _, sh := range right.Shards {
			nodes[sh.Node] = struct{}{}
		}
		for _, sh := range left.Shards {
			if _, ok := nodes[sh.Node]; !ok {
				return false
			}
		}
		return true
	default:
		if len(left.Shards) != len(right.Shards) {
			return false
		}
		for i := range left.Shards {
			if left.Shards[i].Node != right.Shards[i].Node {
				return false
			}
		}
		return true
	}
}

func joinEqualityPairs(on Expr, left, right *Table) ([]joinPair, error) {
	var out []joinPair
	for _, c := range SplitConjuncts(on) {
		b, ok := c.(*BinaryExpr)
		if !ok || b.Op != "=" {
			continue
		}
		lc, lcoll, lok := stripColumn(b.Left)
		rc, rcoll, rok := stripColumn(b.Right)
		if !lok || !rok {
			continue
		}
		lt, lok := resolveRefTable(lc, left, right)
		rt, rok := resolveRefTable(rc, right, left)
		if !lok || !rok || lt == rt {
			continue
		}
		if lt != left.Name {
			lc, rc = rc, lc
			lcoll, rcoll = rcoll, lcoll
			lt, rt = rt, lt
		}
		if lt != left.Name || rt != right.Name {
			continue
		}
		lc = &ColumnRef{Table: lt, Name: lc.Name}
		rc = &ColumnRef{Table: rt, Name: rc.Name}
		colL, okL := left.Schema.Column(lc.Name)
		colR, okR := right.Schema.Column(rc.Name)
		if !okL || !okR {
			continue
		}
		if lcoll == "" {
			lcoll = colL.Collation
		}
		if rcoll == "" {
			rcoll = colR.Collation
		}
		out = append(out, joinPair{
			Left:      lc,
			Right:     rc,
			LeftColl:  lcoll,
			RightColl: rcoll,
			IsString:  colL.Type == TypeString && colR.Type == TypeString,
		})
	}
	return out, nil
}

func stripColumn(e Expr) (*ColumnRef, string, bool) {
	switch n := e.(type) {
	case *ColumnRef:
		return n, "", true
	case *CollateExpr:
		if c, _, ok := stripColumn(n.Expr); ok {
			return c, n.Collation, true
		}
	}
	return nil, "", false
}

func resolveRefTable(ref *ColumnRef, primary, secondary *Table) (string, bool) {
	if ref.Table != "" {
		if strings.EqualFold(ref.Table, primary.Name) {
			return primary.Name, true
		}
		if secondary != nil && strings.EqualFold(ref.Table, secondary.Name) {
			return secondary.Name, true
		}
		return "", false
	}
	_, inP := primary.Schema.Column(ref.Name)
	inS := false
	if secondary != nil {
		_, inS = secondary.Schema.Column(ref.Name)
	}
	if inP && !inS {
		return primary.Name, true
	}
	return "", false
}

func coversShardKey(t *Table, tableName string, pairs []joinPair) bool {
	for _, key := range t.Dist.KeyColumns {
		found := false
		for _, p := range pairs {
			if (p.Left.Table != "" && strings.EqualFold(p.Left.Table, tableName) &&
				strings.EqualFold(p.Left.Name, key)) ||
				(p.Right.Table != "" && strings.EqualFold(p.Right.Table, tableName) &&
					strings.EqualFold(p.Right.Name, key)) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// positionallyCoversShardKeys verifies that join equality maps left shard key
// i to right shard key i. Crossed composite keys such as (a,b) = (b,a) pass a
// naive presence check but do not preserve the hash mapping.
func positionallyCoversShardKeys(left, right *Table, pairs []joinPair) bool {
	if len(left.Dist.KeyColumns) != len(right.Dist.KeyColumns) {
		return false
	}
	for i, lk := range left.Dist.KeyColumns {
		rk := right.Dist.KeyColumns[i]
		found := false
		for _, p := range pairs {
			if p.Left.Table != "" && p.Right.Table != "" &&
				strings.EqualFold(p.Left.Table, left.Name) &&
				strings.EqualFold(p.Left.Name, lk) &&
				strings.EqualFold(p.Right.Table, right.Name) &&
				strings.EqualFold(p.Right.Name, rk) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Filter analysis

func buildFilterPlan(q *Query, left, right *Table) FilterPlan {
	fp := FilterPlan{}
	if q.Where == nil {
		fp.Pushed = true
		fp.Reason = "no WHERE predicate"
		return fp
	}
	var shard, leftF, rightF, coord []Expr
	for _, c := range SplitConjuncts(q.Where) {
		if containsVolatile(c) {
			coord = append(coord, c)
			continue
		}
		if right == nil {
			shard = append(shard, c)
			continue
		}
		side := expressionSide(c, left.Name, right.Name)
		switch q.Join.Type {
		case InnerJoin:
			switch side {
			case "left":
				leftF = append(leftF, c)
			case "right":
				rightF = append(rightF, c)
			default:
				coord = append(coord, c)
			}
		case LeftJoin:
			if side == "left" {
				leftF = append(leftF, c)
			} else {
				coord = append(coord, c)
			}
		case RightJoin:
			if side == "right" {
				rightF = append(rightF, c)
			} else {
				coord = append(coord, c)
			}
		default:
			coord = append(coord, c)
		}
	}
	fp.Shard = And(shard...)
	if len(shard) == 0 {
		fp.Shard = nil
	}
	fp.Left = And(leftF...)
	if len(leftF) == 0 {
		fp.Left = nil
	}
	fp.Right = And(rightF...)
	if len(rightF) == 0 {
		fp.Right = nil
	}
	fp.Coordinator = And(coord...)
	if len(coord) == 0 {
		fp.Coordinator = nil
	}
	fp.Pushed = fp.Coordinator == nil
	switch {
	case fp.Pushed:
		fp.Reason = "all WHERE conjuncts pushed to shards"
	case fp.Shard != nil || fp.Left != nil || fp.Right != nil:
		fp.Reason = "some conjuncts pushed; remaining predicate evaluated at coordinator"
	default:
		fp.Reason = "WHERE predicate is not safe to push (VOLATILE or outer-join null-side)"
	}
	return fp
}

func expressionSide(e Expr, leftName, rightName string) string {
	sawLeft, sawRight := false, false
	for _, ref := range ColumnRefs(e) {
		switch {
		case ref.Table != "" && strings.EqualFold(ref.Table, leftName):
			sawLeft = true
		case ref.Table != "" && strings.EqualFold(ref.Table, rightName):
			sawRight = true
		}
	}
	switch {
	case sawLeft && sawRight:
		return "both"
	case sawLeft:
		return "left"
	case sawRight:
		return "right"
	default:
		return "none"
	}
}

// ---------------------------------------------------------------------------
// Aggregation analysis

func buildAggregationPlan(q *Query, left, right *Table, join *JoinPlan, coordinatorFilter Expr) *AggregationPlan {
	ap := &AggregationPlan{GroupBy: q.GroupBy}
	aggs := q.aggregates()
	colocated := aggregationColocated(q, left, right, join)
	ap.Colocated = colocated

	pushableData := (join == nil || join.Pushed) && coordinatorFilter == nil
	volatileGrouping := false
	for _, g := range q.GroupBy {
		if containsVolatile(g) {
			volatileGrouping = true
		}
	}
	for _, a := range aggs {
		if a.Arg != nil && containsVolatile(a.Arg) {
			volatileGrouping = true
		}
	}

	switch {
	case coordinatorFilter != nil:
		ap.Mode = AggCoordinator
		ap.Reason = "coordinator-side WHERE remains; aggregate after the filter is complete"
		ap.HavingPushdown = false
	case colocated && !volatileGrouping:
		ap.Mode = AggSingleStage
		ap.Reason = "GROUP BY covers the distribution key; complete aggregate per shard"
		ap.HavingPushdown = !containsVolatile(q.Having)
	case pushableData && !volatileGrouping:
		ap.Mode = AggPartialFinal
		ap.Reason = "GROUP BY omits the distribution key; partial aggregate on shards + final merge"
		ap.HavingPushdown = false
	default:
		ap.Mode = AggCoordinator
		if volatileGrouping {
			ap.Reason = "VOLATILE grouping/aggregate argument; evaluate at coordinator"
		} else {
			ap.Reason = "join is not pushed; aggregate at coordinator"
		}
		ap.HavingPushdown = false
	}

	for _, a := range aggs {
		ap.Specs = append(ap.Specs, makeAggSpec(a, left, right, ap.Mode))
	}
	return ap
}

func aggregationColocated(q *Query, left, right *Table, join *JoinPlan) bool {
	if len(q.GroupBy) == 0 && !queryHasAggregate(q) {
		return true
	}
	if right == nil {
		return groupCoversShardKey(q.GroupBy, left)
	}
	if join == nil || !join.Pushed {
		return false
	}
	if left.Dist.Kind != ReplicatedKind {
		return groupCoversShardKey(q.GroupBy, left)
	}
	if right.Dist.Kind != ReplicatedKind {
		return groupCoversShardKey(q.GroupBy, right)
	}
	return true // both replicated: one-node execution
}

func groupCoversShardKey(groupBy []Expr, t *Table) bool {
	if t.Dist.Kind == ReplicatedKind || len(t.Shards) <= 1 {
		return true
	}
	if len(t.Dist.KeyColumns) == 0 {
		return true
	}
	for _, key := range t.Dist.KeyColumns {
		found := false
		for _, g := range groupBy {
			ref, _, ok := stripColumn(g)
			if !ok || !strings.EqualFold(ref.Name, key) {
				continue
			}
			if ref.Table == "" || strings.EqualFold(ref.Table, t.Name) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func makeAggSpec(a *AggExpr, left, right *Table, mode AggMode) AggSpec {
	spec := AggSpec{Agg: a, Key: aggKey(a), Decomposable: true}
	if mode == AggSingleStage || mode == AggCoordinator {
		spec.Partial = PartialComplete
		spec.Final = FinalComplete
		if mode == AggCoordinator {
			spec.Note = "evaluated in one complete pass at coordinator"
		} else {
			spec.Note = "co-located complete aggregate"
		}
		return spec
	}
	if a.Distinct {
		spec.Decomposable = distinctArgCoversShardKey(a.Arg, left, right)
		if spec.Decomposable {
			switch a.Kind {
			case AggCount:
				spec.Partial = PartialCount
				spec.Final = FinalSumCounts
				spec.Note = "COUNT(DISTINCT shard_key): local COUNT(DISTINCT), final SUM"
			case AggSum:
				spec.Partial = PartialSum
				spec.Final = FinalSum
				spec.Note = "SUM(DISTINCT shard_key): local SUM(DISTINCT), final SUM"
			case AggAvg:
				spec.Partial = PartialSumCount
				spec.Final = FinalAvg
				spec.Note = "AVG(DISTINCT shard_key): local (SUM, COUNT), final SUM/COUNT"
			case AggMin:
				spec.Partial = PartialMin
				spec.Final = FinalMin
			case AggMax:
				spec.Partial = PartialMax
				spec.Final = FinalMax
			}
			return spec
		}
		spec.Partial = PartialDistinctSet
		switch a.Kind {
		case AggCount:
			spec.Final = FinalCountDistinct
			spec.Note = "partial emits exact distinct values; coordinator unions before COUNT"
		case AggSum:
			spec.Final = FinalSumDistinct
			spec.Note = "partial emits exact distinct values; coordinator unions before SUM"
		case AggAvg:
			spec.Final = FinalAvgDistinct
			spec.Note = "partial emits exact distinct values; coordinator unions before AVG"
		case AggMin:
			spec.Final = FinalMin
			spec.Note = "partial emits exact distinct values; coordinator unions before MIN"
		case AggMax:
			spec.Final = FinalMax
			spec.Note = "partial emits exact distinct values; coordinator unions before MAX"
		default:
			spec.Final = FinalComplete
		}
		spec.Note += " (argument does not include the full shard key; no early count/sum merge)"
		return spec
	}
	switch a.Kind {
	case AggCount:
		spec.Partial = PartialCount
		spec.Final = FinalSumCounts
		spec.Note = "COUNT -> partial COUNT, final SUM"
	case AggSum:
		spec.Partial = PartialSum
		spec.Final = FinalSum
		spec.Note = "SUM -> partial SUM, final SUM"
	case AggAvg:
		spec.Partial = PartialSumCount
		spec.Final = FinalAvg
		spec.Note = "AVG -> partial (SUM, COUNT), final SUM/SUM(COUNT)"
	case AggMin:
		spec.Partial = PartialMin
		spec.Final = FinalMin
		spec.Note = "MIN -> partial MIN, final MIN"
	case AggMax:
		spec.Partial = PartialMax
		spec.Final = FinalMax
		spec.Note = "MAX -> partial MAX, final MAX"
	}
	return spec
}

func distinctArgCoversShardKey(arg Expr, left, right *Table) bool {
	if arg == nil {
		return false
	}
	refs := ColumnRefs(arg)
	for _, t := range []*Table{left, right} {
		if t == nil || t.Dist.Kind == ReplicatedKind || len(t.Dist.KeyColumns) == 0 {
			continue
		}
		all := true
		for _, key := range t.Dist.KeyColumns {
			found := false
			for _, ref := range refs {
				if ref.Table != "" && !strings.EqualFold(ref.Table, t.Name) {
					continue
				}
				if strings.EqualFold(ref.Name, key) {
					found = true
					break
				}
			}
			if !found {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Top-K analysis

func buildTopKPlan(q *Query, plan *Plan) *TopKPlan {
	tk := &TopKPlan{
		ShardLimit:        -1,
		ShardOffset:       0,
		CoordinatorLimit:  q.Limit,
		CoordinatorOffset: q.Offset,
	}
	if q.Limit < 0 {
		tk.Reason = "no LIMIT"
		return tk
	}
	if containsVolatile(q.Where) {
		tk.Reason = "VOLATILE WHERE must be evaluated once at coordinator; shard limit unsafe"
		return tk
	}
	if plan.Filter.Coordinator != nil {
		tk.Reason = "coordinator still has filters; pushing LIMIT would discard rows before filtering"
		return tk
	}
	if plan.Join != nil && !plan.Join.Pushed {
		tk.Reason = "join is not co-located; shard-local top-K cannot be merged correctly"
		return tk
	}
	if plan.Aggregation != nil {
		switch plan.Aggregation.Mode {
		case AggNone:
		case AggSingleStage:
			if !plan.Aggregation.HavingPushdown {
				tk.Reason = "HAVING is not pushed; shard top-K could discard groups before HAVING"
				return tk
			}
		case AggPartialFinal, AggCoordinator:
			tk.Reason = "aggregation is not complete per shard; top-K must wait for final aggregation"
			return tk
		}
	}
	for _, o := range q.OrderBy {
		if containsVolatile(resolveAliasExpr(q, o.Expr)) {
			tk.Reason = "VOLATILE ORDER BY is not evaluable per shard consistently"
			return tk
		}
	}
	tk.Pushed = true
	tk.ShardLimit = q.Limit + q.Offset // corrected: never push OFFSET
	tk.ShardOffset = 0
	tk.Reason = fmt.Sprintf("push ORDER BY with LIMIT %d (L+O), OFFSET 0; coordinator applies OFFSET %d",
		tk.ShardLimit, q.Offset)
	return tk
}
