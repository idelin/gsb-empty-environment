import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import { ballotKey } from '../src/types.ts';

/** Deterministic PRNG (mulberry32) for reproducible adversarial interleavings. */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Strict no-leak assertions: nothing in flight, nothing buffered, safety holds. */
export function assertNoLeaks(c: Cluster): void {
  assert.equal(c.pendingEvents, 0, 'event queue must be fully drained (no message leak)');
  for (const n of c.nodes) {
    assert.equal(n.pendingClientCount, 0, `node ${n.id} must not buffer client values`);
  }
  c.checkSafety();
}

/** Serializable snapshot of every node's externally visible state. */
export function snapshot(c: Cluster): unknown {
  return c.nodes.map((n) => ({
    id: n.id,
    role: n.role,
    promised: ballotKey(n.promised),
    commitIndex: n.commitIndex,
    log: [...n.log.entries()].map(([slot, e]) => [slot, e.value, ballotKey(e.ballot)]),
  }));
}
