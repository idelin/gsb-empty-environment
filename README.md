# canon — canonical serialization for trees and rooted graphs (Go, stdlib only)

`canon` produces deterministic byte encodings suitable for hashing and
signing. It deliberately does **not** implement any signature algorithm;
feed the canonical bytes (or the `Digest` helper's SHA-256 output) to the
signature scheme of your choice.

## Supported value range

| Type    | Encoding                                                        |
|---------|-----------------------------------------------------------------|
| Null    | tag only                                                        |
| Bool    | tag only                                                        |
| Int     | tag + zigzag varint (minimal form), int64 range                 |
| Float   | tag + 8-byte big-endian IEEE-754; NaN/±Inf **rejected**, -0 → +0 |
| String  | tag + uvarint length + bytes; must be valid UTF-8               |
| Bytes   | tag + uvarint length + bytes (distinct type from String)        |
| Array   | tag + uvarint count + elements; **order is significant**        |
| Object  | tag + uvarint count + entries; keys sorted bytewise, unique     |
| Graph   | tag + node count + nodes + root index; see below                |

Anything outside this set (channels, functions, arbitrary Go structs,
non-finite floats, invalid UTF-8 strings) is rejected. Distinct types
never share an encoding: `Int(1) != String("1")`.

## Canonical graph encoding

`EncodeGraph(root, budget)` canonically serializes the graph reachable
from `root`, including shared references and cycles. Two graphs encode
identically **iff** they are isomorphic as labeled, edge-ordered, rooted
graphs — node creation order and traversal order are irrelevant, and
discovery order is never used as the node numbering.

Algorithm:

1. **Partition refinement** (1-WL style): initial colors from canonical
   label encodings; refine by (own color, ordered successor colors)
   signatures, re-ranked in sorted order, until stable.
2. **Bounded individualization-refinement search**: if a non-singleton
   cell remains, individualize each member of the canonically-chosen
   target cell in turn, recurse, and keep the lexicographically smallest
   certificate. Trying every member of the cell makes the result
   independent of visitation order.
3. **Budget**: `budget` bounds the number of refinement rounds
   (`budget <= 0` selects `DefaultBudget`). If it is exhausted,
   `ErrBudgetExceeded` is returned and **no encoding is produced** — the
   result is explicitly incomplete, never silently wrong.

The wire form is `tag | nodeCount | (label, outDegree, targets...)* |
rootIndex`; node indices are the canonical color order. Only the
reachable subgraph is encoded. Distinct nodes are never merged: two
same-label nodes stay two nodes, and a shared node decodes back to one
shared pointer.

## Decoding safety

Decoding never executes host code — the format is pure data, with no
type names, constructors, or callbacks. Every length/count prefix is
validated against the remaining input **before** any allocation, nesting
depth is capped (`MaxDepth`), non-minimal varints, unsorted/duplicate
object keys, `-0.0`, and non-finite floats are rejected as
non-canonical.

## Usage

```go
obj, _ := canon.NewObject(canon.KV{Key: "id", Value: canon.Int(1)})
enc, _ := canon.Encode(obj)        // canonical bytes
digest, _ := canon.Digest(obj)     // sha-256 of the canonical bytes
v, err := canon.Decode(enc)        // strict, allocation-safe decoding

genc, err := canon.EncodeGraph(root, 0)          // default budget
if errors.Is(err, canon.ErrBudgetExceeded) { /* incomplete */ }
g, err := canon.DecodeGraph(genc)                // restores sharing + cycles
```

## Demo and tests

```
go run ./cmd/canon-demo   # runnable sample
go test ./...             # full test suite
```

Tests include a reference check that runs **all n! permutations** of node
creation order over a battery of small graphs (shared nodes, cycles,
self-loops, twin leaves, a symmetry-forcing uniform cycle) and requires
byte-identical encodings, plus negative checks that array/edge order and
distinct node identities are never collapsed.
