# stable-id-text-sequence

Offline-first text sequence CRDT (RGA-style) in TypeScript, runnable directly
on Node.js >= 23.6 (native type stripping, zero dependencies).

## Features

- **Stable element IDs** — every character is an element `{ replica, counter }`.
- **Causal buffering** — an insert names its left dependency; if it has not
  arrived, the op is buffered and never attached to an arbitrary position.
- **Idempotent deletes + tombstones** — repeated deletes are no-ops; a delete
  that arrives before its insert is remembered, so the char stays invisible.
- **Merge** — set-union over ops: commutative, associative, idempotent.
- **Watermark GC** — tombstones are collected only when every known replica
  has acknowledged a state vector covering the element and its delete.
- **UTF-16 offsets** — public offsets are UTF-16 code units; surrogate pairs
  are never split (mid-pair offsets clamp to the pair boundary). LF and CRLF
  are ordinary characters.

## Usage

```ts
import { TextSequence } from "./src/sequence.ts";

const a = new TextSequence("A");
const ops = a.insertAt(0, "hi \u{1F600}\r\n");
a.deleteAt(0, 1);

const b = new TextSequence("B");
for (const op of a.getOps()) b.apply(op); // any order, duplicates ok
b.merge(a);                               // state-based merge
console.log(b.toString());

// GC: collect tombstones confirmed by ALL replicas
a.collectGarbage({ A: a.getStateVector(), B: b.getStateVector() });
```

## API

- `new TextSequence(replicaId)`
- `insertAt(offset, text): InsertOp[]` — offset in UTF-16 units
- `deleteAt(offset, count): DeleteOp[]` — UTF-16 range, widened to code points
- `apply(op)` — idempotent, buffers missing dependencies
- `merge(other)` — commutative / associative / idempotent
- `toString()`, `length`, `tombstoneCount`, `bufferedCount`
- `getOps()`, `getStateVector()`, `collectGarbage(acks)`

## Run

```sh
npm test    # node --test test/*.test.ts
npm run demo
```
