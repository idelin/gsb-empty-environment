// Package joinenum implements conflict-aware bushy join enumeration for
// queries containing inner, left-outer, semi and anti joins.
//
// The optimizer follows the core-search-space model of Moerkotte and Neumann
// (CD-C / total eligibility sets / conflict rules):
//
//   - Every operator gets a minimal Total Eligibility Set (TES): the base
//     relations whose values are observed by the predicate.  Additional
//     side constraints require every right-hand predicate relation to be
//     exposed by the right input of left-outer, semi and anti joins and every
//     left-hand predicate relation to stay on the preserved side.
//   - Every ancestor/descendant operator pair for which a child swap or an
//     L-ASSCOM / R-ASSCOM rotation is not semantics preserving yields a
//     conflict rule T1 -> T2.  The rule forbids the operator originally
//     rooted at T2 from being placed in the left (preserved) or right
//     (null-supplying/existential) input of the operator rooted at T1.
//   - Bushy plans are enumerated with a connected-subset (DPhyp-style) dynamic
//     program.  A split (S1,S2) is accepted only if the chosen operator's TES,
//     side conditions and all applicable conflict rules hold.
//
// The package uses exact integer cardinality/cost arithmetic.  Plans are
// compared first by total cost, then by the lexicographically smallest
// canonical pre-order encoding.
package joinenum
