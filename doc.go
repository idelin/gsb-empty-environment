// Package netsec implements an importable, decoder-side state machine that
// enforces the HTTP/2 MAX_HEADER_LIST_SIZE setting (setting identifier 0x6).
//
// The machine is transport agnostic. A protocol gateway feeds it the setting
// updates and header-bearing frames it observes, either through the typed
// methods on Connection or by dispatching Event values through
// Connection.HandleEvent, and the machine reports which streams must fail.
//
// Enforcement semantics:
//
//   - A header block is checked against the limit that is current when the
//     block begins (on the HEADERS frame). That limit is snapshotted for the
//     whole block, so shrinking MAX_HEADER_LIST_SIZE while a block is in
//     progress never invalidates bytes already accepted.
//   - A setting update only constrains header blocks that begin after the
//     update.
//   - A single header block whose accumulated size exceeds its snapshotted
//     limit fails that stream with ENHANCE_YOUR_CALM.
//   - A limit of zero means the size is unbounded, per RFC 7540.
package netsec
