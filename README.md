# Tombstone GC Coordinator (因果历史修剪与垃圾回收协调器)

TypeScript implementation of causal history pruning and tombstone garbage
collection for long-running CRDT replicas. Tombstones left behind by deletes
are physically reclaimed once a **distributed causal stability frontier**
proves every node in the cluster has observed them.

## Design

- `src/vectorClock.ts` — vector clocks: `tick` / `merge` (join) /
  `dominates` (happened-before) / `entrywiseMin`.
- `src/tombstoneStore.ts` — LWW-style store; deletes retain tombstones
  stamped with the causal (vector) time of deletion, with a tracked
  in-memory byte budget so GC impact is measurable.
- `src/coordinator.ts` — `TombstoneGcCoordinator`. Tracks the latest clock
  advertised by every cluster member; the **causal stability frontier** is
  the component-wise minimum across all known clocks. A tombstone is
  *causally stable* — observed by every node — when the frontier dominates
  its delete timestamp, and is then physically pruned. GC fires
  automatically on every clock observation; no manual sweep is needed.
- `src/clusterNode.ts` — simulated node (store + clock + coordinator) and
  deterministic full-mesh gossip.
- `src/simulation.ts` — the 5-node acceptance scenario.

Safety properties:

- No frontier exists until **every** cluster member has reported a clock, so
  a partitioned node blocks GC instead of causing premature reclamation.
- Tombstones created after the frontier (not yet gossiped) survive GC.

## Requirements

Node.js >= 22.6 (runs TypeScript directly via `--experimental-strip-types`;
no build step and no dependencies).

## Run

```sh
npm run demo    # 5-node simulation with acceptance report
npm test        # node:test built-in test runner
```

## Acceptance scenario (npm run demo)

1. Nodes A–E run partitioned, each accumulating 400 tombstones at causal
   times T ≤ 25, then advancing their clocks past T = 50.
2. Gossip exchanges causal clocks; each node's coordinator recomputes the
   frontier and auto-GCs.
3. Nodes A and B confirm every node passed T = 50 (watermark = 60 ≥ 50);
   all tombstones with T < 50 are physically reclaimed.
4. Cluster memory drops **99.0%** (539.4 KiB → 5.6 KiB; requirement ≥ 90%).
   The only survivors are 5 tombstones created on A *after* the frontier
   and not yet gossiped.

## Tests

`node --experimental-strip-types --test test/*.test.ts` — 8 tests:
vector-clock algebra, tombstone accounting, frontier formation rules,
unknown-node rejection, the 5-node acceptance scenario (memory drop ≥ 90%),
post-frontier tombstone retention, and partition safety.
