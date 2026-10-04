# HexGrid Geo Index (TypeScript)

Icosahedron-based discrete hexagonal hierarchical geospatial index with
purely topological K-ring neighbourhood traversal and a fault-tolerant
persistent store. Plain TypeScript on Node.js (>= 22.6, native type
stripping) — zero runtime dependencies, tests use the built-in
`node:test` runner.

## Spatial index

- **Icosahedral unfolding** (`src/icosahedron.ts`): 12 vertices / 20
  faces built from the golden ratio; each face carries a local hex
  lattice (IJ) frame. Adjacent-face frames are related by exact integer
  affine *edge transforms* (60° rotations + translations), derived once
  at init from the unfolding geometry and verified to be involutions.
- **Aperture-7 hierarchy** (`src/hexmath.ts`): parent/child scaling via
  the integer matrix `M = [[2,-1],[1,3]]` (det = 7); `upAp7`/`downAp7`
  are exact integer digit decompositions — every cell has exactly 7
  children that tile it.
- **Cell ids** (`src/cell.ts`): 64-bit ids rendered as 16-char hex
  strings — `[4b mode][4b resolution][5b face][17×3b digits]`.
  `latLngToCell` (trig confined to this boundary), `cellToLatLng`,
  `cellToParent`, `cellToChildren`, validation.
- **K-ring traversal** (`src/kring.ts`): `gridDisk` / `gridRing` are
  pure integer topology walks — no floating point, no trigonometry
  (enforced by a test that sabotages `Math.*` during traversal). Face
  crossings use resolution-scaled edge transforms (`M^res · t`);
  icosa-vertex defect gaps (the 12 pentagon defects of the tiling)
  yield smaller but still contiguous, duplicate-free disks.

## Fault tolerance (`src/store.ts`, `src/faults.ts`, `src/replication.ts`)

- **WAL + checksum snapshot**: every transaction appends CRC-checked
  records and fsyncs before touching memory.
- **State-safe rollback**: ENOSPC (disk full) during commit truncates
  the WAL to the pre-transaction offset; memory is never dirtied.
- **Crash recovery / self-healing**: on open, corrupt snapshots are
  quarantined (`snapshot.json.corrupt-*`), torn WAL tails from sudden
  power loss are truncated, replay is idempotent (dedup by opId).
- **Packet-loss replication**: cumulative-ACK protocol with
  retransmission and idempotent apply over a channel that drops,
  duplicates and reorders packets (deterministic seeded RNG).
- **Concurrency**: commits serialised through an async mutex; WAL
  sequence numbers stay contiguous under 200+ parallel writers.

## Usage

```bash
npm test        # node --test test/
npm run demo    # node examples/demo.ts
```

```ts
import { latLngToCell, gridDisk, GridIndexStore } from './src/index.ts';

const cell = latLngToCell(39.9042, 116.4074, 8); // '18228614efffffff'
const ring = gridDisk(cell, 3);                  // 37 cells, closed & contiguous

const store = await GridIndexStore.open('./data');
await store.put(cell, 'beijing');                // WAL + fsync, atomic
await store.checkpoint();
await store.close();
```

## Layout

```
src/hexmath.ts       integer hex lattice, aperture-7 up/down
src/icosahedron.ts   icosahedron model + verified edge transforms
src/cell.ts          cell id codec, lat/lng encoding, hierarchy
src/kring.ts         topological K-ring traversal (integer-only)
src/store.ts         WAL/snapshot store with rollback & recovery
src/faults.ts        power-loss / ENOSPC / packet-loss injectors
src/replication.ts   loss-tolerant replication protocol
test/                node:test suites (geo, store, faults, concurrency)
examples/demo.ts     end-to-end runnable sample
```
