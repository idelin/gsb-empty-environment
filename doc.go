// Package outerjoin implements strong null-rejection analysis, top-down
// outer-join degradation, Galindo-Legaria/Rosenthal rewrite gates, and
// multiset (BAG) verification for logical join trees.
//
// The predicate evaluator uses SQL Kleene three-valued logic: comparisons
// involving NULL are UNKNOWN, NOT UNKNOWN is UNKNOWN, AND uses FALSE as its
// absorbing value, and OR uses TRUE as its absorbing value.
//
// IsNullRejecting forces every attribute of the requested target relations to
// NULL, enumerates representative valuations of all referenced non-target
// attributes, and reports whether the predicate can ever evaluate to TRUE.
// For integer schemas the representative domain is order-complete for the
// supported comparison/IN/COALESCE/CASE fragment.
//
// DegradeQuery uses only top-level WHERE/filter null rejection to change join
// types. An ON predicate is never used to degrade the outer join that owns it;
// it only controls matching and formal associativity conditions.
//
// EnumerateLegalPlans generates commutativity and certified associativity /
// promotion forms. VerifyLegalPlans evaluates every generated form with the
// built-in bag-semantic join evaluator and compares multisets on the supplied
// Database.
package outerjoin
