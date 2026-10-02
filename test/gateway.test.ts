import test from 'node:test';
import assert from 'node:assert/strict';
import { LeaseRouterGateway } from '../src/gateway.ts';
import { StorageNode } from '../src/node.ts';
import { midpointOf } from '../src/keyrange.ts';

function makeGateway(latencyMs = 60) {
  const gateway = new LeaseRouterGateway(1000);
  const node1 = new StorageNode('node-1', latencyMs);
  const node2 = new StorageNode('node-2', latencyMs);
  gateway.addNode(node1);
  gateway.addNode(node2);
  return { gateway, node1, node2 };
}

test('midpoint of [A-Z) is M', () => {
  assert.equal(midpointOf({ start: 'A', end: 'Z' }), 'M');
});

test('oversized [A-Z) partition splits at M into two independently leased ranges', () => {
  const { gateway } = makeGateway();
  const parentLease = gateway.bootstrap({ start: 'A', end: 'Z' }, 'node-1');

  // Grow the [A-Z) partition past the threshold -> automatic split at 'M'.
  let event = null;
  for (let i = 0; i < 1001; i += 1) {
    event = gateway.recordWrite('G' + String(i).padStart(4, '0')) ?? event;
  }
  assert.ok(event, 'split should have been triggered');
  assert.equal(event.parent.leaseId, parentLease.id);

  // Two child ranges with independent leases on different nodes.
  const event2 = gateway.splitLog[0];
  assert.equal(event2.children.length, 2);
  const [left, right] = event2.children;
  assert.deepEqual(left.range, { start: 'A', end: 'M' });
  assert.deepEqual(right.range, { start: 'M', end: 'Z' });
  assert.notEqual(left.leaseId, right.leaseId);

  // Grant the right child to node-2 explicitly (lease inheritance check below
  // uses a fresh gateway to control node assignment deterministically).
  assert.equal(parentLease.status, 'revoked');
  assert.equal(left.epoch, parentLease.epoch + 1);
  assert.equal(right.epoch, parentLease.epoch + 1);
});

test('split grants [A-M) to node-1 and [M-Z) to node-2 with lease inheritance', () => {
  const { gateway } = makeGateway();
  const parentLease = gateway.bootstrap({ start: 'A', end: 'Z' }, 'node-1');

  const event = gateway.splitAt('M', 'node-1', 'node-2');

  assert.equal(parentLease.status, 'revoked');
  assert.deepEqual(
    gateway.table().map((r) => [r.range, r.nodeId, r.epoch]),
    [
      ['[A-M)', 'node-1', 2],
      ['[M-Z)', 'node-2', 2],
    ],
  );
  const [left, right] = event.children;
  assert.equal(left.nodeId, 'node-1');
  assert.equal(right.nodeId, 'node-2');
  assert.notEqual(left.leaseId, right.leaseId);
  assert.equal(event.parent.epoch, 1);
});

test("queries 'Apple' and 'Zoo' route to different nodes and run concurrently", async () => {
  const { gateway, node1, node2 } = makeGateway(80);
  gateway.bootstrap({ start: 'A', end: 'Z' }, 'node-1');
  gateway.splitAt('M', 'node-1', 'node-2');

  const started = Date.now();
  const [apple, zoo] = await Promise.all([gateway.query('Apple'), gateway.query('Zoo')]);
  const elapsed = Date.now() - started;

  assert.equal(apple.nodeId, 'node-1');
  assert.equal(zoo.nodeId, 'node-2');
  assert.deepEqual(node1.processedKeys, ['Apple']);
  assert.deepEqual(node2.processedKeys, ['Zoo']);

  // Non-blocking: two 80ms queries on independent nodes finish well under 160ms.
  assert.ok(elapsed < 150, `expected concurrent execution, took ${elapsed}ms`);
  // Windows of the two queries overlap.
  assert.ok(apple.startedAt < zoo.finishedAt && zoo.startedAt < apple.finishedAt);
});

test("boundary key 'M' belongs to [M-Z); out-of-range keys are rejected", () => {
  const { gateway } = makeGateway();
  gateway.bootstrap({ start: 'A', end: 'Z' }, 'node-1');
  gateway.splitAt('M', 'node-1', 'node-2');

  assert.equal(gateway.route('M').node.nodeId, 'node-2');
  assert.equal(gateway.route('Lzz').node.nodeId, 'node-1');
  // Open tail: keys at/after 'Z' still belong to the last partition [M-Z).
  assert.equal(gateway.route('Z').node.nodeId, 'node-2');
  assert.equal(gateway.route('Zoo').node.nodeId, 'node-2');
  // Keys below the first partition's start have no owner.
  assert.throws(() => gateway.route('0'), /no partition covers key/);
});

test('stale parent lease cannot be used after split', () => {
  const { gateway } = makeGateway();
  const parentLease = gateway.bootstrap({ start: 'A', end: 'Z' }, 'node-1');
  gateway.splitAt('M', 'node-1', 'node-2');
  assert.throws(() => gateway.leaseManager.assertUsable(parentLease), /revoked/);
});

test('recursive split keeps ranges disjoint and leases independent', () => {
  const { gateway } = makeGateway();
  gateway.bootstrap({ start: 'A', end: 'Z' }, 'node-1');
  gateway.splitAt('M', 'node-1', 'node-2');
  gateway.splitAt('G', 'node-1', 'node-2'); // split [A-M) at 'G'

  assert.deepEqual(
    gateway.table().map((r) => r.range),
    ['[A-G)', '[G-M)', '[M-Z)'],
  );
  const leaseIds = gateway.table().map((r) => r.leaseId);
  assert.equal(new Set(leaseIds).size, 3);
  assert.equal(gateway.route('Apple').node.nodeId, 'node-1');
  assert.equal(gateway.route('Hello').node.nodeId, 'node-2');
  assert.equal(gateway.route('Zoo').node.nodeId, 'node-2');
});
