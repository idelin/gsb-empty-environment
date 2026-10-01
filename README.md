# crset — adaptive compressed uint32 set (roaring-style, stdlib only)

`crset` stores a sorted set of `uint32`. Values are split by their **high 16
bits** into buckets kept in a sorted slice (binary search, no map). Each
bucket's low 16 bits live in a container that physically switches between
three encodings, per bucket, based on live cardinality and run structure.

## Encoded layout (what Stats counts, byte for byte)

```
Set
└── directory: []bucket sorted by high key      2 B per bucket (uint16 high)
    └── container                               3 B header (1 B tag + 2 B card)
        ├── sparse:  sorted []uint16            2 B per element
        ├── bitmap:  [8192]uint64               65536 B flat
        └── run:     [](start,len uint16)       4 B per run
```

## Conversion policy (all thresholds configurable via `Config`)

| transition            | condition                                             |
|-----------------------|-------------------------------------------------------|
| sparse → bitmap       | `card > SparseToBitmapCard` (default 4096)            |
| bitmap → sparse       | `card < BitmapToSparseCard` (default 3584)            |
| any → run             | `runBytes*RunSavingsFactor <= currentBytes` (default factor 2, 0 disables RLE) |
| run → sparse/bitmap   | `runBytes > altBytes(card)`                           |

`altBytes` compares against the bitmap cost while `card > BitmapToSparseCard`
and the sparse cost below it. Enter/exit thresholds always differ by a factor
(or a 512-wide cardinality band), and RLE entry additionally requires
`card >= 16`, so a single add/remove can never cross both edges of a band:
**hovering at any threshold causes zero conversions** (proven by
`TestSparseBitmapHysteresis`, `TestRunLifecycle`, and demo section 3).

The bitmap→run scan is amortized (every 64 mutations); `Set.Optimize()`
forces an immediate RLE check on every bucket.

## Set operations

`Union`, `Intersect`, `Difference`, `SymDifference` work directly across
mixed encodings: containers combine through iterators + per-encoding
`contains`, results are re-normalized into the cheapest encoding under the
receiver's config. Verified element-for-element against `map[uint32]bool`
references in `ops_test.go`.

## Guarantees tested (`go test ./...`)

- conversion round-trips preserve elements exactly (`TestConversionsPreserveElements`)
- no flip-flop when deleting to/around thresholds (`TestSparseBitmapHysteresis`, `TestRunLifecycle`)
- full lifecycle run→bitmap→run→sparse with exact conversion counts (`TestRunToBitmapLifecycle`)
- bucket boundary ownership: 65535 vs 65536, 0 and 0xFFFFFFFF (`TestBucketBoundary`)
- exact byte accounting per encoding (`TestByteStats`)
- 150k-op fuzz vs `map[uint32]bool`, incl. stats invariants (`TestFuzzAgainstMap`)
- cross-representation set ops vs map references (`TestSetOpsCrossRepresentation`, `TestOpsFuzz`)

## Run

```sh
go test ./...
go run ./cmd/demo
```

### Sample demo output (go1.24.2 darwin/arm64)

```
== 1. Each high-16-bit bucket picks its cheapest encoding ==
ranges (200x1000 contig)    200000 elems    16 buckets         892 B  (naive []uint32:     800000 B, 896.9x)
dense (evens in [0,4M))    2097152 elems    64 buckets     4194624 B  (naive []uint32:    8388608 B, 2.0x)
sparse (5k random in 1e8)      5000 elems  1471 buckets       17355 B  (naive []uint32:      20000 B, 1.2x)

== 2. Set operations across mixed encodings (verified vs map) ==
intersect=100000 union=2197152 difference=100000 in 19ms, map-verified: true

== 3. Hysteresis: hovering at the bitmap threshold does not flip-flop ==
20000 remove/add pairs at card 4096/4097: conversions 1 -> 1 (0 extra), rep stays bitmap: true

== 4. Thresholds are configurable ==
stride-3, default cfg         2000 elems     1 buckets        4005 B   (sparse)
stride-3, bitmap@1024         2000 elems     1 buckets       65541 B   (bitmap)
contiguous, default           2000 elems     1 buckets           9 B   (run)
contiguous, RLE off           2000 elems     1 buckets        4005 B   (sparse)
```
