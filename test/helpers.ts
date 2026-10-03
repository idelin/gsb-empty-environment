import assert from 'node:assert/strict';
import { Cluster, PaxosNode } from '../src/paxos.ts';
import { Network } from '../src/transport.ts';
import { SagaOrchestrator } from '../src/saga.ts';

/** Strict no-leak assertions shared by every test. */
export function assertNoLeaks(
  net: Network,
  nodes: PaxosNode[],
  orchestrators: SagaOrchestrator[] = [],
): void {
  assert.equal(net.inFlight, 0, 'network must have no in-flight messages');
  assert.equal(net.delivered + net.dropped, net.sent, 'every sent message accounted for');
  for (const node of nodes) {
    assert.ok(node.isQuiescent, `node ${node.id} has a pending campaign`);
  }
  for (const orch of orchestrators) {
    assert.equal(orch.effects.pending, 0, 'no side effect may be in flight');
    assert.equal(orch.pendingSagaCount, 0, 'every saga must reach a terminal state');
    assert.ok(orch.isQuiescent, 'orchestrator must be quiescent');
  }
}

/** Every node's committed prefix must be identical and hole-free. */
export function assertConsistentLogs(cluster: Cluster): void {
  let reference: PaxosNode | null = null;
  for (const node of cluster.nodes.values()) {
    if (!reference || node.commitIndex > reference.commitIndex) reference = node;
  }
  const maxCommit = reference!.commitIndex;
  for (const node of cluster.nodes.values()) {
    assert.equal(node.applied.length, node.commitIndex, `${node.id}: applied stream must be contiguous`);
    for (let slot = 1; slot <= node.commitIndex; slot++) {
      assert.ok(node.log.has(slot), `${node.id}: hole at slot ${slot}`);
    }
  }
  for (const node of cluster.nodes.values()) {
    for (let slot = 1; slot <= Math.min(node.commitIndex, maxCommit); slot++) {
      assert.deepEqual(
        node.log.get(slot)?.entry,
        reference!.log.get(slot)?.entry,
        `slot ${slot}: ${node.id} diverged from ${reference!.id}`,
      );
    }
  }
}

export function payloads(node: PaxosNode): unknown[] {
  return node.applied.map((e) => e.payload);
}
