import test from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import { assertNoLeaks} from './helpers.ts';

test('accept: stores the entry and acknowledges', () => {
  const c = new Cluster(5);
  c.nodes[1].handle({ type: 'Accept', from: 0, to: 1, ballot: { round: 1, nodeId: 0 }, slot: 1, value: 'A' });
  c.run();
  assert.equal(c.nodes[1].entryAt(1)!.value, 'A');
  const ack = c.delivered.find((m) => m.type === 'Accepted' && m.from === 1);
  assert.ok(ack && ack.type === 'Accepted');
  assert.equal(ack.ok, true);
  assert.equal(ack.slot, 1);
  assertNoLeaks(c);
});

test('accept: rejects a ballot lower than the promise', () => {
  const c = new Cluster(5);
  c.nodes[2].handle({ type: 'Prepare', from: 4, to: 2, ballot: { round: 6, nodeId: 4 } });
  c.run();
  c.nodes[2].handle({ type: 'Accept', from: 0, to: 2, ballot: { round: 5, nodeId: 0 }, slot: 1, value: 'X' });
  c.run();
  assert.equal(c.nodes[2].entryAt(1), undefined, 'rejected accept must not write the log');
  const nack = c.delivered.filter((m) => m.type === 'Accepted' && m.to === 0).pop();
  assert.ok(nack && nack.type === 'Accepted');
  assert.equal(nack.ok, false);
  assert.deepEqual(nack.promised, { round: 6, nodeId: 4 });
  assertNoLeaks(c);
});

test('accept: equal ballot is idempotent, higher ballot overwrites uncommitted entries', () => {
  const c = new Cluster(5);
  const n = c.nodes[1];
  n.handle({ type: 'Accept', from: 0, to: 1, ballot: { round: 1, nodeId: 0 }, slot: 1, value: 'old' });
  n.handle({ type: 'Accept', from: 0, to: 1, ballot: { round: 1, nodeId: 0 }, slot: 1, value: 'old' });
  assert.equal(n.entryAt(1)!.value, 'old');
  n.handle({ type: 'Accept', from: 3, to: 1, ballot: { round: 2, nodeId: 3 }, slot: 1, value: 'new' });
  c.run();
  assert.equal(n.entryAt(1)!.value, 'new');
  assert.equal(n.entryAt(1)!.ballot.round, 2);
  assertNoLeaks(c);
});

test('commit: frontier never advances across a hole', () => {
  const c = new Cluster(5);
  c.elect(0);
  c.run();
  const leader = c.nodes[0];
  // Slots 1 and 3 accepted by a majority, slot 2 is a hole.
  for (const from of [0, 1, 2]) {
    leader.handle({ type: 'Accepted', from, to: 0, ballot: { round: 1, nodeId: 0 }, slot: 1, ok: true });
    leader.handle({ type: 'Accepted', from, to: 0, ballot: { round: 1, nodeId: 0 }, slot: 3, ok: true });
  }
  c.run();
  assert.equal(leader.commitIndex, 1, 'hole at slot 2 blocks the frontier');
  // Fill the hole: frontier jumps to 3.
  for (const from of [0, 1, 2]) {
    leader.handle({ type: 'Accepted', from, to: 0, ballot: { round: 1, nodeId: 0 }, slot: 2, ok: true });
  }
  c.run();
  assert.equal(leader.commitIndex, 3);
  assertNoLeaks(c);
});

test('commit: follower commits only its contiguous local prefix', () => {
  const c = new Cluster(5);
  const n = c.nodes[4];
  n.handle({ type: 'Accept', from: 0, to: 4, ballot: { round: 1, nodeId: 0 }, slot: 1, value: 'A' });
  n.handle({ type: 'Accept', from: 0, to: 4, ballot: { round: 1, nodeId: 0 }, slot: 3, value: 'C' });
  n.handle({ type: 'Commit', from: 0, to: 4, ballot: { round: 1, nodeId: 0 }, commitIndex: 3 });
  c.run();
  assert.equal(n.commitIndex, 1, 'must not commit across the hole at slot 2');
  assert.deepEqual(n.committedValues(), ['A']);
  assertNoLeaks(c);
});

test('commit: notification never lowers the frontier',() => {
  const c = new Cluster(5);
  const n = c.nodes[4];
  n.handle({ type: 'Accept', from: 0, to: 4, ballot: { round: 1, nodeId: 0 }, slot: 1, value: 'A' });
  n.handle({ type: 'Accept', from: 0, to: 4, ballot: { round: 1, nodeId: 0 }, slot: 2, value: 'B' });
  n.handle({ type: 'Commit', from: 0, to: 4, ballot: { round: 1, nodeId: 0 }, commitIndex: 2 });
  n.handle({ type: 'Commit', from: 0, to: 4, ballot: { round: 1, nodeId: 0 }, commitIndex: 1 });
  c.run();
  assert.equal(n.commitIndex, 2);
  assertNoLeaks(c);
});

test('accepted: stale or foreign ballots are ignored by the leader', () => {
  const c = new Cluster(5);
  c.elect(0);
  c.run();
  const leader = c.nodes[0];
  leader.handle({ type: 'Accepted', from: 1, to: 0, ballot: { round: 0, nodeId: 0 }, slot: 1, ok: true });
  leader.handle({ type: 'Accepted', from: 2, to: 0, ballot: { round: 9, nodeId: 9 }, slot: 1, ok: true });
  c.run();
  assert.equal(leader.commitIndex, 0);
  assert.equal(leader.role, 'leader');
  assertNoLeaks(c);
});

test('accepted: nack with a higher promised ballot demotes the leader', () => {
  const c = new Cluster(5);
  c.elect(0);
  c.run();
  c.nodes[0].handle({
    type: 'Accepted', from: 3, to: 0, ballot: { round: 1, nodeId: 0 }, slot: 1, ok: false,
    promised: { round: 7, nodeId: 3 },
  });
  c.run();
  assert.equal(c.nodes[0].role, 'follower');
  assert.ok(c.nodes[0].highestRoundSeen >= 7);
  assertNoLeaks(c);
});

test('propose: non-leader rejects with a leader hint', () => {
  const c = new Cluster(5);
  c.elect(0);
  c.run();
  assert.equal(c.propose(0, 'warmup').ok, true);
  c.run();
  const r = c.propose(3, 'X');
  assert.equal(r.ok, false);
  assert.equal(r.reason, 'not-leader');
  assert.equal(r.leaderHint, 0, 'follower learned the leader from Accept traffic');
  assertNoLeaks(c);
});

test('propose: full flow commits one value on every node', () => {
  const c = new Cluster(5);
  c.elect(4);
  c.run();
  assert.equal(c.propose(4, 'cmd-1').ok, true);
  c.run();
  for (const n of c.nodes) {
    assert.deepEqual(n.committedValues(), ['cmd-1']);
  }
  assertNoLeaks(c);
});
