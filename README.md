# utf8-rope-editor

A TypeScript UTF-8 text editing library built on a **persistent rope**, with
**undo** and **branching history**. All edit offsets are counted in Unicode
scalar values; multi-byte encodings are never split.

## Design

- **Scalar offsets** — `insert`/`delete`/`getText` offsets and lengths count
  Unicode scalar values (code points). A 4-byte emoji is exactly one scalar;
  rope leaves are only ever split at scalar boundaries, so a surrogate pair
  or UTF-8 sequence can never be cut.
- **Dual node metrics** — every rope node maintains both `bytes` (UTF-8
  length) and `scalars` (scalar count); `byteLength`/`scalarLength` are O(1).
- **Persistent rope** — nodes are immutable; edits path-copy via
  split/join (AVL-balanced, single/double rotations). Old snapshots share
  every unchanged subtree with the current state: a small edit on a large
  document allocates O(log n) nodes.
- **Strict encoding validation** — input may be a `string` (rejects lone
  surrogates) or a `Uint8Array` of UTF-8 bytes (rejects overlong forms,
  surrogate code points, values > U+10FFFF, truncated sequences). Invalid
  input throws `InvalidEncodingError` and the **whole edit is rejected**
  before any state changes.
- **Branching history** — every committed edit creates an entry in a
  history tree. `undo()`/`redo()` walk it; `checkout(id)` jumps to any
  snapshot, so editing after an undo naturally forms a branch.
- **Deterministic reclamation** — nodes are reference-counted in a
  `NodePool`. `releaseSnapshot(id)` (leaf snapshots only) frees exactly the
  nodes no longer reachable from any live snapshot. `liveNodes` always
  equals the reachable-node count; the tests assert this after every
  operation.

## API

```ts
const ed = new TextEditor("初始😀");      // string | Uint8Array
ed.insert(2, "文本");                     // scalar offset
ed.delete(0, 1);                          // scalar offset + length
ed.getText();                             // full text (or scalar slice)
ed.getBytes();                            // UTF-8 bytes
ed.scalarLength; ed.byteLength;           // O(1) aggregates

const v1 = ed.snapshot("v1");             // label current state
ed.undo(); ed.redo();                     // linear navigation
ed.checkout(v1);                          // jump / branch
ed.releaseSnapshot(id);                   // reclaim unreachable nodes
```

## Tests

`npm test` builds with `tsc` and runs:

- **unit** — UTF-8 validity agrees with the platform decoder on random byte
  strings; encode/decode round-trips; scalar slicing.
- **deterministic** — cross-leaf deletion, empty-string/empty-document
  no-ops, 4-byte character atomicity, invalid-encoding rejection (document
  and history untouched), undo/redo/branching, snapshot sharing (old
  snapshots keep >80% shared nodes after a localized edit), and node
  reclamation after snapshot release (pool returns to the exact reachable
  count).
- **differential fuzz** — seeded random operation sequences (insert /
  delete / undo / redo / snapshot / checkout / release / invalid input)
  checked against an independent splice-based array-of-scalars model,
  verifying text, scalar/byte lengths, AVL invariants, aggregates, and the
  reclamation invariant after every step.
