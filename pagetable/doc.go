// Package pagetable implements copy-on-write permission transactions over a
// four-level (x86-64 style) page table stored in a physical PageStore.
//
// A Snapshot is an immutable view rooted at one PML4 physical page. Walks
// (Lookup, LeafEntry, Mappings, Analyze) only read the store and are safe to
// run concurrently with edits and against roots published long ago.
//
// Service.Update runs one or more Patch values as a single transaction:
//
//   - Every table page that an edit touches is copied through the restricted
//     Allocator first; pre-existing pages are never overwritten, which keeps
//     old snapshots coherent and preserves shared (aliased) subtrees until an
//     entry on the exact edited path is replaced.
//   - Changing permissions inside a 2 MiB large page splits that leaf into a
//     freshly allocated PT holding 512 4 KiB PTEs. Untouched pages keep their
//     physical frames, raw flags and effective permissions. A whole-region
//     edit updates the large leaf directly without splitting.
//   - At commit, collapse re-merges a private PT back into a 2 MiB leaf when
//     all 512 PTEs are again uniform and contiguous (for example after the
//     last patch covering a region is undone).
//   - Any allocator or store error rolls the whole transaction back: every
//     frame the transaction still owns is returned and the previously
//     published root stays valid. The new root is otherwise published with a
//     single atomic word store.
//
// Only the RW, US and NX permission bits are managed; every other entry bit
// (accessed, dirty, global, PAT, PWT/PCD and the frame address) is preserved.
// Non-canonical virtual addresses and misaligned or empty ranges are rejected
// before any allocation.
package pagetable
