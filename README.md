# pairwise-ot

Insert/delete-only operational transformation (OT) for plain text, written in
dependency-free TypeScript. Runs on Node.js >= 22.6 (type stripping) with the
built-in `node:test` runner — no build step, no dependencies.

## Model

- A document is a JS string. **All public offsets and lengths are UTF-16 code
  units** (same as JS string indexing): `\r\n` counts as 2, an astral
  character (surrogate pair) counts as 2.
- Operations never split a surrogate pair: `validateOp` rejects inserts,
  deletes, and insert texts that would (`InvalidOperationError`).
- Two primitives:
  - `insert(pos, text)`
  - `del(pos, length)`
- Ops exchanged between sites carry a **base version** and a **site id**:
  `{ baseVersion, siteId, op }`.

## Transform rules

`transform(op, against, selfSite, againstSite)` returns 0–2 ops (a delete may
be split in two, or vanish if its range was already deleted). Split delete
segments are returned in application order; the second segment's position is
expressed in coordinates *after* the first segment is applied.

- **insert vs insert**: position shifts by the other insert's length. At the
  same position, the insert from the **lower site id goes first** (stable,
  globally consistent order; equal site ids fall back to text comparison).
- **insert vs delete**: shifts left past deleted ranges; an insert point
  inside a deleted range clamps to the range start.
- **delete vs insert**: shifts right past inserts; an insert strictly inside
  the delete range splits it into two deletes.
- **delete vs delete**: only the non-overlapping remainder survives — the
  intersection is deleted exactly once, never twice.

## Convergence guarantee

For any two ops `a` and `b` created against the **same base version**
(TP1), applying them in either delivery order yields identical text:

```
apply(apply(S, a), transform(b, a)) === apply(apply(S, b), transform(a, b))
```

`Replica` tracks a version counter and per-version history; `receive`
transforms an incoming op over everything applied since its `baseVersion`
and rejects unusable base versions (`InvalidBaseVersionError`: non-integer,
negative, or greater than the current version). Serialized chains (each op
based on the latest state, or a server imposing a total order) converge for
any number of sites.

## Usage

```ts
import { del, insert, Replica } from './src/index.ts';

const alice = new Replica(1, 'hello\r\nworld');
const bob = new Replica(2, 'hello\r\nworld');

const fromAlice = alice.local(insert(0, '>> '));  // { baseVersion: 0, siteId: 1, op }
const fromBob = bob.local(del(6, 5));             // concurrent, same base version

alice.receive(fromBob);
bob.receive(fromAlice);
console.log(alice.text === bob.text); // true
```

## Run

```sh
nvm use            # Node 23 (see .nvmrc); any Node >= 22.6 works
npm test           # node --test (16 tests, incl. seeded fuzz)
npm run demo       # examples/demo.ts
```

Sample demo output:

```
initial document: "The quick\r\nbrown fox\njumps 🦀 over"

alice inserts "very " at 4   -> "The very quick\r\nbrown fox\njumps 🦀 over"
bob deletes [11, 17) "brown " -> "The quick\r\nfox\njumps 🦀 over"

after exchanging ops:
alice: "The very quick\r\nfox\njumps 🦀 over" (version 2)
bob:   "The very quick\r\nfox\njumps 🦀 over" (version 2)
converged: true

rejected stale/future base version: invalid baseVersion 99: must be an integer in [0, 2]
rejected pair-splitting delete: delete end at offset 27 splits a surrogate pair
```

## Layout

- `src/ot.ts` — op types, validation, `applyOp`, `transform`
- `src/replica.ts` — `Replica`: version counter, history, `local`/`receive`
- `src/index.ts` — public exports
- `test/ot.test.ts` — unit + property (seeded fuzz) tests
- `examples/demo.ts` — runnable two-site demo
