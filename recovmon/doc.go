// Package recovmon watches a fault-tolerant protocol parser and proves
// whether its error recovery ever crosses into authenticated content.
//
// The test host supplies the original raw byte interval, the set of
// intervals covered by authentication (MAC/signature/AAD), and the
// parser's field and recovery events. The Monitor enforces three rules:
//
//   - recovery may only resume at the start boundary of an unconsumed
//     field and only move forward;
//   - a discarded range may never contain consumed or already discarded
//     bytes;
//   - no byte may be both discarded and authenticated, so coverage that
//     must be verified can never be skipped.
//
// The first illegal event is captured as structured Violation evidence,
// including the active nested-field stack, partially truncated fields and
// the exact authenticated intervals that the recovery would have crossed.
package recovmon
