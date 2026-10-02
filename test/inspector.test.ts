import { test } from 'node:test';
import assert from 'node:assert/strict';
import { inspectCluster, snapshotFromNodes } from '../src/inspector.ts';
import type { ClusterSnapshot, NodeSnapshot } from '../src/inspector.ts';
import { hashEntry } from '../src/node.ts';
import { cmd, GENESIS_HASH, ZERO_BALLOT } from '../src/types.ts';
import type { LogEntry } from '../src/types.ts';
import { makeCluster } from '../src/cluster.ts';

function entry(slot: number, prevHash: string, data = `v${slot}`, hash?: string): LogEntry {
  const ballot = { round: 1, nodeId: 'N1' };
  const value = cmd(data);
  return {
    slot,
    ballot,
    value,
    prevHash,
    hash: hash ?? hashEntry(slot, ballot, value, prevHash),
  };
}

function cleanSnapshot(): ClusterSnapshot {
  const e1 = entry(1, GENESIS_HASH);
  const e2 = entry(2, e1.hash);
  const node = (id: string): NodeSnapshot => ({
    id,
    promised: ZERO_BALLOT,
    log: [e1, e2],
    committed: [e1, e2],
    commitIndex: 2,
    lease: null,
  });
  return { quorum: 2, nodes: [node('N1'), node('N2'), node('N3')] };
}

test('clean snapshot passes inspection', () => {
  const report = inspectCluster(cleanSnapshot());
  assert.ok(report.ok, JSON.stringify(report, null, 2));
  assert.equal(report.danglingRefs.length, 0);
  assert.equal(report.orphanLoops.length, 0);
  assert.equal(report.dataSkew.length, 0);
  assert.equal(report.committedConflicts.length, 0);
});

test('empty cluster snapshot is trivially ok', () => {
  const report = inspectCluster({ quorum: 1, nodes: [] });
  assert.ok(report.ok);
});

test('detects dangling prevHash to a missing entry', () => {
  const snap = cleanSnapshot();
  snap.nodes[0].log[1] = entry(2, 'deadbeef'.repeat(8));
  const report = inspectCluster(snap);
  assert.ok(!report.ok);
  assert.ok(report.danglingRefs.some((f) => f.detail.includes('missing entry')));
});

test('detects slot 1 not anchored to genesis', () => {
  const snap = cleanSnapshot();
  const e1 = entry(1, GENESIS_HASH);
  snap.nodes[0].log[0] = entry(1, e1.hash);
  const report = inspectCluster(snap);
  assert.ok(report.danglingRefs.some((f) => f.detail.includes('anchor')));
});

test('detects missing predecessor slot (gap in log)', () => {
  const snap = cleanSnapshot();
  const e1 = entry(1, GENESIS_HASH);
  const e3 = entry(3, 'whatever');
  snap.nodes[0].log = [e1, { ...e3, prevHash: e1.hash }];
  const report = inspectCluster(snap);
  assert.ok(report.danglingRefs.some((f) => f.detail.includes('predecessor slot 2 is absent')));
});

test('detects prevHash mismatch with predecessor slot', () => {
  const snap = cleanSnapshot();
  const other = entry(9, GENESIS_HASH, 'other');
  snap.nodes[0].log.push(other);
  snap.nodes[0].log[1] = { ...snap.nodes[0].log[1], prevHash: other.hash };
  const report = inspectCluster(snap);
  assert.ok(report.danglingRefs.some((f) => f.detail.includes('does not match')));
});

test('detects commitIndex beyond committed log', () => {
  const snap = cleanSnapshot();
  snap.nodes[1].commitIndex = 5;
  const report = inspectCluster(snap);
  assert.ok(report.danglingRefs.some((f) => f.detail.includes('commitIndex 5')));
});

