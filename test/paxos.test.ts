import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SimNet } from '../src/simnet.ts';
import { ObjectPool } from '../src/pool.ts';
import { PaxosNode, NOOP, makeEntry, resetEntry, makeBallot } from '../src/paxos.ts';
import type { LogEntry } from '../src/paxos.ts';

const N = 5;
const LEASE_MS = 1000;

function makeCluster() {
  const net = new SimNet();
  net.latency = 1;
  const entryPool = new ObjectPool<LogEntry>(makeEntry, resetEntry, 4096, 512);
  const nodes: PaxosNode[] = [];
  for (let i = 0; i < N; i++) nodes.push(new PaxosNode(i, net, N, LEASE_MS, entryPool));
  return { net, nodes, entryPool };
}

/** Strict teardown: every pooled entry and message envelope must come back. */
function assertNoLeaks(net: SimNet, nodes: PaxosNode[], entryPool: ObjectPool<LogEntry>): void {
  net.flush();
  assert.equal(net.pending, 0, 'no undelivered messages left behind');
  for (const n of nodes) n.dispose();
  entryPool.assertNoLeaks('entryPool');
  net.msgPool.assertNoLeaks('msgPool');
}

function assertNoSafetyViolations(nodes: PaxosNode[]): void {
  for (const n of nodes) assert.equal(n.stats.safetyViolations, 0, `node ${n.id} safety`);
}

test('election + basic replication commits on all 5 nodes', () => {
  const { net, nodes, entryPool } = makeCluster();
  nodes[0].becomeCandidate();
  net.flush();
  assert.equal(nodes[0].role, 'leader');

  nodes[0].propose('A');
  nodes[0].propose('B');
  nodes[0].propose('C');
  net.flush();

  for (const n of nodes) assert.deepEqual(n.committedLog(), ['A', 'B', 'C']);
  assertNoSafetyViolations(nodes);
  assertNoLeaks(net, nodes, entryPool);
});

test('leader lease bypasses Phase 1 for consecutive entries', () => {
  const { net, nodes, entryPool } = makeCluster();
  nodes[0].becomeCandidate();
  net.flush();
  assert.equal(nodes[0].stats.prepareRounds, 1);

  for (let i = 0; i < 10; i++) {
    const r = nodes[0].propose(`v${i}`);
    assert.equal(r.ok, true);
  }
  net.flush();

  assert.equal(nodes[0].stats.prepareRounds, 1, 'no extra Prepare rounds under a valid lease');
  assert.equal(nodes[0].stats.fastPathProposals, 10, 'all proposals took the Phase-2 fast path');
  assert.equal(nodes[0].committedIndex, 10);
  assertNoLeaks(net, nodes, entryPool);
});

test('lease expiry blocks the fast path until re-election', () => {
  const { net, nodes, entryPool } = makeCluster();
  nodes[0].becomeCandidate();
  net.flush();
  nodes[0].propose('x');
  net.flush();

  net.advance(LEASE_MS + 1);
  const r = nodes[0].propose('y');
  assert.equal(r.ok, false);
  assert.equal(r.reason, 'lease-expired');

  nodes[0].becomeCandidate(); // re-run Phase 1 to renew
  net.flush();
  assert.equal(nodes[0].stats.prepareRounds, 2);
  const r2 = nodes[0].propose('y');
  assert.equal(r2.ok, true);
  net.flush();
  assert.deepEqual(nodes[0].committedLog(), ['x', 'y']);
  assertNoLeaks(net, nodes, entryPool);
});

test('non-leader proposals are rejected', () => {
  const { net, nodes, entryPool } = makeCluster();
  nodes[0].becomeCandidate();
  net.flush();
  const r = nodes[1].propose('nope');
  assert.equal(r.ok, false);
  assert.equal(r.reason, 'not-leader');
  assertNoLeaks(net, nodes, entryPool);
});

test('Phase 1 repair fills holes with NOOP and re-proposes surviving values', () => {
  const { net, nodes, entryPool } = makeCluster();
  nodes[0].becomeCandidate();
  net.flush();
  nodes[0].propose('v1');
  nodes[0].propose('v2');
  net.flush();
  assert.deepEqual(nodes[2].committedLog(), ['v1', 'v2']);

  // Craft a hole: node2 holds an uncommitted term-1 entry at slot 4 while
  // slot 3 is empty (simulates a partially-delivered Accept storm).
  const m = net.msgPool.acquire();
  m.type = 'accept';
  m.from = 0;
  m.to = 2;
  m.ballot = makeBallot(1, 0);
  m.slot = 4;
  m.value = 'x';
  nodes[2].handle(m);
  net.msgPool.release(m);
  net.flush();
  assert.equal(nodes[2].entryAt(4)?.value, 'x');
  assert.equal(nodes[2].committedIndex, 2, 'slot 4 is dirty, slot 3 is a hole');

  // Partition the old leader away; node2 wins Phase 1 in the majority.
  net.partition([0, 1], [2, 3, 4]);
  nodes[2].becomeCandidate();
  net.flush();

  assert.equal(nodes[2].role, 'leader');
  assert.equal(nodes[2].stats.repairProposals, 2, 'slots 3 and 4 re-proposed');
  for (const id of [2, 3, 4]) {
    assert.deepEqual(
      nodes[id].committedLog(),
      ['v1', 'v2', NOOP, 'x'],
      `node ${id}: hole filled with NOOP, surviving value preserved, no gap`,
    );
  }
  assertNoSafetyViolations(nodes);
  assertNoLeaks(net, nodes, entryPool);
});

