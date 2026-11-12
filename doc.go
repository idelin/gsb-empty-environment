// Package netsec aggregates per-chunk detection verdicts for a large object
// scanned by a security gateway.
//
// A gateway dispatches the fixed chunks described by a Manifest to one or more
// detectors. A single chunk passing does not imply the whole object passes: the
// object is only published when every chunk has completed every check required
// by the manifest's current generation, with no positive verdict. A missing
// chunk, a timeout, a failed check or an explicit cancellation yields an
// Incomplete decision instead.
//
// Each Manifest carries a monotonically increasing Generation. Receipts are
// bound to the generation they were produced for, so results that arrive after
// a fragment replacement (a newer manifest) are stale and ignored.
package netsec
