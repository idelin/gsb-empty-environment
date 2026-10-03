import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import type { ClusterOptions } from '../src/cluster.ts';
import { SafetyOracle } from '../src/invariants.ts';
import type { NodeId } from '../src/types.ts';

export const IDS: readonly NodeId[] = ['n1', 'n2', 'n3', 'n4', 'n5'];

export function makeCluster(opts: ClusterOptions = {}): Cluster {
  return new Cluster(IDS, opts);
}

/** Tick the cluster `ticks` times, asserting all safety invariants after every tick. */
export function runChecked(cluster: Cluster, oracle: SafetyOracle, ticks: number): void {
  for (let i = 0; i < ticks; i++) {
    cluster.tick();
    assert.deepEqual(oracle.check(cluster), []);
  }
}

/** Like Cluster.runUntil, but asserts invariants after every tick. */
export function runUntilChecked(
  cluster: Cluster,
  oracle: SafetyOracle,
  cond: () => boolean,
  maxTicks: number,
): boolean {
  for (let i = 0; i < maxTicks; i++) {
    if (cond()) return true;
    cluster.tick();
    assert.deepEqual(oracle.check(cluster), []);
  }
  return cond();
}

/**
 * No-leak assertion: with a live leader the only legitimate in-flight
 * traffic is one heartbeat round (heartbeats + acks); every node outbox must
 * be drained by the cluster harness each tick.
 */
export function assertInFlightBounded(cluster: Cluster): void {
  const maxInFlight = 2 * (cluster.nodes.length - 1);
  assert.ok(
    cluster.network.pending <= maxInFlight,
    `in-flight messages ${cluster.network.pending} exceed one heartbeat round ${maxInFlight}`,
  );
  for (const n of cluster.nodes) {
    assert.equal(n.drainOutbox().length, 0, `${n.id} outbox must be drained`);
  }
}
