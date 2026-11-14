// Package netsec provides statistical disclosure controls for grouped
// aggregate queries.
//
// A statistics service can wrap its grouped summaries in a Query and execute
// it through an Engine. The engine enforces minimum group sizes, sensitive
// attribute suppression, per-subject and global differential privacy budgets,
// duplicate/cross-query restrictions, rounding, and calibrated noise.
package netsec
