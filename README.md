# tree-random-stream

Deterministic, hierarchical random streams for tests. Each test in a tree
gets an independent stream derived from its **name path**, so adding or
removing sibling tests never perturbs existing data — only renaming does.

## Guarantees

- **Tree derivation** — `stream.child(name)` produces a stream that depends
  only on the ancestor name path and the root seed. Siblings are fully
  independent: deriving new siblings, or drawing from the parent, never
  changes existing child streams.
- **Rename-sensitive** — a child's stream is a function of its name, so
  renaming a test changes its stream (and its subtree's).
- **Canonical encoding** — names are NFC-normalized UTF-8, length-prefixed
  as `<byteLength>:<bytes>`. Length-prefixing makes concatenated path
  encodings injective: distinct name sequences can never collide.
- **Ambiguity rejection** — `child()` throws for empty names, names not in
  Unicode NFC, and names containing control characters, `/` or `\`.
- **Batched ≡ one-shot** — output is a pure function of `(seed, index)`
  (SplitMix64), so `take(n)` always equals `n` successive `nextFloat()`
  calls, in any interleaving.
- **No host randomness** — never touches `Math.random`, `crypto`, or the
  clock. Every stream starts from an explicit seed.

## Usage

```ts
import { RandomStream } from "tree-random-stream";

const root = RandomStream.fromSeed(42n);        // bigint | safe integer | string
const suite = root.child("checkout");
const caseA = suite.child("applies-discount");

caseA.nextFloat();        // [0, 1), 53-bit precision
caseA.nextUint32();       // 32-bit unsigned int
caseA.nextInt(1, 7);      // die roll, rejection-sampled (no modulo bias)
caseA.take(100);          // batched floats, ≡ 100 × nextFloat()
```

## Algorithm

- **Generator**: SplitMix64. Draw *i* of a stream is
  `mix64(seed + (i + 1) · 0x9E3779B97F4A7C15) mod 2⁶⁴`, where `mix64` is
  the Stafford mix13 finalizer. All arithmetic is 64-bit `bigint` —
  identical on every platform.
- **Child derivation**: `childSeed = mix64(parentSeed ⊕ FNV1a64(encode(name)))`,
  with `encode(name)` the canonical length-prefixed NFC UTF-8 encoding.
- **String seeds**: `mix64(FNV1a64("seed:<len>:<string>"))`, domain-separated
  from child derivations.

## Fixed vectors

`test/random-stream.test.ts` pins cross-platform vectors, e.g. seed `0`
yields `0xe220a8397b1dcdaf, 0x6e789e6aa1b965f4, …` (the SplitMix64
reference sequence). Any conforming implementation must reproduce them.

## Running tests

Requires Node ≥ 22.6 (native TypeScript type stripping):

```sh
npm test
```
