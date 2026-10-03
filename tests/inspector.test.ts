import { test } from 'node:test';
import assert from 'node:assert/strict';
import { inspect } from '../src/inspector.ts';
import type { ClusterSnapshot, SnapshotNode } from '../src/cluster.ts';
import { GENESIS_PARENT } from '../src/types.ts';

function node(partial: Partial<SnapshotNode> & { id: string }): SnapshotNode {
  return {
    role: 'follower',
    promised: null,
    knownLeader: null,
    leaseExpiry: 0,
    commitIndex: -1,
    log: [],
    ...partial,
  };
}

function entry(
  slot: number,
  value: string,
  opts: { committed?: boolean; parentSlot?: number; term?: number } = {},
) {
  return {
    slot,
    value,
    ballot: { term: opts.term ?? 1, nodeId: 'A' },
    parentSlot: opts.parentSlot ?? (slot === 0 ? GENESIS_PARENT : slot - 1),
    committed: opts.committed ?? false,
  };
}

function snap(nodes: SnapshotNode[], time = 0): ClusterSnapshot {
  return {
    time,
    majority: 3,
    nodeIds: nodes.map((n) => n.id),
    nodes,
  };
}

test('empty cluster snapshot is clean', () => {
  const report = inspect(snap([]));
  assert.ok(report.ok);
  assert.equal(report.skew.length, 0);
});

test('healthy converged cluster passes with ok=true', () => {
  const nodes = ['A', 'B', 'C', 'D', 'E'].map((id) =>
    node({
      id,
      commitIndex: 2,
      log: [
        entry(0, 'x', { committed: true }),
        entry(1, 'y', { committed: true }),
        entry(2, 'z', { committed: true }),
      ],
    }),
  );
  nodes[0].role = 'leader';
  nodes[0].leaseExpiry = 100;
  nodes[0].promised = { term: 1, nodeId: 'A' };
  const report = inspect(snap(nodes));
  assert.ok(report.ok, JSON.stringify(report, null, 2));
});

test('detects dangling parent reference', () => {
  const report = inspect(
    snap([node({ id: 'A', log: [entry(0, 'x'), entry(2, 'z', { parentSlot: 1 })] })]),
  );
  assert.equal(report.danglingRefs.length, 1);
  assert.equal(report.danglingRefs[0].kind, 'parent-slot');
  assert.ok(!report.ok);
});

test('detects non-contiguous parent even when parent exists', () => {
  const report = inspect(
    snap([
      node({
        id: 'A',
        log: [entry(0, 'x'), entry(1, 'y'), entry(2, 'z', { parentSlot: 0 })],
      }),
    ]),
  );
  assert.ok(report.danglingRefs.some((d) => d.kind === 'non-contiguous-parent'));
});

test('detects dangling commit pointer', () => {
  const report = inspect(
    snap([node({ id: 'A', commitIndex: 5, log: [entry(0, 'x', { committed: true })] })]),
  );
  assert.ok(report.danglingRefs.some((d) => d.kind === 'commit-pointer'));
});

test('detects dangling leader reference to unknown node', () => {
  const report = inspect(snap([node({ id: 'A', knownLeader: 'Z' })]));
  assert.ok(report.danglingRefs.some((d) => d.kind === 'leader-ref'));
});

test('detects expired lease on a node still claiming leadership', () => {
  const report = inspect(
    snap([node({ id: 'A', role: 'leader', leaseExpiry: 10 })], 50),
  );
  assert.ok(report.danglingRefs.some((d) => d.kind === 'lease'));
});

test('valid lease is not flagged (boundary: leaseExpiry == now)', () => {
  const report = inspect(
    snap([node({ id: 'A', role: 'leader', leaseExpiry: 50 })], 50),
  );
  assert.equal(report.danglingRefs.length, 0);
});

test('detects orphan cycle in log parent chain', () => {
  const report = inspect(
    snap([
      node({
        id: 'A',
        log: [
          entry(0, 'x', { parentSlot: 2 }),
          entry(1, 'y', { parentSlot: 0 }),
          entry(2, 'z', { parentSlot: 1 }),
        ],
      }),
    ]),
  );
  assert.equal(report.orphanCycles.length, 1);
  assert.equal(report.orphanCycles[0].kind, 'log-chain');
  assert.equal(report.orphanCycles[0].members.length, 3);
});

test('detects orphan leadership belief cycle with no real leader', () => {
  const report = inspect(
    snap([
      node({ id: 'A', knownLeader: 'B' }),
      node({ id: 'B', knownLeader: 'C' }),
      node({ id: 'C', knownLeader: 'A' }),
    ]),
  );
  assert.equal(report.orphanCycles.length, 1);
  assert.equal(report.orphanCycles[0].kind, 'leadership');
});

