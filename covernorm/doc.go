// Package covernorm normalizes raw fuzzing coverage samples across module
// reloads and address-space layout randomization.
//
// A fuzzing host observes control-flow edges as raw jump addresses. When a
// module is reloaded, the same code can land at a different base address;
// worse, a new module can be mapped at an address previously occupied by an
// unrelated module. Attributing samples by raw address alone then folds
// distinct edges together and mis-attributes post-reload coverage to the old
// image.
//
// The normalizer removes that ambiguity with three rules:
//
//   - Load events declare a module content digest (content identity), its
//     address segments with module-relative offsets, and a generation number.
//     A load only becomes usable at its confirmation sequence; samples that
//     arrive earlier are classified as ReasonBeforeLoad instead of being
//     projected onto a mapping that did not yet exist.
//   - Each edge endpoint is bound to (content digest, module-relative offset);
//     the load generation is retained on the archived record for provenance.
//     The resulting EdgeID contains no raw host address, so the same jump
//     observed under two ASLR layouts merges correctly while the same raw
//     address belonging to two different digests never merges.
//   - Samples that cannot be attributed deterministically are never
//     force-mapped. Overlapping active mappings become ReasonAmbiguous,
//     unmapped addresses become ReasonNoMapping, and early samples become
//     ReasonBeforeLoad; they land in Archive.Unknown, separate from the edge
//     archive.
//
// Events may be ingested out of order and are re-sorted by sequence number on
// Flush. The edge archive is append-only: an unload retires the active
// mapping for future samples but never rewrites or deletes archived edges or
// earlier timeline entries. Archive snapshots returned by Flush are
// independent copies and remain stable across later flushes.
package covernorm
