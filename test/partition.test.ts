import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import type { Command } from '../src/types.ts';

const client = (data: string): Command => ({ kind: 'client', id: data, data });
const values = (c: Cluster, id: number) => c.node(id).applied.map((e) => e.value);

/**
 * Acceptance boundary:
 * 5-node cluster, network partition {n1,n2} | {n3,n4,n5}. Old leader n1 is
 * replaced by a majority-elected new leader n3, which runs Phase 1 to fill
 * uncommitted slots and safely overwrites dirty minority logs after healing.
 */
test('partition: new leader repairs holes and overwrites dirty minority log', () => {
  const c = new Cluster(5);
  const n1 = c.electLeader();
  assert.equal(n1.id, 1);

  // Slots 1..3 committed cluster-wide.
  n1.propose(client('A'));
  n1.propose(client('B'));
  n1.propose(client('C'));
  for (const n of c.nodes) assert.equal(n.commitIndex, 3);

  // Slot 4 (D) reaches only n1 and n3: uncommitted, but visible to the future
  // majority quorum -> its value must be preserved by repair.
  c.network.setLink(1, 2, false);
  c.network.setLink(1, 4, false);
  c.network.setLink(1, 5, false);
  n1.propose(client('D'));
  assert.equal(n1.commitIndex, 3, 'D is a minority write, cannot commit');
  c.network.healAll();

  // Partition: old leader n1 + n2 vs majority n3,n4,n5.
  c.partition([1, 2], [3, 4, 5]);

  // Old leader (lease still valid in its partition) writes dirty slot 5 (E)
  // onto the minority side only.
  n1.propose(client('E'));
  assert.equal(n1.commitIndex, 3, 'E is dirty: minority can never commit it');
  assert.equal(c.node(2).log.get(5)?.value.kind, 'client');

  // Majority side: n3 times out, wins Phase 1 with a higher ballot.
  const ok = c.runUntil(() => c.node(3).role === 'leader');
  assert.ok(ok, 'n3 elected by majority');
  const n3 = c.node(3);

  // Phase 1 repair: slot 4 is filled with the preserved value D and committed.
  assert.equal(n3.commitIndex, 4, 'repair commits the highest-ballot known value');
  assert.deepEqual(values(c, 3), [client('A'), client('B'), client('C'), client('D')]);

  // New leader continues under its lease: slot 5 = F on the majority side.
  const preparesBefore = c.network.count('prepare');
  n3.propose(client('F'));
  assert.equal(c.network.count('prepare'), preparesBefore, 'lease fast path, no Phase 1');
  assert.equal(n3.commitIndex, 5);

  // Old leader cannot commit anything more, and steps down once it hears the
  // higher ballot after healing.
  c.heal();
  const healed = c.runUntil(() => c.nodes.every((n) => n.commitIndex === 5));
  assert.ok(healed, 'all nodes converge after heal');

  const expected = [client('A'), client('B'), client('C'), client('D'), client('F')];
  for (const n of c.nodes) {
    assert.deepEqual(
      n.applied.map((e) => e.value),
      expected,
      `node ${n.id}: identical contiguous committed prefix`,
    );
    // Dirty minority value E is gone from every log; slot 5 is F everywhere.
    const slot5 = n.log.get(5);
    assert.deepEqual(slot5?.value, client('F'), `node ${n.id}: dirty slot overwritten`);
    assert.equal(n.role === 'leader' ? n.id : 0, n.role === 'leader' ? 3 : 0, 'single leader n3');
  }
  assert.ok(
    c.nodes.every((n) => ![...n.log.values()].some((e) => e.value.kind === 'client' && e.value.data === 'E')),
    'dirty value E fully overwritten, no leak',
  );
});

test('partition: unseen minority slots are safely overwritten as the log grows', () => {
  const c = new Cluster(5);
  const n1 = c.electLeader();
  n1.propose(client('A')); // slot 1 committed

  // Slots 2 and 3 exist only on the minority side (n1,n2).
  c.partition([1, 2], [3, 4, 5]);
  n1.propose(client('X'));
  n1.propose(client('Y'));
  assert.equal(n1.commitIndex, 1);

  // n3 becomes leader; its quorum never saw slots 2,3, so its next slots
  // reuse 2,3 and the dirty minority entries are overwritten on heal.
  c.runUntil(() => c.node(3).role === 'leader');
  const n3 = c.node(3);
  assert.equal(n3.commitIndex, 1, 'nothing new to repair: quorum saw no slots > 1');
  n3.propose(client('Z1')); // slot 2
  n3.propose(client('Z2')); // slot 3

  c.heal();
  c.runUntil(() => c.nodes.every((n) => n.commitIndex === 3));
  for (const n of c.nodes) {
    assert.deepEqual(
      n.applied.map((e) => e.value),
      [client('A'), client('Z1'), client('Z2')],
      `node ${n.id}: dirty X/Y overwritten, never committed`,
    );
  }
});

