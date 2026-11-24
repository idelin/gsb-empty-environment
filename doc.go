// Package viewopt plans and verifies rewrites of grouped SQL queries over
// overlapping materialized views.
//
// # Model
//
// A base row has a three-dimensional nullable integer group key, an integer
// filter coordinate, and a nullable integer measure.  A View is the aggregate
//
//	SELECT dims, SUM(measure), COUNT(*), COUNT(measure)
//	FROM base WHERE filter BETWEEN lo AND hi GROUP BY dims
//
// A Query is the corresponding direct GROUP BY over the same base table, with
// an optional HAVING predicate.  A Plan sums view rows with integer
// coefficients, projects them onto the query grouping, and applies the same
// HAVING predicate.  AVG is not a plan term; it is derived as SUM/COUNT
// (non-null) only after the linear components have been combined.
//
// Equality is required for every underlying multiset in the configured filter
// domain.  It includes group presence, COUNT, COUNT(non-null), SUM and
// therefore HAVING and projected result rows.  A group with no rows is not
// the same as a group whose SUM is zero: SUM is NULL whenever COUNT(non-null)
// is zero.  NULL grouping keys form their own groups.
//
// Equivalence checking is symbolic.  Interval endpoints partition the filter
// line into cells on which every view's membership is constant.  The checker
// only needs to compare coefficient sums with the query indicator on those
// cells; it never enumerates individual values, even for ranges near 2^40.
//
// # Counterexamples
//
// If a plan is not equivalent, VerifyPlan returns a table that distinguishes
// it from the direct query.  Candidate tables are considered by increasing
// row count and then by lexicographic tuple order, subject to
// Options.MaxMultiplicity.  ExhaustiveVerify provides an independent
// small-domain enumeration for tests and for cross-checking the symbolic
// checker.
package viewopt
