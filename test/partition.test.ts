import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Cluster } from '../src/paxos/cluster.ts';
import { NoMajorityError } from '../src/paxos/types.ts';
import { resources } from '../src/storage/resources.ts';

const v = (s: string) => Buffer.from(s, 'utf8');

async function withCluster5<T>(fn: (c: Cluster) => Promise<T>): Promise<T> {
  const dir = await mkdtemp(join(tmpdir(), 'partition-'));
  const cluster = await Cluster.start([1, 2, 3, 4, 5], { rootDir: dir, leaseMs: 5_000 });
  try {
    return await fn(cluster);
  } finally {
    await cluster.close();
    await rm(dir, { recursive: true, force: true });
    resources.assertDrained();
  }
}

test('acceptance: partition replaces old leader, dirty minority log is safely overwritten', () =>
  withCluster5(async (c) => {
    // Phase A: node 1 leads, commits slots 1-2 to all five nodes.
    await c.node(1).proposer.propose(v('set x=1'));
    await c.node(1).proposer.propose(v('set y=2'));
    assert.equal(c.assertConverged().commitIndex, 2);

    // Phase B: partition {1,2} | {3,4,5}. Old leader keeps its lease but can
    // no longer reach a majority: slot 3 lands only on the minority {1,2}.
    c.partition([1, 2], [3, 4, 5]);
    await assert.rejects(() => c.node(1).proposer.propose(v('set z=dirty')), NoMajorityError);
    assert.equal(c.node(1).store.get(3)!.value.toString(), 'set z=dirty');
    assert.equal(c.node(3).store.has(3), false);
    assert.equal(c.node(1).commitIndex, 2, 'dirty slot must never be committed');

    // Phase C: the old lease expires, then the majority side elects node 3.
    // Phase 1 promises from {3,4,5} show no accepted value at slot 3, so the
    // new leader may reuse it.
    c.clock.advance(6_000);
    await c.node(3).proposer.propose(v('set z=9'));
    await c.node(3).proposer.propose(v('set w=8'));
    assert.equal(c.node(3).proposer.ballot!.term, 2);
    assert.equal(c.node(3).commitIndex, 4);
    for (const id of [3, 4, 5]) assert.equal(c.node(id).commitIndex, 4);
    for (const id of [1, 2]) assert.equal(c.node(id).commitIndex, 2, 'minority stays behind');

    // Phase D: heal. The next proposal backfills nodes 1,2; their dirty
    // slot 3 is overwritten by the majority-committed value.
    c.heal();
    await c.node(3).proposer.propose(v('set k=5'));

    const { commitIndex, summary } = c.assertConverged();
    assert.equal(commitIndex, 5);
    assert.equal(summary, '1:set x=1 2:set y=2 3:set z=9 4:set w=8 5:set k=5');
    for (const id of [1, 2, 3, 4, 5]) {
      const e3 = c.node(id).store.get(3)!;
      assert.equal(e3.value.toString(), 'set z=9', `node ${id} slot 3`);
      assert.equal(e3.term, 2, `node ${id} slot 3 term`);
    }
  }));

test('safety: majority-accepted value is never overwritten by a new leader', () =>
  withCluster5(async (c) => {
    await c.node(1).proposer.propose(v('a'));
    // Slot 2 is accepted by a majority {1,2,3} but the commit notice is
    // lost: use a drop filter so slot 2 reaches exactly 1,2,3 and the
    // commit broadcast for it never happens before the partition.
    c.network.setDropFilter((from, to, type, payload) => {
      if (type !== 'accept') return false;
      const entry = (payload as { entry: { index: number } }).entry;
      return entry.index === 2 && (to === 4 || to === 5);
    });
    await c.node(1).proposer.propose(v('important'));
    c.network.setDropFilter(null);
    assert.equal(c.node(3).store.get(2)!.value.toString(), 'important');

    // Partition so the new leader's quorum must include node 3 (which holds
    // the value) but cannot include node 1: {1,5} | {2,3,4}.
    c.partition([1, 5], [2, 3, 4]);
    c.clock.advance(6_000); // wait out the old lease before failover
    await c.node(2).proposer.propose(v('later'));
    c.heal();
    await c.node(2).proposer.propose(v('final'));

    // Phase 1 discovered the majority-accepted value via node 3 and
    // re-proposed it: slot 2 keeps 'important' everywhere.
    const { summary } = c.assertConverged();
    assert.equal(summary, '1:a 2:important 3:later 4:final');
    for (const id of [1, 2, 3, 4, 5]) {
      assert.equal(c.node(id).store.get(2)!.value.toString(), 'important');
    }
  }));

test('old leader cannot commit after partition even while its lease is valid', () =>
  withCluster5(async (c) => {
    await c.node(1).proposer.propose(v('before'));
    c.partition([1], [2, 3, 4, 5]);
    // Lease is still valid on node 1 (logical clock has not advanced), so it
    // attempts a direct Phase 2 commit and fails to reach a majority.
    assert.equal(c.node(1).proposer.isLeader, true);
    await assert.rejects(() => c.node(1).proposer.propose(v('impossible')), NoMajorityError);
    assert.equal(c.node(1).commitIndex, 1);

    // The majority side moves on once the old lease has expired.
    c.clock.advance(6_000);
    await c.node(4).proposer.propose(v('after'));
    c.heal();
    await c.node(4).proposer.propose(v('converge'));
    assert.equal(c.assertConverged().summary, '1:before 2:after 3:converge');
  }));
