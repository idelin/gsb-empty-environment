// Package grouping executes SQL:2011 multi-dimensional grouping:
// ROLLUP, CUBE, GROUPING SETS and composite column tuples may be nested
// and chained freely inside a single GROUP BY clause, combined with
// GROUP BY ALL (default) or GROUP BY DISTINCT, the GROUPING(c1, ..., ck)
// bitmap function, and per-aggregate DISTINCT and FILTER (WHERE cond)
// modifiers.
//
// Semantics implemented:
//
//   - A composite tuple (b, c) is an atomic grouping item:
//     ROLLUP(a, (b, c)) expands to [(a b c), (a), ()], never 4 sets.
//   - Comma-separated grouping elements combine by cartesian product;
//     inside each resulting set duplicate column names are removed,
//     keeping canonical (first-appearance) column order.
//   - GROUP BY ALL (default) keeps duplicate grouping sets produced by
//     the expansion and emits one output row per duplicate; GROUP BY
//     DISTINCT deduplicates identical column sets.
//   - GROUPING(c1, ..., ck) returns an unsigned bitmap, most
//     significant bit first: bit i is 1 iff column ci is absent from
//     the current grouping set (synthesized NULL), 0 iff ci takes part
//     in grouping, even when the real key value itself is NULL.
//   - On an empty input table the empty grouping set () still produces
//     exactly one row (COUNT=0, SUM/AVG/MIN/MAX=NULL, GROUPING all
//     ones), while any non-empty grouping set produces zero rows.
//   - COUNT(DISTINCT col) and the other aggregates strictly ignore
//     NULL inputs; each aggregate evaluates its own FILTER predicate.
package grouping
