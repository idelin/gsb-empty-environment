import { test } from 'node:test';
import assert from 'node:assert/strict';
import { makeCluster } from '../src/cluster.ts';
import { hashEntry, LEASE_MS } from '../src/node.ts';
import { inspectCluster, snapshotFromNodes } from '../src/inspector.ts';
import { cmd, NOOP, valueEquals, GENESIS_HASH } from '../src/types.ts';
import type { LogEntry, Value } from '../src/types.ts';

function committedValues(node: { committed: ReadonlyMap<number, LogEntry> }): Value[] {
  return [...node.committed.values()].sort((a, b) => a.slot - b.slot).map((e) => e.value);
}

function assertClusterConverged(cluster: ReturnType<typeof makeCluster>, expected: Value[]): void {
  for (const node of cluster.nodes) {
    const values = committedValues(node);
    assert.equal(values.length, expected.length, `${node.id} committed count`);
    for (let i = 0; i < expected.length; i++) {
      assert.ok(valueEquals(values[i], expected[i]), `${node.id} slot ${i + 1}`);
    }
    assert.equal(node.commitIndex, expected.length, `${node.id} commitIndex (no holes)`);
  }
}

test('leader election and basic commit on all nodes', async () => {
  const cluster = makeCluster(5);
  const { nodes, net, monitor } = cluster;
  const slot = await nodes[0].propose(cmd('a'));
  assert.equal(slot, 1);
  await net.flush();
  assertClusterConverged(cluster, [cmd('a')]);
  assert.ok(nodes[0].leaseValid());
  monitor.checkCluster();
  monitor.assertNoLeaks();
  monitor.assertClean();
});

test('leader lease skips Phase 1 for consecutive entries', async () => {
  const cluster = makeCluster(5);
  const { nodes, net, clock } = cluster;
  await nodes[0].propose(cmd('a'));
  await net.flush();

  net.resetStats();
  await nodes[0].propose(cmd('b'));
  await net.flush();
  assert.equal(net.stats.byType.get('Prepare') ?? 0, 0, 'no Phase 1 under valid lease');
  assert.ok((net.stats.byType.get('Accept') ?? 0) > 0, 'Phase 2 still runs');

  clock.advance(LEASE_MS + 1);
  net.resetStats();
  await nodes[0].propose(cmd('c'));
  await net.flush();
  assert.ok((net.stats.byType.get('Prepare') ?? 0) > 0, 'Phase 1 required after lease expiry');
  assertClusterConverged(cluster, [cmd('a'), cmd('b'), cmd('c')]);
  cluster.monitor.assertNoLeaks();
  cluster.monitor.assertClean();
});

test('new leader back-fills holes with no-op via Phase 1', async () => {
  const cluster = makeCluster(5);
  const { nodes, net, clock } = cluster;
  const [n1, , n3, n4] = nodes;

  await n1.propose(cmd('a')); // slot 1 committed everywhere
  await net.flush();

  // Simulate a partially delivered accept from the old leader:
  // slot 3 exists (dirty) on N3 only, slot 2 never existed anywhere.
  const dirtyBallot = { round: 1, nodeId: 'N1' };
  const dirty: LogEntry = {
    slot: 3,
    ballot: dirtyBallot,
    value: cmd('dirty'),
    prevHash: GENESIS_HASH,
    hash: '',
  };
  dirty.hash = hashEntry(dirty.slot, dirty.ballot, dirty.value, dirty.prevHash);
  n3.receive({ type: 'Accept', from: 'N1', entry: dirty });
  await net.flush();
  assert.equal(n3.log.get(3)?.value.kind, 'cmd');

  net.cut(['N1', 'N2'], ['N3', 'N4', 'N5']);
  clock.advance(LEASE_MS + 1);

  await n4.propose(cmd('b')); // triggers Phase 1, back-fill, then new entry
  await net.flush();

  // Slot 2 was a hole in every promise -> filled with no-op.
  // Slot 3 dirty value was seen in N3's promise -> inherited and committed.
  // Slot 4 is the new proposal.
  for (const id of ['N3', 'N4', 'N5']) {
    const node = cluster.byId(id);
    assert.equal(node.commitIndex, 4, `${id} commitIndex`);
    const values = committedValues(node);
    assert.ok(valueEquals(values[0], cmd('a')), `${id} slot 1`);
    assert.ok(valueEquals(values[1], NOOP), `${id} slot 2 must be no-op fill`);
    assert.ok(valueEquals(values[2], cmd('dirty')), `${id} slot 3 inherits highest-ballot value`);
    assert.ok(valueEquals(values[3], cmd('b')), `${id} slot 4`);
  }
  cluster.monitor.assertNoLeaks();
  cluster.monitor.assertClean();
});

