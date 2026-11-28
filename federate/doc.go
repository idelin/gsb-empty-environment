// Package federate implements a federated query planner, a multi-shard
// simulation executor and a full-pull reference evaluator.
//
// The planner makes pushdown decisions explicit: corrected Top-K LIMIT/OFFSET,
// single-stage versus partial/final aggregation, HAVING placement, co-located
// join eligibility, COLLATE safety and predicate volatility. ExecutePlan runs
// the decision on a simulated cluster. Reference pulls all rows to one machine
// and evaluates the same logical query. Verify compares both results row by row.
package federate