test('leadership cycle containing the real leader is not orphaned', () => {
  const report = inspect(
    snap([
      node({ id: 'A', role: 'leader', leaseExpiry: 100, knownLeader: 'A' }),
      node({ id: 'B', knownLeader: 'A' }),
    ]),
  );
  assert.equal(report.orphanCycles.length, 0);
});

test('skew: boundary values are inclusive-safe', () => {
  const mk = (id: string, n: number) =>
    node({
      id,
      log: Array.from({ length: n }, (_, i) => entry(i, `v${i}`)),
    });
  // ratio exactly 2.0 and lag exactly 3 -> not flagged (thresholds are strict >)
  let report = inspect(snap([mk('A', 6), mk('B', 3)]), );
  assert.equal(report.skew.filter((s) => s.kind === 'log-size').length, 0);
  // ratio 7/3 > 2 -> flagged
  report = inspect(snap([mk('A', 7), mk('B', 3)]));
  assert.ok(report.skew.some((s) => s.kind === 'log-size'));
  // lag 4 > 3 -> flagged even with ratio <= 2 impossible here; use abs lag
  report = inspect(snap([mk('A', 5), mk('B', 1)]));
  assert.ok(report.skew.some((s) => s.kind === 'log-size'));
});

test('skew: committed slot below majority coverage is critical', () => {
  const nodes = ['A', 'B', 'C', 'D', 'E'].map((id) => node({ id }));
  nodes[0].log = [entry(0, 'x', { committed: true })];
  nodes[1].log = [entry(0, 'x', { committed: true })];
  const report = inspect(snap(nodes));
  assert.ok(report.skew.some((s) => s.kind === 'commit-coverage'));
});

test('skew: dirty entries concentrated on one node', () => {
  const clean = entry(0, 'x', { committed: true });
  const report = inspect(
    snap([
      node({ id: 'A', log: [clean, entry(1, 'd1'), entry(2, 'd2'), entry(3, 'd3')] }),
      node({ id: 'B', log: [clean, entry(1, 'd9')] }),
      node({ id: 'C', log: [clean] }),
    ]),
  );
  assert.ok(report.skew.some((s) => s.kind === 'dirty-concentration'));
});

test('invariant: state machine safety violation is caught offline', () => {
  const report = inspect(
    snap([
      node({ id: 'A', commitIndex: 0, log: [entry(0, 'x', { committed: true })] }),
      node({ id: 'B', commitIndex: 0, log: [entry(0, 'y', { committed: true })] }),
    ]),
  );
  assert.ok(
    report.invariantViolations.some((v) => v.invariant.startsWith('I3')),
  );
  assert.ok(!report.ok);
});

test('invariant: log matching violation is caught offline', () => {
  const report = inspect(
    snap([
      node({ id: 'A', log: [entry(0, 'x', { term: 1 })] }),
      node({ id: 'B', log: [entry(0, 'y', { term: 1 })] }),
    ]),
  );
  assert.ok(
    report.invariantViolations.some((v) => v.invariant.startsWith('I2')),
  );
});

test('invariant: gap in committed prefix is caught offline', () => {
  const report = inspect(
    snap([
      node({
        id: 'A',
        commitIndex: 1,
        log: [entry(0, 'x'), entry(1, 'y', { committed: true })],
      }),
    ]),
  );
  assert.ok(
    report.invariantViolations.some((v) => v.invariant.startsWith('I4')),
  );
});

test('invariant: duplicate leadership claim of one ballot is caught', () => {
  const report = inspect(snap([node({ id: 'A' })]), {
    leadershipHistory: [
      { term: 3, nodeId: 'A' },
      { term: 3, nodeId: 'A' },
    ],
  });
  assert.ok(
    report.invariantViolations.some((v) => v.invariant.startsWith('I1')),
  );
});

test('invariant: two live leaders sharing one ballot are caught', () => {
  const promised = { term: 2, nodeId: 'A' };
  const report = inspect(
    snap([
      node({ id: 'A', role: 'leader', leaseExpiry: 100, promised }),
      node({ id: 'B', role: 'leader', leaseExpiry: 100, promised }),
    ]),
  );
  assert.ok(
    report.invariantViolations.some((v) => v.invariant.startsWith('I1')),
  );
});

test('invariant: leader completeness violation is caught offline', () => {
  const report = inspect(
    snap([
      node({
        id: 'A',
        role: 'leader',
        leaseExpiry: 100,
        promised: { term: 5, nodeId: 'A' },
        log: [],
      }),
      node({
        id: 'B',
        commitIndex: 0,
        log: [entry(0, 'committed', { committed: true })],
      }),
      node({
        id: 'C',
        commitIndex: 0,
        log: [entry(0, 'committed', { committed: true })],
      }),
      node({
        id: 'D',
        commitIndex: 0,
        log: [entry(0, 'committed', { committed: true })],
      }),
    ]),
  );
  assert.ok(
    report.invariantViolations.some((v) => v.invariant.startsWith('I5')),
  );
});
