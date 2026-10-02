import { test } from 'node:test';
import assert from 'node:assert/strict';
import { InvariantMonitor } from '../src/invariants.ts';
import { hashEntry } from '../src/node.ts';
import { cmd, GENESIS_HASH } from '../src/types.ts';
import type { LogEntry } from '../src/types.ts';
import { makeCluster } from '../src/cluster.ts';

function entry(slot: number, data: string, round = 1, prevHash = GENESIS_HASH): LogEntry {
  const ballot = { round, nodeId: 'N1' };
  const value = cmd(data);
  return { slot, ballot, value, prevHash, hash: hashEntry(slot, ballot, value, prevHash) };
}

function fakeNode(id: string, overrides: Record<string, unknown> = {}) {
  return {
    id,
    leaseValid: () => false,
    pendingOperations: () => 0,
    committed: new Map<number, LogEntry>(),
    commitIndex: 0,
    ...overrides,
  };
}

test('ballot regression is flagged', () => {
  const monitor = new InvariantMonitor();
  monitor.onPromise('N1', { round: 5, nodeId: 'N2' }, { round: 3, nodeId: 'N1' });
  assert.equal(monitor.violations.length, 1);
  assert.equal(monitor.violations[0].kind, 'BALLOT_REGRESSION');
  assert.throws(() => monitor.assertClean(), /BALLOT_REGRESSION/);
});

test('monotonic ballot promise is accepted', () => {
  const monitor = new InvariantMonitor();
  monitor.onPromise('N1', { round: 3, nodeId: 'N1' }, { round: 5, nodeId: 'N2' });
  monitor.assertClean();
});

test('committed overwrite with a different value is flagged', () => {
  const monitor = new InvariantMonitor();
  monitor.onOverwrite('N1', 3, entry(3, 'old'), entry(3, 'new', 2), true);
  assert.equal(monitor.violations[0]?.kind, 'COMMITTED_OVERWRITE');
});

test('uncommitted overwrite by higher ballot is allowed', () => {
  const monitor = new InvariantMonitor();
  monitor.onOverwrite('N1', 3, entry(3, 'old'), entry(3, 'new', 2), false);
  monitor.assertClean();
});

test('overwrite by lower ballot is flagged', () => {
  const monitor = new InvariantMonitor();
  monitor.onOverwrite('N1', 3, entry(3, 'x', 5), entry(3, 'x', 2), false);
  assert.equal(monitor.violations[0]?.kind, 'BALLOT_REGRESSION');
});

test('cross-node committed conflict is flagged at commit time', () => {
  const monitor = new InvariantMonitor();
  monitor.onCommit('N1', 1, entry(1, 'a'));
  monitor.onCommit('N2', 1, entry(1, 'b'));
  assert.equal(monitor.violations.length, 1);
  assert.equal(monitor.violations[0].kind, 'COMMITTED_CONFLICT');
});

test('same committed value on all nodes is clean', () => {
  const monitor = new InvariantMonitor();
  const e = entry(1, 'a');
  monitor.onCommit('N1', 1, e);
  monitor.onCommit('N2', 1, e);
  monitor.onCommit('N3', 1, e);
  monitor.assertClean();
});

test('checkCluster detects a hole under the commit watermark', () => {
  const monitor = new InvariantMonitor();
  const node = fakeNode('N1', { commitIndex: 3, committed: new Map([[1, entry(1, 'a')], [3, entry(3, 'c')]]) });
  monitor.bind([node]);
  monitor.checkCluster();
  assert.ok(monitor.violations.some((v) => v.kind === 'LOG_HOLE' && v.detail.includes('slot 2')));
});

test('checkCluster detects divergent committed values across nodes', () => {
  const monitor = new InvariantMonitor();
  const a = fakeNode('N1', { commitIndex: 1, committed: new Map([[1, entry(1, 'a')]]) });
  const b = fakeNode('N2', { commitIndex: 1, committed: new Map([[1, entry(1, 'b')]]) });
  monitor.bind([a, b]);
  monitor.checkCluster();
  assert.ok(monitor.violations.some((v) => v.kind === 'COMMITTED_CONFLICT'));
});

test('checkCluster detects simultaneous lease holders', () => {
  const monitor = new InvariantMonitor();
  const a = fakeNode('N1', { leaseValid: () => true });
  const b = fakeNode('N2', { leaseValid: () => true });
  monitor.bind([a, b]);
  monitor.checkCluster();
  assert.ok(monitor.violations.some((v) => v.kind === 'LEASE_CONFLICT'));
});

test('single lease holder passes exclusivity', () => {
  const monitor = new InvariantMonitor();
  monitor.bind([fakeNode('N1', { leaseValid: () => true }), fakeNode('N2')]);
  monitor.checkCluster();
  monitor.assertClean();
});

test('assertNoLeaks is strict about pending operations', () => {
  const monitor = new InvariantMonitor();
  monitor.bind([fakeNode('N1', { pendingOperations: () => 2 })]);
  assert.throws(() => monitor.assertNoLeaks(), /LEAK/);
});

test('assertNoLeaks passes when fully settled', () => {
  const monitor = new InvariantMonitor();
  monitor.bind([fakeNode('N1'), fakeNode('N2')]);
  monitor.assertNoLeaks();
});

test('corrupted entry hash is rejected and flagged on a live node', async () => {
  const cluster = makeCluster(3);
  const { nodes, net, monitor } = cluster;
  const bad = entry(1, 'forged');
  bad.hash = '0'.repeat(40); // tampered hash
  nodes[1].receive({ type: 'Accept', from: 'N1', entry: bad });
  await net.flush();
  assert.ok(monitor.violations.some((v) => v.kind === 'HASH_INTEGRITY' && v.node === 'N2'));
  assert.equal(nodes[1].log.size, 0, 'corrupted entry must not be stored');
});

test('accept below promised ballot is nacked (ballot safety)', async () => {
  const cluster = makeCluster(3);
  const { nodes, net } = cluster;
  const [, n2] = nodes;
  // N2 promises a high ballot, then a stale low-ballot accept arrives.
  n2.receive({ type: 'Prepare', from: 'N3', ballot: { round: 9, nodeId: 'N3' } });
  await net.flush();
  const stale = entry(1, 'stale', 1);
  n2.receive({ type: 'Accept', from: 'N1', entry: stale });
  await net.flush();
  assert.equal(n2.log.size, 0, 'stale accept must be rejected');
  assert.ok((net.stats.byType.get('AcceptNack') ?? 0) >= 1);
  cluster.monitor.assertNoLeaks();
  cluster.monitor.assertClean();
});
