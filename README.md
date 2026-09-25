# tile-dedup-store

Content-addressed, tile-based RGBA image storage with copy-on-write layers,
snapshots, read leases and generational garbage collection. Pure TypeScript on
Node.js (>= 22.6, type stripping) — no Canvas, no image libraries, no
dependencies. Pixel tiles are built and compared by hand.

## Design

- **Tiles as immutable blocks.** An image is a grid of fixed-size tiles. Each
  tile's RGBA bytes are hashed (SHA-256, pluggable) and stored once under
  `blocks/<id>.rgba`. Identical tiles — including all-transparent blank tiles —
  share a single block across positions, layers and snapshots.
- **Collision buckets.** A digest only selects a bucket. Insertion compares the
  full pixel bytes (and dimensions) against every entry in the bucket; equal
  content reuses the block, different content gets a suffixed id (`<digest>#n`).
- **Copy-on-write.** Layers and snapshots are roots: immutable arrays of block
  ids. Editing a tile writes a new block and publishes a new root array; the
  old root keeps referencing the old blocks, so snapshots and sibling layers
  are never affected.
- **Persistent manifest + reference ledger.** `manifest.json` holds blocks,
  roots, leases, the refcount ledger and the GC generation. Every commit
  (edit, snapshot, delete, lease acquire/release) updates roots and the ledger
  in one decision and persists atomically (write tmp + rename). Block files
  are written before any root can reference them; a commit referencing an
  unknown block is rejected, so a crash can never leave a live snapshot
  pointing at a deleted block.
- **Read leases.** A lease pins a root's tile list in the manifest. Leased
  blocks survive snapshot deletion and GC until the lease is released.
- **Generational GC with independent verification.** Each `gc()` closes a
  generation. A block is reclaimable only if it is older than the current
  generation window, has a zero ledger refcount, **and** is confirmed
  unreachable by an independent traversal of all live roots and leases — the
  volatile refcount alone is never trusted. Manifest is persisted before block
  files are unlinked; orphan files (crash leftovers) are swept on the next GC.

## Layout

- `src/image.ts` — hand-rolled RGBA image/tile helpers (create, set/get pixel, extract, blit, compare)
- `src/store.ts` — block store: manifest, ledger, collision buckets, leases, generational GC
- `src/document.ts` — layers, copy-on-write edits, snapshots, rendering
- `test/` — `node:test` suites (dedup, COW, snapshots, collisions, GC, leases, interleavings)
- `examples/demo.ts` — runnable end-to-end sample

## Run

```sh
node --test test/*.test.ts   # tests (Node >= 22.6; developed on v23.9.0)
node examples/demo.ts        # demo
```

## Demo output (sample)

```
store dir: /var/folders/.../tilestore-demo-YzMSkv
two blank layers (blank block shared)          blocks=1 roots=2 leases=0 gen=0
background painted                             blocks=3 roots=2 leases=0 gen=0
snapshot snap-v1 taken                         blocks=3 roots=3 leases=0 gen=0
background edited after snapshot (COW)         blocks=4 roots=3 leases=0 gen=0
  snap-v1 pixel(0,0):  [0,0,128,255]
  background pixel(0,0): [0,200,200,255]
foreground edited independently                blocks=5 roots=3 leases=0 gen=0
snapshot deleted, 3 GC cycles, lease held      blocks=5 roots=2 leases=1 gen=3
lease released, 2 more GC cycles               blocks=4 roots=2 leases=0 gen=5
invariant problems: []
after reopen: roots=[background, foreground] blocks=4
reopened background pixel(0,0): [0,200,200,255]
reopened foreground pixel(8,0): [255,128,0,255]
```

## Test results

```
✔ digest collision bucket stores both blocks and compares full pixels
✔ collision bucket survives manifest reload
✔ blank tiles across positions and layers share a single block
✔ identical tile content dedups to the same block
✔ copy-on-write: editing one layer never affects a layer sharing its blocks
✔ pixel-level edit round-trips through render
✔ generational GC: young unreferenced blocks survive their first cycles
✔ GC re-verifies with live-root traversal, not just the refcount ledger
✔ read lease pins blocks of a snapshot even after the snapshot is deleted
✔ orphan block files not in the manifest are swept
✔ interleavings of block write, root publish, lease release and reclamation
✔ publishing a root referencing an unknown block is rejected atomically
✔ lease released before GC never exposes a leased-then-deleted snapshot
✔ snapshot holds old blocks after later edits (copy-on-write)
✔ deleting a snapshot reclaims only blocks no one else references
✔ manifest persists: reopening the store keeps roots, blocks and ledger
ℹ tests 16
ℹ pass 16
ℹ fail 0
```