test('network partition: old leader replaced, minority dirty log safely overwritten', () => {
  const { net, nodes, entryPool } = makeCluster();

  // 1. node0 is elected and commits A,B,C cluster-wide.
  nodes[0].becomeCandidate();
  net.flush();
  nodes[0].propose('A');
  nodes[0].propose('B');
  nodes[0].propose('C');
  net.flush();
  for (const n of nodes) assert.deepEqual(n.committedLog(), ['A', 'B', 'C']);

  // 2. Partition: {0,1} minority vs {2,3,4} majority.
  net.partition([0, 1], [2, 3, 4]);

  // 3. Old leader (lease still valid) replicates dirty entries to the
  //    minority only — they can never reach a quorum, never commit.
  assert.equal(nodes[0].propose('dirty1').ok, true);
  assert.equal(nodes[0].propose('dirty2').ok, true);
  net.flush();
  assert.equal(nodes[0].committedIndex, 3, 'minority accepts must not commit');
  assert.equal(nodes[0].entryAt(4)?.value, 'dirty1');
  assert.equal(nodes[1].entryAt(5)?.value, 'dirty2');
  assert.equal(nodes[1].entryAt(4)?.committed, false);

  // 4. Majority side elects a new leader; Phase 1 completes with quorum.
  nodes[2].becomeCandidate();
  net.flush();
  assert.equal(nodes[2].role, 'leader');
  assert.equal(nodes[2].committedIndex, 3, 'no committed value lost in handover');

  // 5. New leader commits fresh values N1,N2 on the majority (slots 4,5).
  nodes[2].propose('N1');
  nodes[2].propose('N2');
  net.flush();
  for (const id of [2, 3, 4]) {
    assert.deepEqual(nodes[id].committedLog(), ['A', 'B', 'C', 'N1', 'N2']);
  }
  assert.equal(nodes[0].committedIndex, 3, 'partitioned minority cannot advance');

  // 6. Heal. Old leader steps down on first higher-ballot contact; the next
  //    proposal triggers catch-up that overwrites the dirty minority log.
  net.healAll();
  nodes[2].propose('N3');
  net.flush();

  assert.equal(nodes[0].role, 'follower', 'old leader stepped down');
  assert.equal(nodes[0].stats.stepDowns >= 1, true);
  const expected = ['A', 'B', 'C', 'N1', 'N2', 'N3'];
  for (const n of nodes) {
    assert.deepEqual(n.committedLog(), expected, `node ${n.id} fully converged`);
    assert.equal(n.entryAt(4)?.value, 'N1', 'dirty1 safely overwritten');
    assert.equal(n.entryAt(4)?.committed, true);
    assert.equal(n.entryAt(5)?.value, 'N2', 'dirty2 safely overwritten');
  }

  // 7. Safety: no committed entry was ever altered anywhere.
  assertNoSafetyViolations(nodes);
  assertNoLeaks(net, nodes, entryPool);
});

test('committed values are never lost across successive leader changes', () => {
  const { net, nodes, entryPool } = makeCluster();
  const committed: string[] = [];

  // Leader 0 commits a batch.
  nodes[0].becomeCandidate();
  net.flush();
  nodes[0].propose('e1');
  nodes[0].propose('e2');
  net.flush();
  committed.push('e1', 'e2');

  // Leadership moves to node 3, then node 1; each preserves the prefix.
  for (const next of [3, 1]) {
    nodes[next].becomeCandidate();
    net.flush();
    assert.equal(nodes[next].role, 'leader');
    assert.deepEqual(nodes[next].committedLog(), committed, 'prefix preserved across election');
    const value = `e${committed.length + 1}`;
    nodes[next].propose(value);
    net.flush();
    committed.push(value);
  }

  for (const n of nodes) assert.deepEqual(n.committedLog(), committed);
  assertNoSafetyViolations(nodes);
  assertNoLeaks(net, nodes, entryPool);
});
