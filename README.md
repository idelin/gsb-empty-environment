# bptree — balanced-parentheses ordered rooted tree index

Succinct-style encoding of ordered rooted trees in Go (stdlib only).

## Encoding layout (real bytes, no map wrappers)

- **Bitvector**: 2N bits for N nodes, `[]uint64`, 1 = `(`, 0 = `)`. Node `v`
  (preorder id) has its open paren at `Select(v)`; its subtree spans
  `[Select(v), FindClosePos(Select(v))]`.
- **Block** (64 bits = 1 word), 10 B: `base i32, total i16, relMin i16, relOpen u16`.
- **Superblock** (16 blocks = 1024 bits), 32 B: `excess, min, opens, total i64`.

Queries skip whole blocks/superblocks via the excess summaries; nothing
scans the full parentheses string. No recursion anywhere (deep chains safe).

## Operations

`Parse/Build` (validates: non-empty, even length, prefix balance never
negative, total balance zero), `Depth`, `Parent`, `FirstChild`,
`NextSibling`, `PrevSibling`, `Degree`, `Child`, `FindClosePos`,
`FindOpenPos`, `EnclosePos`, `LCA`, `SubtreeInterval`, `SubtreeSize`,
`Rank`/`Select` (position <-> preorder id), `MarshalBinary`/`Unmarshal`
(recomputes and verifies every stored min-excess and count; rejects
corruption), `Layout` (byte occupancy), `Accesses` (block-access counter).

## Run

```sh
go test ./...        # exhaustive check vs independent pointer tree, etc.
go run ./cmd/bptree-demo
```
