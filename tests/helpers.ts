import assert from 'node:assert/strict';
import type { Cluster } from '../src/cluster.ts';

/** Collects unhandled rejections for the whole test file (own process). */
export const rejections: unknown[] = [];
process.on('unhandledRejection', (r) => {
  rejections.push(r);
});

export function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

/**
 * Strict no-leak / no-corruption assertion, run at the end of every test:
 * the network is quiescent, no timers survive, no node observed a protocol
 * safety violation and no promise rejection went unhandled.
 */
export async function assertNoLeaks(cluster: Cluster): Promise<void> {
  await cluster.settle();
  assert.equal(cluster.net.inFlight, 0, 'network in-flight messages leaked');
  assert.equal(cluster.net.pendingTimers, 0, 'network timers leaked');
  for (const n of cluster.nodes) {
    assert.deepEqual(n.safetyViolations, [], `node ${n.id} safety violations`);
  }
  assert.deepEqual(rejections, [], 'unhandled promise rejections');
}
