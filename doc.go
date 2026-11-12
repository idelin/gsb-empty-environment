// Package taskpkg implements the phase validation used by a Go aggregation host
// when devices drop out of a multi-party statistics run.
//
// Each surviving device submits a signed participation-set digest, a masked
// integer, and the share type of its seed (personal or pairwise). A completion
// set exists only when at least the configured threshold of distinct trusted
// devices sign the same canonical digest; two competing threshold views abort
// the phase as a split view.
//
// A RecoveryPlan is validated before any seed is handed to a Recoverer: only
// completed devices expose a personal seed, and only completed/dropped edges
// expose a pairwise seed. Pairwise seeds between two completed devices are
// rejected because those masks already cancel, and a personal seed outside the
// completion set is rejected as a double unmask. Pairwise scalars add on the
// smaller device ID and subtract on the larger one; Finalize reverses those
// contributions modulo the configured prime.
package taskpkg
