// Package netsec correlates cloud-audit credential events whose reports can
// arrive out of order.
//
// Each Event carries a trusted commit sequence (Feed/Seq) and an inclusive
// possible-occurrence interval (Lo/Hi) in an integer time domain. Arrival
// order is intentionally ignored: events sharing a feed are constrained to
// strict sequence order instead.
//
// Correlate works per credential in three stages:
//
//   - Narrowing: the sequence constraints form a DAG of difference
//     constraints t_pred + 1 <= t_succ. One forward and one backward
//     topological pass tightens every interval to its exact feasible range;
//     a collapsed interval is retained as an inconsistent-window conflict.
//   - Classification: every use/revoke pair is judged as necessarily-before,
//     necessarily-after, possibly-straddling the revocation boundary, or
//     unknown. Comparable events follow the trusted path; incomparable ones
//     are decided from the narrowed intervals, including same-time
//     feasibility.
//   - Enumeration cross-check: for small time domains the package exhaustively
//     enumerates every feasible assignment and independently re-derives each
//     verdict. Any disagreement is surfaced as a conflict rather than hidden.
//
// Conflicting events and ambiguous pairs are always kept in the Report;
// nothing is dropped or resolved by arbitrarily picking one record.
package netsec
