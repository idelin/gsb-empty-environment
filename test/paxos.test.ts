import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { ENTRY_NOOP } from '../src/codec/logEntry.ts';
import { Cluster } from '../src/paxos/cluster.ts';
import { MSG_PREPARE } from '../src/paxos/types.ts';
import { resources } from '../src/storage/resources.ts';

async function withCluster<T>(ids: number[], fn: (c: Cluster) => Promise<T>): Promise<T> {
  const dir = await mkdtemp(join(tmpdir(), 'cluster-'));
  const cluster = await Cluster.start(ids, { rootDir: dir, leaseMs: 5_000 });
  try {
    return await fn(cluster);
  } finally {
    await cluster.close();
    await rm(dir, { recursive: true, force: true });
    resources.assertDrained();
  }
}

test('leader election and basic commit across 3 nodes', () =>
  withCluster([1, 2, 3], async (c) => {
    const idx = await c.node(1).proposer.propose(Buffer.from('cmd-1'));
    assert.equal(idx, 1);
    assert.equal(c.node(1).proposer.isLeader, true);
    for (const id of [1, 2, 3]) {
      assert.equal(c.node(id).commitIndex, 1);
      assert.equal(c.node(id).store.get(1)!.value.toString(), 'cmd-1');
    }
  }));

test('leader lease skips Phase 1 for consecutive entries', () =>
  withCluster([1, 2, 3], async (c) => {
    const p = c.node(1).proposer;
    await p.propose(Buffer.from('a'));
    const preparesAfterFirst = c.network.sentCount(MSG_PREPARE);
    assert.equal(preparesAfterFirst, 3); // one Phase 1 round to all peers

    await p.propose(Buffer.from('b'));
    await p.propose(Buffer.from('c'));
    assert.equal(c.network.sentCount(MSG_PREPARE), preparesAfterFirst, 'lease must bypass Phase 1');
    assert.deepEqual(c.assertConverged().summary, '1:a 2:b 3:c');
  }));

test('lease expiry forces a new Phase 1', () =>
  withCluster([1, 2, 3], async (c) => {
    const p = c.node(1).proposer;
    await p.propose(Buffer.from('a'));
    const before = c.network.sentCount(MSG_PREPARE);
    c.clock.advance(6_000); // lease (5s) expires
    assert.equal(p.isLeader, false);
    await p.propose(Buffer.from('b'));
    assert.ok(c.network.sentCount(MSG_PREPARE) > before, 'expired lease must re-run Phase 1');
    assert.equal(p.ballot!.term, 2);
  }));

test('higher ballot takes over once the previous lease expires', () =>
  withCluster([1, 2, 3], async (c) => {
    await c.node(1).proposer.propose(Buffer.from('first'));
    // While node 1's lease is live, node 2 cannot preempt it.
    await assert.rejects(() => c.node(2).proposer.propose(Buffer.from('blocked')), /gave up|prepare reached/);
    assert.equal(c.node(1).proposer.isLeader, true);
    // After the lease expires, node 2's higher ballot wins Phase 1.
    c.clock.advance(6_000);
    await c.node(2).proposer.propose(Buffer.from('second'));
    assert.equal(c.node(2).proposer.isLeader, true);
    assert.equal(c.node(2).proposer.ballot!.term, 2);
    // Node 1 regains leadership later with a still higher ballot.
    c.clock.advance(6_000);
    await c.node(1).proposer.propose(Buffer.from('third'));
    assert.deepEqual(c.assertConverged().summary, '1:first 2:second 3:third');
  }));

test('gap repair fills missing slots with no-ops (white-box)', () =>
  withCluster([1, 2, 3], async (c) => {
    // Seed a hole directly: slot 3 accepted on a majority, slot 2 missing.
    const seed = { index: 3, term: 1, nodeId: 9, type: 1, value: Buffer.from('orphan'), flags: 0, timestampMs: 0 };
    for (const id of [1, 2, 3]) {
      await c.node(id).store.accept({ ...seed });
      await c.node(id).store.setPromised({ term: 1, nodeId: 9 });
    }
    await c.node(1).proposer.propose(Buffer.from('after'));
    // Phase 1 saw slot 3 but no slot 2 -> slot 2 becomes a committed no-op,
    // slot 3 keeps its highest-ballot value, slot 4 is the new proposal.
    assert.equal(c.assertConverged().summary, '1:noop 2:noop 3:orphan 4:after');
    const log = c.node(2).committedLog();
    assert.equal(log[0].type, ENTRY_NOOP);
    assert.equal(log[1].type, ENTRY_NOOP);
    assert.equal(log[2].value.toString(), 'orphan');
    assert.equal(log[3].value.toString(), 'after');
  }));

test('committed entries survive leader restart (durability)', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'cluster-'));
  const cluster = await Cluster.start([1, 2, 3], { rootDir: dir, leaseMs: 5_000 });
  await cluster.node(1).proposer.propose(Buffer.from('durable'));
  const node1dir = join(dir, 'node-1');
  await cluster.close();

  // Reopen node 1's store directly: promised ballot, entry and commitIndex
  // must all have survived.
  const { LogStore } = await import('../src/storage/logStore.ts');
  const store = await LogStore.open(node1dir);
  assert.equal(store.get(1)!.value.toString(), 'durable');
  assert.equal(store.commitIndex, 1);
  assert.ok(store.promised.term >= 1);
  await store.close();
  await rm(dir, { recursive: true, force: true });
  resources.assertDrained();
});
