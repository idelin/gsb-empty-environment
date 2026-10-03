import test from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import { NOOP_VALUE} from '../src/types.ts';
import { assertNoLeaks} from './helpers.ts';

/**
 * Log repair after leader change:
 *  - holes in the quorum's view are filled with NOOP so the prefix commits;
 *  - committed values are always preserved (quorum intersection);
 *  - uncommitted "dirty" values on a minority are safely overwritten.
 */
test('repair: new leader fills holes with NOOP and keeps quorum values', () => {
  const c = new Cluster(5);
  c.elect(0);
  c.run();
  c.propose(0, 'A'); // slot 1: committed everywhere
  c.run();
  for (const n of c.nodes) assert.deepEqual(n.committedValues(), ['A']);

  // Slot 2 ('B') reaches only {0,1}: dirty minority value.
  c.blockLink(0, 2); c.blockLink(0, 3); c.blockLink(0, 4);
  c.propose(0, 'B');
  c.run();
  assert.equal(c.nodes[0].commitIndex, 1, 'minority accept must not commit');

  // Slot 3 ('D') reaches {0,1,2}: a hole at slot 2 on the majority side.
  c.heal();
  c.blockLink(0, 3); c.blockLink(0, 4);
  c.propose(0, 'D');
  c.run();
  assert.equal(c.nodes[2].entryAt(3)!.value, 'D');
  assert.equal(c.nodes[2].entryAt(2), undefined);

  // Node 0 is partitioned away; node 2 is elected by the majority {2,3,4}.
  c.partition([0, 1], [2, 3, 4]);
  c.elect(2);
  c.run();
  assert.equal(c.nodes[2].role, 'leader');
  assert.equal(c.nodes[0].role, 'leader', 'partitioned old leader cannot observe the election');
  assert.deepEqual(
    c.nodes[2].committedValues(),
    ['A', NOOP_VALUE, 'D'],
    'hole at slot 2 filled with NOOP, quorum value D kept',
  );
  assert.deepEqual(c.nodes[3].committedValues(), ['A', NOOP_VALUE, 'D']);
  assert.deepEqual(c.nodes[4].committedValues(), ['A', NOOP_VALUE, 'D']);
  c.checkSafety();

  // Heal: dirty 'B' on {0,1} is overwritten by the committed NOOP.
  c.heal();
  c.elect(2);
  c.run();
  for (const n of c.nodes) {
    assert.deepEqual(n.committedValues(), ['A', NOOP_VALUE, 'D']);
    assert.equal(n.entryAt(2)!.value, NOOP_VALUE, 'dirty minority value overwritten');
  }
  assertNoLeaks(c);
});

test('repair: committed values survive any subsequent election',() => {
  const c = new Cluster(5);
  c.elect(0);
  c.run();
  c.propose(0, 'committed');
  c.run();
  // Every node is elected in turn; the committed prefix must never change.
  for (const id of [1, 2, 3, 4, 0]) {
    c.elect(id);
    c.run();
    for (const n of c.nodes) {
      assert.deepEqual(n.committedValues(), ['committed'], `committed entry lost after electing ${id}`);
    }
    c.checkSafety();
  }
  assertNoLeaks(c);
});

test('repair: uncommitted values may be legitimately replaced', () => {
  const c = new Cluster(5);
  c.elect(0);
  c.run();
  // 'X' is accepted by the minority {0,1} only: never committed.
  c.partition([0, 1], [2, 3, 4]);
  c.propose(0, 'X');
  c.run();
  // The majority {2,3,4} never saw 'X'; the new leader commits 'Y' at slot 1.
  c.elect(2);
  c.run();
  c.propose(2, 'Y');
  c.run();
  assert.deepEqual(c.nodes[2].committedValues(), ['Y']);
  // After healing, the minority's dirty 'X' is overwritten by committed 'Y'.
  c.heal();
  c.elect(2);
  c.run();
  for (const n of c.nodes) {
    assert.deepEqual(n.committedValues(), ['Y']);
  }
  assert.equal(c.nodes[0].entryAt(1)!.value, 'Y');
  c.checkSafety();
  assertNoLeaks(c);
});
