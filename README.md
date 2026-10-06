# convergent-field-store

A small TypeScript CRDT-style state library. Operations carry a field path, a
logical (Lamport) version and a client id; conflicts are resolved per field by
taking the highest version, then the highest client id. Replaying the same
messages in any order — or restoring from a snapshot — converges to the same
state.

## Semantics

- **Per-field LWW**: each leaf field keeps the write with the max
  `(version, clientId)` stamp. Ties on version are broken by client id, so the
  outcome is deterministic and order independent.
- **Tombstone deletes**: `delete` writes a tombstone at the path. Older writes
  at or below that path stay shadowed forever and cannot resurrect, while a
  newer write (higher stamp) may revive the field.
- **Object updates by leaf field**: setting an object expands into one write
  per leaf field, so concurrent or partial updates to different keys merge.
- **Arrays replaced wholesale**: an array is a single leaf value; writes never
  merge element-wise.
- **Ancestor shadowing**: a leaf is visible only if it holds the max stamp
  along its whole ancestor/descendant chain, so a scalar can replace an object
  (and vice versa) deterministically.

## Usage

```ts
import { Store } from './src/store.ts';

const alice = new Store('alice');
const bob = new Store('bob');

const op1 = alice.set(['user'], { name: 'alice', tags: ['a', 'b'] });
const op2 = bob.set(['user', 'age'], 30);
const op3 = alice.delete(['user', 'name']);

// Apply remote ops in any order; every replica converges.
bob.apply(op1);
bob.apply(op3);
alice.apply(op2);

bob.materialize(); // { user: { age: 30, tags: ['a', 'b'] } }

// Snapshots restore to the same converged state.
const restored = Store.restore(bob.snapshot());
```

Local writes return the `Operation` to broadcast; `apply` feeds remote or
replayed ops and advances the local Lamport clock past observed versions.

## Tests

Requires Node >= 23.6 (runs TypeScript natively, no build step):

```sh
npm test
```

The suite enumerates **all permutations** of short concurrent op sequences
(up to 7 ops / 5040 orderings) and asserts every ordering — and every
snapshot/restore along the way — converges to the same state, that deleted
old values never resurrect, and that independent fields merge.
