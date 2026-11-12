// Package netsec verifies segmented audit hash chains for archive checking.
//
// Audit storage is divided into time windows. Each window carries a durable
// header (window number, start sequence, entry count, previous window head
// hash) and a sequence of chained entries. Verification enforces adjacent
// window numbers, continuous sequence numbers, accurate counts, intact
// back-links, and equality with externally published immutable anchors.
//
// Call VerifyChain for fail-fast validation or CheckChain for a full report.
// DetectForks reports window numbers observed with conflicting heads.
package netsec