test('acceptance: partition, old leader deposed, dirty minority log safely overwritten', async () => {
  const cluster = makeCluster(5);
  const { nodes, net, clock, monitor, quorum } = cluster;
  const [n1, , , n4] = nodes;

  await n1.propose(cmd('x1'));
  await n1.propose(cmd('x2'));
  await net.flush();
  assertClusterConverged(cluster, [cmd('x1'), cmd('x2')]);

  // Network partition: old leader N1 stuck in minority {N1,N2}.
  net.cut(['N1', 'N2'], ['N3', 'N4', 'N5']);

  // Old leader still believes in its lease; its accept lands on the
  // minority only and can never reach a quorum.
  await assert.rejects(() => n1.propose(cmd('dirty3')));
  assert.equal(n1.commitIndex, 2, 'old leader cannot commit in minority');
  assert.equal(n1.log.get(3)?.value.kind, 'cmd', 'dirty entry exists on minority');

  // Majority elects a new leader after the old lease expires.
  clock.advance(LEASE_MS + 1);
  const slot = await n4.propose(cmd('safe3'));
  assert.equal(slot, 3);
  await net.flush();
  for (const id of ['N3', 'N4', 'N5']) {
    assert.equal(cluster.byId(id).commitIndex, 3, `${id} advances under new leader`);
  }

  // Heal: minority dirty log must be safely overwritten by the committed truth.
  net.heal();
  await n4.propose(cmd('x4'));
  await net.flush();

  assertClusterConverged(cluster, [cmd('x1'), cmd('x2'), cmd('safe3'), cmd('x4')]);
  assert.ok(valueEquals(n1.log.get(3)?.value as Value, cmd('safe3')), 'dirty slot 3 overwritten');

  const report = inspectCluster(snapshotFromNodes(nodes, quorum));
  assert.ok(report.ok, `inspection failed: ${JSON.stringify(report)}`);
  monitor.checkCluster();
  monitor.assertNoLeaks();
  monitor.assertClean();
});

test('committed entries survive leader churn (quorum intersection)', async () => {
  const cluster = makeCluster(5);
  const { nodes, net, clock } = cluster;

  await nodes[0].propose(cmd('durable'));
  await net.flush();

  // Three successive new leaders; each must re-propose the committed value.
  for (const idx of [1, 2, 3]) {
    clock.advance(LEASE_MS + 1);
    await nodes[idx].propose(cmd(`m${idx}`));
    await net.flush();
  }
  assertClusterConverged(cluster, [cmd('durable'), cmd('m1'), cmd('m2'), cmd('m3')]);
  cluster.monitor.assertNoLeaks();
  cluster.monitor.assertClean();
});

test('concurrent racing proposers preserve consensus safety', async () => {
  const cluster = makeCluster(5);
  const { nodes, net, clock, monitor } = cluster;

  // Two nodes race to become leader at the same instant.
  const [r1, r2] = await Promise.all([nodes[0].prepare(), nodes[3].prepare()]);
  await net.flush();
  assert.ok(r1 || r2, 'at least one proposer wins');

  // Concurrent proposals from multiple would-be leaders.
  const results = await Promise.allSettled([
    nodes[0].propose(cmd('p0')),
    nodes[3].propose(cmd('p3')),
    nodes[4].propose(cmd('p4')),
  ]);
  await net.flush();
  assert.ok(results.some((r) => r.status === 'fulfilled'), 'at least one proposal commits');

  // Seeded pseudo-random churn: partitions, heals, clock advances, proposals.
  let seed = 42;
  const rand = (): number => {
    seed = (seed * 1103515245 + 12345) % 2147483648;
    return seed / 2147483648;
  };
  for (let round = 0; round < 25; round++) {
    const pick = rand();
    if (pick < 0.35) {
      const ids = nodes.map((n) => n.id);
      const k = 1 + Math.floor(rand() * 2);
      net.cut(ids.slice(0, k), ids.slice(k));
    } else if (pick < 0.55) {
      net.heal();
    } else if (pick < 0.75) {
      clock.advance(Math.floor(rand() * (LEASE_MS + 500)));
    }
    const proposer = nodes[Math.floor(rand() * nodes.length)];
    await proposer.propose(cmd(`r${round}`)).catch(() => undefined);
    await net.flush();
  }

  // Final convergence: heal, expire leases, drive one more proposal.
  net.heal();
  clock.advance(LEASE_MS + 1);
  await nodes[2].propose(cmd('final')).catch(() => undefined);
  await net.flush();

  // Safety: every node's committed prefix agrees, contiguous, no holes.
  const reference = committedValues(nodes[2]);
  for (const node of nodes) {
    const values = committedValues(node);
    assert.equal(values.length, reference.length, `${node.id} committed count`);
    for (let i = 0; i < reference.length; i++) {
      assert.ok(valueEquals(values[i], reference[i]), `${node.id} slot ${i + 1} divergence`);
    }
    assert.equal(node.commitIndex, reference.length, `${node.id} hole in committed prefix`);
  }

  const report = inspectCluster(snapshotFromNodes(nodes, cluster.quorum));
  assert.ok(report.ok, `inspection failed: ${JSON.stringify(report, null, 2)}`);
  monitor.checkCluster();
  monitor.assertNoLeaks();
  monitor.assertClean();
});