test('hole repair: new leader fills gaps between known slots with no-op', () => {
  const c = new Cluster(5);
  const n1 = c.electLeader();
  n1.propose(client('A')); // slot 1 committed everywhere

  // Slot 2 (B) lands on n1,n3 only; slot 3 (C) lands on n1,n4 only.
  c.network.setLink(1, 2, false);
  c.network.setLink(1, 4, false);
  c.network.setLink(1, 5, false);
  n1.propose(client('B'));
  c.network.setLink(1, 4, true);
  c.network.setLink(1, 3, false);
  n1.propose(client('C'));
  c.network.healAll();
  assert.equal(n1.commitIndex, 1);

  // n5 campaigns: quorum {n3,n4,n5} collectively knows slots 2 and 3.
  c.partition([1, 2], [3, 4, 5]);
  c.runUntil(() => c.node(3).role === 'leader');
  const n3 = c.node(3);
  assert.equal(n3.commitIndex, 3, 'both uncommitted slots repaired from quorum knowledge');
  assert.deepEqual(
    n3.applied.map((e) => e.value),
    [client('A'), client('B'), client('C')],
    'highest-ballot values preserved, contiguous commit',
  );

  c.heal();
  c.runUntil(() => c.nodes.every((n) => n.commitIndex === 3));
  for (const n of c.nodes) {
    assert.deepEqual(
      n.applied.map((e) => e.value),
      [client('A'), client('B'), client('C')],
      `node ${n.id}: converged`,
    );
  }
});

test('no-op fill: slot unknown to the whole quorum becomes no-op', () => {
  const c = new Cluster(5);
  const n1 = c.electLeader();
  n1.propose(client('A')); // slot 1 committed

  // Slot 2 dirty on n1 only; slot 3 (C) visible to n1,n3.
  c.network.setLink(1, 2, false);
  c.network.setLink(1, 3, false);
  c.network.setLink(1, 4, false);
  c.network.setLink(1, 5, false);
  n1.propose(client('dirty'));
  c.network.setLink(1, 3, true);
  n1.propose(client('C'));
  c.network.healAll();
  assert.equal(n1.commitIndex, 1);

  c.partition([1, 2], [3, 4, 5]);
  c.runUntil(() => c.node(3).role === 'leader');
  const n3 = c.node(3);
  // Slot 2 is unknown to the quorum -> no-op; slot 3 keeps C.
  const noop: Command = { kind: 'noop' };
  assert.deepEqual(
    n3.applied.map((e) => e.value),
    [client('A'), noop, client('C')],
    'gap filled with no-op, known value preserved',
  );

  c.heal();
  c.runUntil(() => c.nodes.every((n) => n.commitIndex === 3));
  for (const n of c.nodes) {
    assert.deepEqual(
      n.applied.map((e) => e.value),
      [client('A'), noop, client('C')],
      `node ${n.id}: converged with no-op hole fill`,
    );
  }
});

test('committed entries survive any subsequent leadership change', () => {
  const c = new Cluster(5);
  const n1 = c.electLeader();
  n1.propose(client('A'));
  n1.propose(client('B'));

  // Roll leadership through every node via successive partitions; the
  // committed prefix must remain intact and identical everywhere.
  for (const excluded of [1, 2, 3, 4]) {
    const rest = [1, 2, 3, 4, 5].filter((id) => id !== excluded);
    c.partition([excluded], rest);
    c.runUntil(() => rest.some((id) => c.node(id).role === 'leader'));
    const leader = c.nodes.find((n) => n.role === 'leader' && n.id !== excluded);
    assert.ok(leader, `leader elected without node ${excluded}`);
    leader!.propose(client(`after-${excluded}`));
    c.heal();
    c.runUntil(() => c.nodes.every((n) => n.commitIndex === leader!.commitIndex));
  }

  const head = c.node(5).applied.slice(0, 2).map((e) => e.value);
  assert.deepEqual(head, [client('A'), client('B')], 'original committed prefix preserved');
  const reference = c.node(1).applied.map((e) => e.value);
  for (const n of c.nodes) {
    assert.deepEqual(
      n.applied.map((e) => e.value),
      reference,
      `node ${n.id}: converged to identical log`,
    );
  }
});
