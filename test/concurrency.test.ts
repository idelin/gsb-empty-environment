import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Server } from '../src/server.ts';
import { ListClient } from '../src/client.ts';
import { formatTree } from '../src/tree.ts';
import { makeTree } from '../src/fixtures.ts';
import type { CommitOp } from '../src/types.ts';

test('two competing drag sessions: first commit wins, loser keeps server state', () => {
  const server = new Server(makeTree());
  const a = new ListClient(server, 'a');
  const b = new ListClient(server, 'b');

  a.startDrag('apple');
  a.dragTo({ parentId: 'grains', index: 1 });
  b.startDrag('carrot');
  b.dragTo({ parentId: 'fruits', index: 0 });

  const ra = a.commitDrag();
  assert.ok(ra && ra.ok);
  // b received the reorder broadcast mid-drag; its drag is still valid
  assert.equal(b.machine.phase, 'dragging');
  assert.deepEqual(b.tree.nodes.grains.children, ['rice', 'apple']);

  const rb = b.commitDrag(); // base version is stale now
  assert.ok(rb && !rb.ok);
  assert.match(rb.reason, /stale source version/);

  // b's failed commit discarded only its preview; everyone converges
  assert.equal(formatTree(a.tree), formatTree(server.tree));
  assert.equal(formatTree(b.tree), formatTree(server.tree));
  assert.deepEqual(b.tree.nodes.fruits.children, ['banana', 'cherry']); // carrot NOT moved
  assert.deepEqual(b.machine.ui, { focusId: null, selection: [] }); // ui restored
});

test('opId makes retries idempotent across a lost acknowledgement', () => {
  const server = new Server(makeTree());
  const a = new ListClient(server, 'a');

  let dropAck = true;
  let captured: CommitOp | null = null;
  a.transport = (op) => {
    captured = op;
    const res = server.commit('a', op);
    return dropAck ? null : res; // simulate a lost acknowledgement
  };

  a.startDrag('cherry');
  a.dragTo({ parentId: 'veggies', index: 2 });
  const r1 = a.commitDrag();
  assert.equal(r1, null); // no ack received
  assert.equal(a.machine.phase, 'awaiting');
  assert.equal(a.pendingOpIds.length, 1);
  assert.equal(server.version, 1); // server applied it exactly once

  dropAck = false;
  a.retryPending();
  assert.equal(a.machine.phase, 'idle');
  assert.equal(a.pendingOpIds.length, 0);
  assert.equal(server.version, 1); // still once
  assert.deepEqual(a.tree.nodes.veggies.children, ['carrot', 'daikon', 'cherry']);
  assert.deepEqual(a.machine.ui, { focusId: 'cherry', selection: ['cherry'] });

  // a duplicate delivery of the same opId is a server-side no-op
  const again = server.commit('a', captured!);
  assert.ok(again.ok && again.deduplicated);
  assert.equal(server.version, 1);
});

test('a failed commit preserves independent edits made afterwards', () => {
  const server = new Server(makeTree());
  const a = new ListClient(server, 'a');

  a.startDrag('banana'); // base version 0
  // independent edit while the drag is in flight
  const ins = a.insertNode('fruits', 1, { id: 'apricot', parentId: null, label: 'Apricot', children: [] });
  assert.ok(ins.ok);
  a.dragTo({ parentId: 'grains', index: 0 });

  const res = a.commitDrag();
  assert.ok(res && !res.ok);
  assert.match(res.reason, /stale source version/);

  // the independent edit survives; the rejected move does not
  assert.ok(a.tree.nodes.apricot);
  assert.deepEqual(a.tree.nodes.fruits.children, ['apple', 'apricot', 'banana', 'cherry']);
  assert.deepEqual(a.tree.nodes.grains.children, ['rice']);
  assert.deepEqual(a.machine.ui, { focusId: null, selection: [] });
});

test('deleting the drop target mid-drag terminates the drag safely', () => {
  const server = new Server(makeTree());
  const a = new ListClient(server, 'a');
  const b = new ListClient(server, 'b');

  a.startDrag('apple');
  a.dragTo({ parentId: 'veggies', index: 1 });
  assert.equal(a.machine.phase, 'dragging');

  b.removeNode('veggies'); // broadcast deletes a's drop target
  assert.equal(a.machine.phase, 'idle');
  const term = a.machine.trace.find((e) => e.op === 'terminate');
  assert.deepEqual(term, { op: 'terminate', reason: 'target-removed' });

  assert.equal(a.commitDrag(), null); // nothing left to commit
  assert.deepEqual(a.tree.nodes.fruits.children, ['apple', 'banana', 'cherry']); // apple unmoved
  assert.equal(a.tree.nodes.veggies, undefined); // removal broadcast applied
  assert.deepEqual(a.machine.ui, { focusId: null, selection: [] });
});

test('deleting the dragged node mid-drag terminates the drag safely', () => {
  const server = new Server(makeTree());
  const a = new ListClient(server, 'a');
  const b = new ListClient(server, 'b');

  a.startDrag('apple');
  a.dragTo({ parentId: 'grains', index: 0 });
  b.removeNode('fruits'); // removes apple's whole subtree

  assert.equal(a.machine.phase, 'idle');
  const term = a.machine.trace.find((e) => e.op === 'terminate');
  assert.deepEqual(term, { op: 'terminate', reason: 'dragged-node-removed' });
  assert.equal(a.tree.nodes.apple, undefined);
  assert.equal(a.commitDrag(), null);
});

test('server reorder broadcasts converge all clients', () => {
  const server = new Server(makeTree());
  const a = new ListClient(server, 'a');
  const b = new ListClient(server, 'b');
  const c = new ListClient(server, 'c');

  a.startDrag('rice');
  a.dragTo({ parentId: 'root', index: 0 });
  const r = a.commitDrag();
  assert.ok(r && r.ok);

  for (const client of [a, b, c]) {
    assert.equal(formatTree(client.tree), formatTree(server.tree));
    assert.equal(client.version, server.version);
    assert.deepEqual(client.tree.nodes[client.tree.rootId].children, ['rice', 'fruits', 'veggies', 'grains']);
  }
});