test('detects lease pointing at unknown node', () => {
  const snap = cleanSnapshot();
  snap.nodes[0].lease = { leaderId: 'GHOST', until: 100 };
  const report = inspectCluster(snap);
  assert.ok(report.danglingRefs.some((f) => f.detail.includes('unknown leader GHOST')));
});

test('detects orphan reference loops unreachable from committed roots', () => {
  const snap = cleanSnapshot();
  const loopA: LogEntry = { slot: 10, ballot: { round: 7, nodeId: 'N9' }, value: cmd('la'), prevHash: 'hashB', hash: 'hashA' };
  const loopB: LogEntry = { slot: 11, ballot: { round: 7, nodeId: 'N9' }, value: cmd('lb'), prevHash: 'hashA', hash: 'hashB' };
  snap.nodes[2].log.push(loopA, loopB);
  const report = inspectCluster(snap);
  assert.ok(!report.ok);
  assert.equal(report.orphanLoops.length, 1);
  assert.ok(report.orphanLoops[0].detail.includes('cycle'));
});

test('does not flag committed chain as orphan loop', () => {
  const report = inspectCluster(cleanSnapshot());
  assert.equal(report.orphanLoops.length, 0);
});

test('detects log size data skew', () => {
  const snap = cleanSnapshot();
  const e1 = entry(1, GENESIS_HASH);
  let prev = e1.hash;
  for (let slot = 2; slot <= 12; slot++) {
    const e = entry(slot, prev);
    snap.nodes[0].log.push(e);
    prev = e.hash;
  }
  const report = inspectCluster(snap);
  assert.ok(report.dataSkew.some((f) => f.node === 'N1' && f.detail.includes('deviates')));
});

test('detects under-replicated committed slot (replication skew)', () => {
  const snap = cleanSnapshot();
  snap.nodes[1].log = snap.nodes[1].log.slice(0, 1);
  snap.nodes[1].committed = snap.nodes[1].committed.slice(0, 1);
  snap.nodes[1].commitIndex = 1;
  snap.nodes[2].log = snap.nodes[2].log.slice(0, 1);
  snap.nodes[2].committed = snap.nodes[2].committed.slice(0, 1);
  snap.nodes[2].commitIndex = 1;
  const report = inspectCluster(snap);
  assert.ok(report.dataSkew.some((f) => f.detail.includes('replicated on only 1/3')));
});

test('detects committed conflict for the same slot', () => {
  const snap = cleanSnapshot();
  const e1 = entry(1, GENESIS_HASH);
  const evil = entry(2, e1.hash, 'EVIL');
  snap.nodes[2].committed = [e1, evil];
  snap.nodes[2].log = [e1, evil];
  const report = inspectCluster(snap);
  assert.ok(!report.ok);
  assert.ok(report.committedConflicts.some((f) => f.detail.includes('slot 2')));
});

test('same value re-accepted under different ballots is not a conflict', () => {
  const snap = cleanSnapshot();
  const e1 = entry(1, GENESIS_HASH);
  const e2a = entry(2, e1.hash, 'v2');
  const e2b: LogEntry = { ...e2a, ballot: { round: 5, nodeId: 'N4' } };
  e2b.hash = hashEntry(2, e2b.ballot, e2b.value, e1.hash);
  snap.nodes[0].committed = [e1, e2a];
  snap.nodes[1].committed = [e1, e2b];
  snap.nodes[2].committed = [e1, e2b];
  const report = inspectCluster(snap);
  assert.equal(report.committedConflicts.length, 0);
});

test('inspection of a real converged cluster is clean', async () => {
  const cluster = makeCluster(5);
  await cluster.nodes[0].propose(cmd('a'));
  await cluster.nodes[0].propose(cmd('b'));
  await cluster.net.flush();
  const report = inspectCluster(snapshotFromNodes(cluster.nodes, cluster.quorum));
  assert.ok(report.ok, JSON.stringify(report, null, 2));
  cluster.monitor.assertNoLeaks();
  cluster.monitor.assertClean();
});
