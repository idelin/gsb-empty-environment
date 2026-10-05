# Adaptive Prefix Coding — Wire Format & Update Rules

This document specifies the bit-level format implemented by this package
(FGK-style adaptive Huffman over a byte alphabet). Encoder and decoder start
from the same initial state and apply identical, fully deterministic update
rules, so no dictionary ever needs to be transmitted.

## 1. Bit-stream conventions

* Bits are packed into bytes **most-significant-bit first**: the first bit of
  a stream is bit 7 of byte 0.
* Multi-bit integers (header fields, raw symbol bytes) are written
  big-endian / most significant bit first.
* Error positions are reported as `(byte, bit)` where `bit` is the offset
  inside the byte and `0` denotes the most significant bit. For frame-level
  errors, `byte` is relative to the start of the frame (header included);
  for raw symbol-level decoding, relative to the start of the bit stream.

## 2. Frame format

| Offset | Size | Field    | Contents                                   |
|-------:|-----:|----------|--------------------------------------------|
| 0      | 4    | magic    | ASCII `AHF1` (0x41484631)                  |
| 4      | 4    | version  | format/state version, uint32, currently 1  |
| 8      | 8    | length   | original length in bytes, uint64           |
| 16     | …    | payload  | concatenated symbol codes                  |

Version 1 pins the initial state (a single NYT node) and the default rescale
threshold (`DefaultMaxWeight` = 2^32).

**Padding rule (fixed):** after the code of the last symbol, the final
partial byte is padded with **zero bits** to the next byte boundary. A frame
contains no further bytes after that. A decoder must reject non-zero padding
bits and any trailing bytes.

## 3. Code construction

* The code of a leaf is the path from the root: **left edge = 0, right
  edge = 1**, written from the root downward.
* A byte that has no code yet is transmitted as an **escape**: the code of
  the NYT ("not yet transmitted") leaf, followed by the byte's raw 8 bits
  (MSB first). In the initial state the tree is a single NYT node, so the
  very first escape code is the empty bit string.
* The NYT leaf is never removed, even after all 256 symbols are known.

## 4. Node numbering and ordering

* Every node receives a **stable id** at creation: 0, 1, 2, … Ids never
  change afterwards. When the NYT node spawns (see below), the new internal
  node **inherits the retired NYT node's id**, so ids remain dense
  (0..N−1); the two new leaves take the next two ids.
* The sibling-property ordering is maintained explicitly as an **order
  list**; `order[r]` is the node with rank `r`. Rank 0 is always the NYT
  node, the root always has the highest rank. Swapping two nodes exchanges
  their ranks, never their ids.
* All update decisions are derived only from the tree structure and this
  explicit order list. No map iteration or platform-dependent ordering is
  involved, so encoder and decoder cannot diverge through differing
  iteration order.

## 5. Update rules (applied identically by both sides after each symbol)

1. **Rescale (overflow) check.** If `root.weight >= maxWeight` (default
   2^32), rescale *before* updating: process the order list from rank 0
   upward (children are always ranked below their parents); each leaf's
   weight becomes `ceil(w/2)`, each internal node's weight becomes the sum
   of its (already rescaled) children. The NYT leaf stays at weight 0.
2. **Spawn (first occurrence of a byte).** The NYT leaf is replaced by a new
   internal node `I` whose left child is a fresh NYT leaf and whose right
   child is a fresh leaf `L` for the symbol. `L` and `I` start with weight
   1. In the order list, the triple `[newNYT, L, I]` takes the old NYT
   node's position, in that sequence. The increment loop (step 3) then
   continues from `I`'s parent.
   Otherwise the loop starts at the symbol's leaf.
3. **Increment loop.** Repeat until the current node is nil (above root):
   * find the **block leader**: the highest-rank node with the same weight
     as the current node (a plain scan from the top of the order list);
   * if the leader is neither the current node nor **its parent**, swap the
     two nodes (exchange tree positions and ranks). Only the parent can ever
     be an equal-weight ancestor, because the NYT leaf is the unique
     zero-weight node;
   * increment the current node's weight and continue from its parent.

## 6. Escape validity rules (decoder)

An escape is **invalid** — and reported as `ErrKindInvalidEscape` at the bit
position where the escape code started — when:

* the NYT code is decoded after all 256 symbols are already known, or
* the raw 8-bit byte following an escape already has a codeword.

## 7. Frame termination and error reporting

After decoding `length` symbols the decoder verifies:

* the remaining bits of the current byte (if any) are all zero — otherwise
  `ErrKindNonZeroPadding` at the exact bit offset of the first non-zero
  padding bit;
* no further bytes remain — otherwise `ErrKindTrailingData` at the byte
  offset of the first extra byte.

Other reported errors, all as `*adaptivehuffman.Error` with byte/bit
offsets:

| Kind                    | Meaning                                             | Reported position                |
|-------------------------|-----------------------------------------------------|----------------------------------|
| `ErrKindBadMagic`       | wrong magic bytes                                   | byte 0, bit 0                    |
| `ErrKindBadVersion`     | unsupported version                                 | byte 4, bit 0                    |
| `ErrKindTruncated`      | stream ended mid-header or mid-codeword             | start of the incomplete codeword (byte 0 for header) |
| `ErrKindInvalidEscape`  | see §6                                              | start of the escape code         |
| `ErrKindNonZeroPadding` | non-zero bit in the zero padding                    | exact bit                        |
| `ErrKindTrailingData`   | extra bytes after the frame                         | first extra byte, bit 0          |
| `ErrKindCorruptState`   | invalid exported-state blob                         | offset of the offending field    |

## 8. Exported state format

`ExportState` captures the complete adaptive state; restoring it and
continuing at any symbol boundary yields a bit stream identical to an
uninterrupted run. All integers big-endian:

| Field     | Type    | Contents                                             |
|-----------|---------|------------------------------------------------------|
| magic     | uint32  | `AHST` (0x41485354)                                  |
| version   | uint32  | currently 1                                          |
| maxWeight | uint64  | rescale threshold                                    |
| nodeCount | uint32  | number of nodes N (ids are dense 0..N−1)             |
| nodes     | N × 24B | per node, in increasing id order: `symbol` int32 (−2 internal, −1 NYT, 0..255 leaf), `weight` uint64, `parent`/`left`/`right` int32 (node id, −1 if none) |
| order     | N × 4B  | node ids sorted by rank, rank 0 first                |

Import validates the magic, version, sizes, id ranges, the permutation
property of the order list, uniqueness of the root and NYT node, and the
consistency of all parent/child links.
