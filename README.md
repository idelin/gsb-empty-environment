# hash-tree-verifier

Chunked Merkle-tree file verification tool (TypeScript, Node >= 23, no
dependencies). A publisher commits to a file by its tree root; a verifier
can then check any byte range using only the chunks and proof it received
— it never reads the full file.

## Hash format

- File is split into fixed-size chunks of `chunkSize` bytes.
- **Padding rule (fixed):** the final chunk is zero-padded up to
  `chunkSize` before hashing.
- **Leaf:** `sha256(0x00 || fileLength:u64be || chunkSize:u32be || chunkIndex:u64be || paddedChunk)`
- **Internal node:** `sha256(0x01 || left || right)`; an odd trailing node
  at any level is promoted unchanged to the next level.
- **Tree root (commitment):** `sha256(0x02 || fileLength:u64be || chunkSize:u32be || merkleRoot)`

Because the length and chunk size are bound into every leaf and into the
root, changing the total length, swapping blocks, or splicing a proof from
another version of the file can never recompute to the trusted root.

## Verification semantics

`verifyRange` reports one status per chunk in the requested range:

| status | meaning |
| --- | --- |
| `verified` | chunk + proof recompute to the trusted root |
| `corrupt` | chunk present, proof structurally valid, root mismatch |
| `missing` | chunk in the requested range was not received |
| `insufficient-evidence` | proof absent, malformed (e.g. duplicated path nodes) or incomplete |

Chunks that were never received are reported `missing`, never `corrupt`.

## Usage

```sh
npm test                 # run the test suite (node --test)

# CLI
node src/cli.ts build  file.bin --chunk-size 4096 --tree-out file.tree.json
node src/cli.ts prove  file.bin --tree file.tree.json --start 4096 --end 12288 \
     --proof-out proof.json --chunks-dir chunks
node src/cli.ts verify --tree file.tree.json --start 4096 --end 12288 \
     --proof proof.json --chunks-dir chunks
```

Library API:

- `HashTree.build(reader, chunkSize)` — publish-time build (reads file once).
- `tree.proveRange(reader, start, end)` — proof + chunks for a byte range;
  reads only the requested chunks from the `RandomAccessReader`.
- `verifyRange({ commitment, fileLength, chunkSize, start, end, chunks, proof })`
  — pure function; verifies using only the supplied evidence.

## Layout

- `src/merkle.ts` — hash primitives, padding rule, tree shape.
- `src/tree.ts` — `HashTree` builder/prover, readers (`Buffer`, file).
- `src/verify.ts` — range verifier and report types.
- `src/cli.ts` — command-line interface.
- `test/hash-tree.test.ts` — round-trip, tamper and proof-splicing tests.
