// Package fieldbinding implements the writer-side column mapping for a data
// lake whose update batches arrive with independently evolving nested
// schemas.
//
// Fields are bound exclusively by stable field IDs, never by name, so
// renames are free. A batch schema is validated against the table schema
// once, up front, producing a Mapping that allows:
//
//   - renames (same ID, different name),
//   - moving nullable subtrees (every ancestor on both paths, plus the
//     moved root, must be nullable),
//   - lossless numeric widening (int32 -> int64/float64, float32 -> float64).
//
// Values are tri-state: Missing (absent from the record) is kept strictly
// distinct from Null (an explicit null) and the two are never merged.
//
// Batches that span schema versions are validated atomically by
// Table.Commit: the whole batch is planned and every row is checked before
// a new Snapshot is published, so concurrent readers keep seeing the old
// snapshot until the swap completes.
package fieldbinding
