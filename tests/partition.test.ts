import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import type { Command } from '../src/types.ts';

const set = (key: string, value: string): Command => ({ kind: 'set', key, value });
const noop: Command = { kind: 'noop' };

/**
 * Acceptance boundary:
 * 5-node cluster. Old leader (node 0) is partitioned away with one minority
 * node; a new leader emerges in the 3-node majority, runs Phase 1 to fill
 * uncommitted slots, and safely overwrites the minority's dirty log after
 * the partition heals. Previously committed entries must survive.
 */
test('partition: old leader is replaced; new leader fills holes and repairs dirty minority logs', () => {
  const c = new Cluster(5);
  c.elect(0);
  c.submit(0, set('committed', 'A')); // slot 1: committed everywhere
  c.stabilize();
  for (const node of c.nodes) assert.equal(node.getCommitIndex(), 1);

  // --- slot 2: dirty entry accepted by the minority {0,1} only ---
  c.partition([0, 1], [2, 3, 4]);
  const dirty = c.submit(0, set('dirty', 'B'));
  assert.equal(dirty.status, 'proposed');
  c.stabilize();
  assert.equal(c.nodes[0].getCommitIndex(), 1, 'minority acceptance is not committable');
  assert.deepEqual(c.nodes[0].getEntry(2)?.command, set('dirty', 'B'), 'dirty entry sits in minority log');
  assert.deepEqual(c.nodes[1].getEntry(2)?.command, set('dirty', 'B'));

  // --- slot 3: accepted by majority {0,2,3} but blocked behind the hole ---
  c.heal();
  c.partition([1, 4], [0, 2, 3]);
  c.submit(0, set('client', 'C'));
  c.stabilize();
  assert.equal(c.nodes[0].getCommitIndex(), 1, 'commitIndex must not skip the hole at slot 2');
  assert.deepEqual(c.nodes[2].getEntry(3)?.command, set('client', 'C'));

  // --- final partition: old leader stranded; majority {2,3,4} elects node 2 ---
  c.heal();
  c.partition([0, 1], [2, 3, 4]);
  c.nodes[2].startElection();
  c.stabilize();
  assert.equal(c.nodes[2].getRole(), 'leader');
  assert.ok(c.nodes[2].getBallot().term > 1);

  // Phase 1 recovery: slot 2 is invisible to the quorum -> filled with noop;
  // slot 3 was accepted by a quorum witness -> re-proposed and committed.
  for (const id of [2, 3, 4]) {
    assert.deepEqual(
      c.committedLog(id).map((e) => e.command),
      [set('committed', 'A'), noop, set('client', 'C')],
      `node ${id}: hole filled with noop, quorum-visible value recovered`,
    );
  }

  // --- heal: minority nodes must converge and drop the dirty entry ---
  c.heal();
  c.submit(2, set('post-heal', 'D')); // triggers Commit broadcast to all
  c.stabilize();

  const expected = [set('committed', 'A'), noop, set('client', 'C'), set('post-heal', 'D')];
  for (const node of c.nodes) {
    assert.deepEqual(
      node.getCommittedLog().map((e) => e.command),
      expected,
      `node ${node.id}: fully converged committed log`,
    );
    assert.equal(node.getState().get('committed'), 'A', 'previously committed value survives');
    assert.equal(node.getState().get('dirty'), undefined, 'dirty value never applied');
  }
  assert.deepEqual(c.nodes[0].getEntry(2)?.command, noop, 'dirty slot overwritten on old leader');
  assert.deepEqual(c.nodes[1].getEntry(2)?.command, noop, 'dirty slot overwritten on minority node');
  assert.equal(c.nodes[0].getRole(), 'follower', 'old leader steps down after seeing higher term');
  assert.equal(c.inconsistencies.length, 0);
  c.assertConverged();
});

test('partition: value accepted by a minority is recovered when visible to the new quorum', () => {
  const c = new Cluster(5);
  c.elect(0);
  c.submit(0, set('a', '1')); // slot 1 committed
  c.stabilize();

  // Slot 2 is accepted by nodes 0 and 1 only.
  c.partition([0, 1], [2, 3, 4]);
  c.submit(0, set('b', '2'));
  c.stabilize();
  assert.equal(c.nodes[0].getCommitIndex(), 1);

  // Re-partition so the new majority {1,2,3} *includes* a witness of slot 2.
  c.heal();
  c.partition([0, 4], [1, 2, 3]);
  c.nodes[1].startElection();
  c.stabilize();
  assert.equal(c.nodes[1].getRole(), 'leader');

  // Phase 1 saw the accepted value at slot 2 -> must re-propose and commit it
  // (a chosen-or-choosable value is never discarded).
  c.submit(1, set('c', '3'));
  c.stabilize();
  for (const id of [1, 2, 3]) {
    assert.deepEqual(
      c.committedLog(id).map((e) => e.command),
      [set('a', '1'), set('b', '2'), set('c', '3')],
      `node ${id}: minority-accepted value recovered by Phase 1`,
    );
  }

  c.heal();
  c.submit(1, set('d', '4'));
  c.stabilize();
  c.assertConverged();
  assert.equal(c.inconsistencies.length, 0);
});

test('partition: minority side cannot elect a leader or commit anything', () => {
  const c = new Cluster(5);
  c.elect(0);
  c.submit(0, set('a', '1'));
  c.stabilize();

  c.partition([0, 1], [2, 3, 4]);
  c.nodes[1].startElection(); // can only reach node 0 and itself
  c.stabilize();
  assert.notEqual(c.nodes[1].getRole(), 'leader', 'no majority -> no leadership');

  c.submit(0, set('x', 'y'));
  c.stabilize();
  assert.equal(c.nodes[0].getCommitIndex(), 1, 'no progress without a majority');
  assert.equal(c.nodes[1].getCommitIndex(), 1);

  c.heal();
  c.assertConverged();
});

test('partition: repeated partition/heal cycles preserve the committed prefix', () => {
  const c = new Cluster(5, { seed: 42 });
  c.elect(0);
  c.submit(0, set('k0', 'v0'));
  c.stabilize();
  const committedPrefix = c.committedLog(0).map((e) => e.command);

  for (let round = 0; round < 5; round++) {
    c.partition([round % 5, (round + 1) % 5], [(round + 2) % 5, (round + 3) % 5, (round + 4) % 5]);
    // Some node in the majority becomes leader and commits.
    const majorityId = (round + 2) % 5;
    c.nodes[majorityId].startElection();
    c.stabilize();
    if (c.nodes[majorityId].getRole() === 'leader') {
      c.submit(majorityId, set(`k${round + 1}`, `v${round + 1}`));
      c.stabilize();
    }
    c.heal();
    c.stabilize();
    c.assertConverged();
    // The original committed prefix is immutable.
    assert.deepEqual(
      c.committedLog(0).map((e) => e.command).slice(0, committedPrefix.length),
      committedPrefix,
    );
  }
  assert.equal(c.inconsistencies.length, 0);
});
