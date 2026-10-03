import { test } from 'node:test';
import assert from 'node:assert/strict';
import { PaxosNode } from '../src/node.ts';
import type { Accept, Message, Prepare } from '../src/types.ts';
import { IDS } from './helpers.ts';

function makeNode(id = 'n2', leaseDuration = 0): PaxosNode {
  return new PaxosNode({ id, peers: IDS, electionTimeout: 10, leaseDuration, heartbeatInterval: 5 });
}

function acceptMsg(slot: number, value: string, ballot: { round: number; node: string }, commitIndex = -1): Accept {
  return { type: 'Accept', from: ballot.node, to: 'n2', ballot, slot, value, commitIndex };
}

function prepareMsg(ballot: { round: number; node: string }): Prepare {
  return { type: 'Prepare', from: ballot.node, to: 'n2', ballot };
}

test('Promise carries previously accepted entries (Phase 1 report)', () => {
  const node = makeNode();
  node.tick(1);
  node.receive(acceptMsg(0, 'a', { round: 1, node: 'n1' }));
  const accepted = node.drainOutbox().filter((m: Message) => m.type === 'Accepted');
  assert.equal(accepted.length, 1, 'acceptor must ack the Accept');
  assert.equal(node.entryAt(0)?.value, 'a');

  node.receive(prepareMsg({ round: 2, node: 'n3' }));
  const promise = node.drainOutbox().find((m: Message) => m.type === 'Promise');
  assert.ok(promise && promise.type === 'Promise');
  assert.deepEqual(promise.accepted, [{ slot: 0, ballot: { round: 1, node: 'n1' }, value: 'a' }]);
});

test('stale Prepare below promised ballot is rejected', () => {
  const node = makeNode();
  node.tick(1);
  node.receive(prepareMsg({ round: 2, node: 'n3' }));
  node.drainOutbox();
  node.receive(prepareMsg({ round: 1, node: 'n1' }));
  assert.deepEqual(node.drainOutbox(), [], 'no Promise for a stale ballot');
  assert.deepEqual(node.promised, { round: 2, node: 'n3' });
});

test('Accept below promised ballot is rejected', () => {
  const node = makeNode();
  node.tick(1);
  node.receive(prepareMsg({ round: 2, node: 'n3' }));
  node.drainOutbox();
  node.receive(acceptMsg(1, 'x', { round: 1, node: 'n1' }));
  assert.deepEqual(node.drainOutbox(), [], 'no Accepted for a stale ballot');
  assert.equal(node.entryAt(1), undefined);
});

test('uncommitted entry is overwritten by a higher-ballot Accept', () => {
  const node = makeNode();
  node.tick(1);
  node.receive(acceptMsg(0, 'a', { round: 1, node: 'n1' }));
  node.receive(acceptMsg(0, 'b', { round: 2, node: 'n3' }));
  assert.equal(node.entryAt(0)?.value, 'b');
});

test('committed slot is immutable even for a higher-ballot Accept', () => {
  const node = makeNode();
  node.tick(1);
  node.receive(acceptMsg(0, 'a', { round: 1, node: 'n1' }, 0));
  assert.deepEqual(node.applied, ['a']);
  assert.equal(node.commitIndex, 0);
  node.receive(acceptMsg(0, 'EVIL', { round: 9, node: 'n5' }, 0));
  assert.equal(node.entryAt(0)?.value, 'a', 'committed entry must not be overwritten');
  assert.deepEqual(node.applied, ['a']);
});

test('granted lease blocks foreign Prepare until it expires', () => {
  const node = makeNode('n2', 30);
  node.tick(1);
  node.receive(acceptMsg(0, 'a', { round: 1, node: 'n1' })); // grants lease to n1 until t=31
  node.drainOutbox();
  node.tick(5);
  node.receive(prepareMsg({ round: 2, node: 'n3' }));
  assert.deepEqual(node.drainOutbox(), [], 'lease must suppress foreign Prepare');

  for (let t = 6; t <= 31; t++) node.tick(t); // lease expires at t=31
  node.drainOutbox(); // drop the node's own candidacy traffic
  node.receive(prepareMsg({ round: 5, node: 'n3' }));
  const out = node.drainOutbox();
  assert.ok(out.some((m) => m.type === 'Promise' && m.to === 'n3'), 'Promise flows after lease expiry');
});

test('acceptor log may hold holes; commit never skips them', () => {
  const node = makeNode();
  node.tick(1);
  node.receive(acceptMsg(0, 'a', { round: 1, node: 'n1' }));
  node.receive(acceptMsg(2, 'c', { round: 1, node: 'n1' }, 2)); // leader claims commitIndex 2
  assert.equal(node.commitIndex, 0, 'hole at slot 1 blocks commit');
  assert.deepEqual(node.applied, ['a']);
  node.receive(acceptMsg(1, 'b', { round: 1, node: 'n1' }, 2)); // hole repaired
  assert.deepEqual(node.applied, ['a', 'b', 'c']);
  assert.equal(node.commitIndex, 2);
});
