// Package submigrate is an importable migration acceptance kit for merging
// several host subscription partitions into one new stream.
//
// # Trust model
//
// The event host supplies three signed things per partition:
//   - a partition root committing both the full physical log and the public
//     visible prefix, without revealing hidden event count or positions;
//   - the stable, signed identity of every event and each visible occurrence,
//     bound to a partition and a visibility-only sequence number;
//   - the partial order of occurrences in the merged stream.
//
// The old reader contributes host-signed receipts. A receipt records an
// AuthorizedPrefix (the visible events the host ever made visible to this
// reader) and a smaller ConfirmedPrefix (the contiguous prefix the reader had
// acknowledged). Neither value is a physical slot index.
//
// # Migration rules
//
// The service walks the merged stream and admits an occurrence only when its
// identity and occurrence signatures verify, its visible chain is contiguous
// with the signed root, and its visibility sequence stays inside an old
// receipt's AuthorizedPrefix. Deduplication is performed strictly on verified
// EventID: equal summaries with distinct identities stay distinct, and an
// identity cannot be reused with different content.
//
// Incomplete old receipts yield exactly one of three verdicts:
//   - StatusResumable: everything checks out, the result is deterministic and
//     safe to replay;
//   - StatusResync: an artifact is missing, malformed, out of scope, or not
//     provable, so the reader must re-synchronize;
//   - StatusRevoked: a host-signed Revocation explicitly withdraws the grant.
//
// Hidden events only seal the root's log commitment. They never appear in
// notifications, diffs or cursors, and their count or positions cannot change
// any visible output. The cursor embeds its full evidence and receipts and is
// independently verifiable with public keys alone via CursorVerifier.
package submigrate
