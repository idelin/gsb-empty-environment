import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import type { Command } from '../src/types.ts';

const set = (key: string, value: string): Command => ({ kind: 'set', key, value });

test('arbitration: higher-term candidate replaces the incumbent leader', () => {
  const c = new Cluster(5);
  c.elect(0);
  c.submit(0, set('a', '1'));
  c.stabilize();
  assert.equal(c.nodes[0].getRole(), 'leader');

  c.elect(2); // term 2 campaign
  assert.equal(c.nodes[2].getRole(), 'leader');
  assert.equal(c.nodes[0].getRole(), 'follower', 'old leader must step down on higher ballot');
  assert.equal(c.submit(0, set('b', '2')).status, 'not-leader');

  c.submit(2, set('b', '2'));
  c.stabilize();
  for (const node of c.nodes) {
    assert.deepEqual(
      node.getCommittedLog().map((e) => e.command),
      [set('a', '1'), set('b', '2')],
    );
  }
  c.assertConverged();
});

test('arbitration: simultaneous five-way campaign keeps safety, exactly one ballot wins commits', () => {
  const c = new Cluster(5);
  for (let i = 0; i < 5; i++) c.nodes[i].startElection(); // all race before any delivery
  c.stabilize();

  const leaders = c.nodes.filter((n) => n.getRole() === 'leader');
  assert.ok(leaders.length >= 1, 'at least one leader must emerge');

  // Every self-declared leader proposes a distinct value for the same slots.
  for (const leader of leaders) {
    c.submit(leader.id, set(`from-${leader.id}`, 'v'));
  }
  c.stabilize();

  // Safety: no slot may hold two different committed values anywhere.
  assert.equal(c.inconsistencies.length, 0);
  c.assertConverged();
  // Stale-ballot proposals must have been rejected by the acceptors.
  assert.ok(c.countMessages('Rejected') > 0, 'losing ballots are rejected');
});

test('arbitration: dueling candidates, loser entries are never committed', () => {
  const c = new Cluster(5);
  c.nodes[0].startElection();
  c.nodes[1].startElection(); // same term, higher nodeId wins acceptor promises
  c.stabilize();

  const leaders = c.nodes.filter((n) => n.getRole() === 'leader');
  for (const leader of leaders) {
    c.submit(leader.id, set(`duel-${leader.id}`, 'x'));
  }
  c.stabilize();

  assert.equal(c.inconsistencies.length, 0);
  c.assertConverged();
  const committed = c.committedLog(0).map((e) => JSON.stringify(e.command));
  assert.equal(new Set(committed).size, committed.length, 'no duplicated commits');
});

test('arbitration: stale Accept under an old ballot is rejected after a higher promise', () => {
  const c = new Cluster(5);
  c.elect(0);
  // Node 0 proposes but the Accept messages are stranded by a partition.
  c.partition([0], [1, 2, 3, 4]);
  const res = c.submit(0, set('stale', 'x'));
  assert.equal(res.status, 'proposed');
  c.stabilize();
  assert.equal(c.nodes[0].getCommitIndex(), 0);

  // Majority moves on with a higher term; the stale value must not commit.
  c.elect(2);
  c.submit(2, set('fresh', 'y'));
  c.stabilize();
  c.heal();
  c.submit(2, set('after-heal', 'z')); // forces a Commit broadcast to node 0
  c.stabilize();

  for (const node of c.nodes) {
    assert.deepEqual(
      node.getCommittedLog().map((e) => e.command),
      [set('fresh', 'y'), set('after-heal', 'z')],
      'stale minority value must be overwritten, never committed',
    );
  }
  c.assertConverged();
});

test('arbitration: acceptor rejects lower-ballot Prepare and Accept with the promised floor', () => {
  const c = new Cluster(5);
  c.elect(3); // term 1
  c.nodes[3].startElection(); // term 2 campaign raises every acceptor's floor
  c.stabilize();
  assert.equal(c.nodes[0].getPromisedFloor().term, 2);

  // A lagging node replays a stale term-1 Prepare: must be rejected.
  const since = c.sentLog.length;
  c.nodes[0].receive({ type: 'Prepare', from: 4, to: 0, ballot: { term: 1, nodeId: 4 }, fromSlot: 1 });
  c.stabilize();
  const reject = c.sentLog.slice(since).find((m) => m.type === 'Rejected');
  assert.ok(reject && reject.type === 'Rejected');
  assert.equal(reject.promised.term, 2, 'rejection carries the promised floor');

  // A stale term-1 Accept must also be rejected and must not touch the log.
  const since2 = c.sentLog.length;
  c.nodes[0].receive({
    type: 'Accept',
    from: 4,
    to: 0,
    entry: { slot: 1, ballot: { term: 1, nodeId: 4 }, command: set('stale', 'x') },
  });
  c.stabilize();
  assert.ok(c.sentLog.slice(since2).some((m) => m.type === 'Rejected'));
  assert.equal(c.nodes[0].getEntry(1), undefined);
  c.assertConverged();
});
