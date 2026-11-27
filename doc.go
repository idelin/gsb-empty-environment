// Package ftl implements a page-level flash translation layer for storage
// simulation and block-device firmware models.
//
// The package separates three persistent concerns:
//
//   - Flash holds physical pages and enforces sequential programming, erase
//     granularity, and erase counters.
//   - Page OOB metadata carries LBA, monotonic seq, length, and a CRC32C so a
//     torn page can never be mistaken for valid data.
//   - MetaArea holds the CRC-protected TRIM journal and two alternating
//     complete-checkpoint slots.
//
// A Device adds an in-memory L2P map, per-block valid bitmaps, and GC.  After
// PowerLoss, Recover loads the newest complete checkpoint, scans every
// physical page's OOB, merges all newer data and TRIM records in seq order,
// discards checksum-corrupt pages, and rebuilds the exact pre-crash state.
//
// TRIM tombstones are journaled before the mapping is released.  During GC,
// every live page is copied with a higher seq before the source block is
// erased, so a power loss at any point leaves either the source or a newer
// destination copy recoverable.
package ftl
