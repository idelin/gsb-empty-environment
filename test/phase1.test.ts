import test from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import { NOOP_VALUE } from '../src/types.ts';
import { assertNoLeaks} from './helpers.ts';

test('prepare: acceptor promises a higher ballot and reports its accepted log', () => {
  const c = new Cluster(5);
  // Pre-load acceptor 1 with an accepted entry and a commit frontier.
  c.nodes[1].handle({ type: 'Accept', from: 0, to: 1, ballot: { round: 1, nodeId: 0 }, slot: 1, value: 'A' });
  c.nodes[1].handle({ type: 'Commit', from: 0, to: 1, ballot: { round: 1, nodeId: 0 }, commitIndex: 1 });
  c.run();

  c.nodes[1].handle({ type: 'Prepare', from: 2, to: 1, ballot: { round: 2, nodeId: 2 } });
  c.run();

  assert.equal(c.nodes[1].promised.round, 2);
  assert.equal(c.nodes[1].promised.nodeId, 2);
  const reply = c.delivered.find((m) => m.type === 'Promise' && m.from === 1 && m.to === 2);
  assert.ok(reply && reply.type === 'Promise');
  assert.equal(reply.ok, true);
  assert.deepEqual(reply.entries, [[1, { ballot: { round: 1, nodeId: 0 }, value: 'A' }]]);
  assert.equal(reply.commitIndex, 1);
  assertNoLeaks(c);
});

test('prepare: acceptor rejects a lower-or-equal ballot and reports its promised ballot', () => {
  const c = new Cluster(5);
  c.nodes[3].handle({ type: 'Prepare', from: 0, to: 3, ballot: { round: 5, nodeId: 0 } });
  c.run();
  c.nodes[3].handle({ type: 'Prepare', from: 1, to: 3, ballot: { round: 4, nodeId: 1 } });
  c.run();
  const nack = c.delivered.filter((m) => m.type === 'Promise' && m.to === 1).pop();
  assert.ok(nack && nack.type === 'Promise');
  assert.equal(nack.ok, false);
  assert.deepEqual(nack.promised, { round: 5, nodeId: 0 });
  // promise monotonicity: never decreases
  assert.equal(c.nodes[3].promised.round, 5);
  assertNoLeaks(c);
});

test('promise: candidate becomes leader exactly at a majority of promises', () => {
  const c = new Cluster(5);
  c.elect(0);
  // Deliver promises one at a time by ticking individual events.
  while (c.pendingEvents > 0 && c.nodes[0].role === 'candidate') c.tick();
  assert.equal(c.nodes[0].role, 'leader'); // self + 2 of 4 peers = 3 = majority
  assert.equal(c.nodes[0].knownLeader, 0);
  assert.ok(c.nodes[0].leaseUntil > c.now);
  c.run();
  assertNoLeaks(c);
});

test('promise: candidate steps down when a nack carries a higher promised ballot', () => {
  const c = new Cluster(5);
  // Nodes 2,3,4 already promised a very high ballot to someone else, so the
  // candidate cannot reach a majority and nacks carry the higher ballot.
  for (const id of [2, 3, 4]) {
    c.nodes[id].handle({ type: 'Prepare', from: 3, to: id, ballot: { round: 9, nodeId: 3 } });
  }
  c.run();
  c.elect(0); // round 1: nack from node 4 carries promised 9@3
  c.run();
  assert.equal(c.nodes[0].role, 'follower');
  assert.ok(c.nodes[0].highestRoundSeen >= 9);
  assertNoLeaks(c);
});

test('promise: stale replies from an old ballot are ignored', () => {
  const c = new Cluster(5);
  c.elect(0); // round 1
  c.run();
  assert.equal(c.nodes[0].role, 'leader');
  // A stray promise from a hypothetical older election must not corrupt state.
  c.nodes[0].handle({
    type: 'Promise', from: 1, to: 0, ballot: { round: 0, nodeId: 0 }, ok: true, entries: [], commitIndex: 0,
  });
  c.run();
  assert.equal(c.nodes[0].role, 'leader');
  assertNoLeaks(c);
});

test('prepare: a higher prepare demotes a sitting leader', () => {
  const c = new Cluster(5);
  c.elect(0);
  c.run();
  assert.equal(c.nodes[0].role, 'leader');
  c.nodes[0].handle({ type: 'Prepare', from: 4, to: 0, ballot: { round: 8, nodeId: 4 } });
  c.run();
  assert.equal(c.nodes[0].role, 'follower');
  assert.equal(c.nodes[0].promised.round, 8);
  assertNoLeaks(c);
});

test('election: empty cluster log elects leader with no repair traffic', () => {
  const c = new Cluster(5);
  c.elect(2);
  c.run();
  assert.deepEqual(c.leaderIds(), [2]);
  assert.equal(c.stats.Accept, 0, 'no slots to repair => no Accept messages');
  assert.equal(c.nodes[2].commitIndex, 0);
  assert.equal(NOOP_VALUE.length > 0, true);
  assertNoLeaks(c);
});
