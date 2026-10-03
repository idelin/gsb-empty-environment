import test from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import { LEASE_DURATION } from '../src/node.ts';
import { assertNoLeaks} from './helpers.ts';

/**
 * Acceptance boundary scenario:
 * A 5-node cluster is partitioned 2|3. The old leader (minority side) is
 * replaced by a new majority-elected leader, which completes Phase 1 to
 * repair uncommitted slots and safely overwrites the minority's dirty log
 * after healing. Committed entries are never lost or reordered.
 */
test('partition: old leader is replaced, holes repaired, dirty log overwritten', () => {
  const c = new Cluster(5);

  // Phase 0: node 0 leads; 'set x=1' committed on all 5 nodes.
  c.elect(0);
  c.run();
  assert.deepEqual(c.leaderIds(), [0]);
  assert.equal(c.propose(0, 'set x=1').slot, 1);
  c.run();
  for (const n of c.nodes) assert.deepEqual(n.committedValues(), ['set x=1']);

  // Phase 1: partition {0,1} | {2,3,4}. The old leader keeps its lease but
  // can never reach a majority: 'set y=2' stays uncommitted (dirty on {0,1}).
  c.partition([0, 1], [2, 3, 4]);
  assert.equal(c.propose(0, 'set y=2').ok, true, 'old leader still holds its lease');
  c.run();
  assert.equal(c.nodes[0].commitIndex, 1, 'minority leader must not commit');
  assert.equal(c.nodes[1].commitIndex, 1);
  assert.equal(c.nodes[0].entryAt(2)!.value, 'set y=2', 'dirty value sits on the minority');
  assert.ok(c.stats.dropped > 0, 'cross-partition traffic was dropped');

  // Phase 2: the majority side elects node 2, repairs via Phase 1, and
  // commits new entries. The old leader is replaced.
  c.elect(2);
  c.run();
  assert.equal(c.nodes[2].role, 'leader', 'majority elected a new leader');
  assert.equal(c.nodes[0].role, 'leader', 'old leader cannot observe the election yet');
  assert.equal(c.propose(2, 'set z=3').slot, 2);
  c.run();
  assert.deepEqual(c.nodes[2].committedValues(), ['set x=1', 'set z=3']);
  assert.deepEqual(c.nodes[3].committedValues(), ['set x=1', 'set z=3']);
  assert.deepEqual(c.nodes[4].committedValues(), ['set x=1', 'set z=3']);
  assert.equal(c.nodes[0].commitIndex, 1, 'minority side is frozen in time');
  c.checkSafety();

  // Phase 3: the old leader's lease expires; its re-election cannot reach a
  // majority, so it never commits anything new.
  c.wait(LEASE_DURATION + 10);
  c.run();
  const r = c.propose(0, 'set w=4');
  assert.equal(r.deferred, true, 'expired lease forces re-election');
  c.run();
  assert.notEqual(c.nodes[0].role, 'leader', 'minority cannot sustain leadership');
  assert.equal(c.nodes[0].commitIndex, 1);
  assert.equal(c.nodes[0].committedValues().includes('set w=4'), false);
  c.checkSafety();

  // Phase 4: heal. A fresh election lets the majority leader's Phase 1 see
  // and overwrite the dirty 'set y=2' with the committed 'set z=3'.
  c.heal();
  c.elect(2);
  c.run();
  assert.deepEqual(c.leaderIds(), [2], 'exactly one leader after healing');
  assert.equal(c.nodes[0].role, 'follower', 'old leader stepped down');
  for (const n of c.nodes) {
    assert.deepEqual(n.committedValues(), ['set x=1', 'set z=3']);
    assert.equal(n.entryAt(2)!.value, 'set z=3', 'dirty minority value overwritten');
  }
  assert.equal(c.nodes[0].committedValues().includes('set y=2'), false, 'dirty value never committed');
  assertNoLeaks(c);
});

test('partition: isolated node cannot commit or disrupt the majority', () => {
  const c = new Cluster(5);
  c.elect(0);
  c.run();
  c.isolate(0);
  // Isolated leader's proposals go nowhere.
  c.propose(0, 'lost');
  c.run();
  assert.equal(c.nodes[0].commitIndex, 0);
  // The remaining 4 nodes still have a majority and elect a new leader.
  c.elect(1);
  c.run();
  assert.equal(c.nodes[1].role, 'leader');
  assert.equal(c.nodes[0].role, 'leader', 'isolated node still believes it leads');
  c.propose(1, 'kept');
  c.run();
  for (const id of [1, 2, 3, 4]) {
    assert.deepEqual(c.nodes[id].committedValues(), ['kept']);
  }
  // Heal: the isolated node's dirty 'lost' is overwritten by 'kept'.
  c.heal();
  c.elect(1);
  c.run();
  assert.deepEqual(c.leaderIds(), [1], 'exactly one leader after healing');
  assert.equal(c.nodes[0].role, 'follower', 'isolated old leader stepped down');
  for (const n of c.nodes) assert.deepEqual(n.committedValues(), ['kept']);
  assertNoLeaks(c);
});

test('partition: dueling elections never produce divergent commits', () => {
  const c = new Cluster(5);
  // Two candidates in the same round; delivery order decides the promises.
  c.elect(0);
  c.elect(1);
  c.run();
  c.checkSafety();
  // A third election breaks any tie and makes progress.
  c.elect(4);
  c.run();
  assert.equal(c.leaderIds().length, 1);
  const leader = c.leaderIds()[0];
  c.propose(leader, 'v1');
  c.propose(leader, 'v2');
  c.run();
  for (const n of c.nodes) assert.deepEqual(n.committedValues(), ['v1', 'v2']);
  assertNoLeaks(c);
});
