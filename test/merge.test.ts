declare function require(name: string): any;

const { test } = require('node:test');
const assert = require('node:assert/strict');

import { Simulation } from '../src/simulation';
import { Network } from '../src/network';
import { Node, defaultConfig } from '../src/node';
import { mulberry32 } from '../src/util';
import { Message } from '../src/types';

const GROUP_A = [1, 2, 3, 4, 5];
const GROUP_B = [6, 7, 8, 9, 10];

function partitionedSim(seed = 42): Simulation {
  const sim = new Simulation({ seed });
  sim.run(30);
  sim.partition([GROUP_A, GROUP_B]);
  sim.run(300);
  return sim;
}

test('partition splits the cluster into two closed loops', () => {
  const sim = partitionedSim();
  for (const id of GROUP_A) {
    assert.deepEqual(sim.nodes.get(id)!.memberIds(), '1,2,3,4,5');
  }
  for (const id of GROUP_B) {
    assert.deepEqual(sim.nodes.get(id)!.memberIds(), '6,7,8,9,10');
  }
  assert.ok(sim.network.stats.droppedByPartition > 0, 'cross-group traffic must be dropped');
  assert.equal(sim.stats.firstCrossGroupSuccess, null, 'no cross-group success during partition');
});

test('first successful cross-group PingReq triggers merge handshake and converges without deadlock', () => {
  const sim = partitionedSim();
  sim.heal();
  const ok = sim.runUntil(() => sim.converged(), 2000);

  assert.ok(ok, 'cluster must converge within the tick budget (no deadlock)');
  const first = sim.stats.firstCrossGroupSuccess!;
  assert.ok(first, 'a cross-group success must be observed');
  assert.equal(first.kind, 'ping-req', 'first cross-group success must be a PingReq');
  assert.ok(sim.stats.mergeProposals > 0, 'merge handshake must be initiated');
  assert.ok(sim.stats.mergeCommits > 0, 'merge commit must be broadcast');

  const epochs = new Set([...sim.nodes.values()].map((n) => n.epoch));
  assert.equal(epochs.size, 1, 'all nodes agree on one cluster epoch');
  assert.ok([...epochs][0] >= 1, 'epoch bumped by the merge');
  for (const node of sim.nodes.values()) {
    assert.equal(node.state, 'IDLE', `node ${node.id} must not be stuck in a handshake state`);
    assert.equal(node.aliveCount(), 10, `node ${node.id} sees all 10 members alive`);
  }
});

test('simultaneous merge initiation is resolved by deterministic tie-break', () => {
  const sim = partitionedSim();
  sim.heal();
  // Force both sides to initiate at the same tick before any traffic flows.
  sim.nodes.get(1)!.initiateMergeWith(6, sim.now);
  sim.nodes.get(6)!.initiateMergeWith(1, sim.now);
  const ok = sim.runUntil(() => sim.converged(), 2000);

  assert.ok(ok, 'concurrent handshakes must still converge (no deadlock)');
  const epochs = new Set([...sim.nodes.values()].map((n) => n.epoch));
  assert.equal(epochs.size, 1);
  assert.equal([...epochs][0], 1, 'exactly one merge wins; epoch bumps exactly once');
});

test('merge commit is idempotent', () => {
  const network = new Network();
  network.join(1);
  network.join(2);
  const rng = mulberry32(7);
  const node = new Node(1, [1, 2], network, rng, defaultConfig);
  const peer = new Node(2, [1, 2], network, rng, defaultConfig);

  const commit: Message = {
    type: 'merge-commit', from: 2, to: 1, clock: peer.clock,
    epoch: 5, view: { epoch: 5, members: peer.snapshot().members },
  };
  node.receive(commit, 100);
  const epochAfterFirst = node.epoch;
  const appliedAfterFirst = node.stats.commitsApplied;
  node.receive({ ...commit }, 101);

  assert.equal(node.epoch, epochAfterFirst, 'epoch unchanged on duplicate commit');
  assert.equal(node.stats.commitsApplied, appliedAfterFirst, 'duplicate commit is a no-op');
});

test('lamport clock is corrected on merge', () => {
  const network = new Network();
  network.join(1);
  network.join(2);
  const rng = mulberry32(9);
  const initiator = new Node(1, [1, 2], network, rng, defaultConfig);
  const responder = new Node(2, [1, 2], network, rng, defaultConfig);
  // Give the responder a divergent view so a merge is actually required.
  responder.members.set(3, { id: 3, incarnation: 1, status: 'alive', statusChangedAt: 0 });
  initiator.clock = 100;
  responder.clock = 500;

  initiator.initiateMergeWith(2, 0);
  network.now = 1;
  for (const msg of network.deliver()) {
    (msg.to === 2 ? responder : initiator).receive(msg, 1);
  }
  // responder's accept carries its larger clock back to the initiator
  network.now = 2;
  for (const msg of network.deliver()) {
    (msg.to === 2 ? responder : initiator).receive(msg, 2);
  }
  assert.ok(initiator.clock > 500, `initiator clock corrected past responder's (${initiator.clock})`);
});

test('post-merge clocks stay causally consistent across the healed cluster', () => {
  const sim = partitionedSim();
  const preHealClocks = new Map([...sim.nodes.values()].map((n) => [n.id, n.clock]));
  sim.heal();
  assert.ok(sim.runUntil(() => sim.converged(), 2000));
  sim.run(100);
  const clocks = [...sim.nodes.values()].map((n) => n.clock);
  for (const node of sim.nodes.values()) {
    assert.ok(node.clock > preHealClocks.get(node.id)!, `node ${node.id} clock advanced`);
  }
  const spread = Math.max(...clocks) - Math.min(...clocks);
  assert.ok(spread <= 100, `clock spread across nodes stays bounded (got ${spread})`);
});
