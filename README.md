# bitmapidx — compressed bitmap with rank1/select1

A succinct, compressed bitmap index in pure Go (standard library only).

## Encoding layout

A bitmap of `bitLen` bits is split into 512-bit blocks. Each block is
stored in one of four real encodings (directory records type, popcount,
and data offset — 8 B per block):

| type   | payload                        | bytes        |
|--------|--------------------------------|--------------|
| zero   | —                              | 0            |
| one    | —                              | 0            |
| sparse | u8 count + count × u16 offsets | 1 + 2·count  |
| raw    | 8 × u64 words                  | 64           |

Hierarchical sampling on top of the blocks:

- **rank**: one u64 cumulative-ones entry per 32 blocks (superblock),
  plus the per-block popcounts in the directory.
- **select**: one sample per 64 one-bits → (block, rank base), 16 B.

## Serialization

Fixed 116-byte header: magic `RSB1`, **version**, **bitLen**, block /
superblock / **sample counts**, sampling rates, total ones, section
offsets and lengths, per-section CRC32, header CRC32. Sections follow
in order: directory, rank superblock index, select sample index, block
data. `Map` **independently recomputes** every offset from the counts,
verifies all checksums, and re-derives both indexes from the directory
before accepting a file — stored offsets are never trusted.

## Queries (unified boundaries)

- `Rank1(i)` — ones in `[0, min(i, bitLen))`; `Rank1(bitLen) == TotalOnes()`.
- `Select1(k)` — position of the k-th one (0-based); `ok=false` when
  `k >= TotalOnes()` (an all-zero bitmap has no select result).
- Tail padding bits beyond `bitLen` are masked at query time and never
  participate.

Queries run on a read-only mapping and are metered by `Metrics`
(bytes read, blocks decoded): rank/select touch O(1) blocks and never
decompress the whole bitmap.

## Run

```
go test ./bitvec/ -v   # exhaustive + mutation + boundary tests
go run ./cmd/demo      # layout + sample queries with cost metrics
```
