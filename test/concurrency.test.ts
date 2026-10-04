import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { Cluster } from '../src/cluster.ts';
import { EntryType } from '../src/codec.ts';

const OPTS = { leaseMs: 60_000, rpcTimeoutMs: 200 };

test('50 concurrent proposals commit exactly once, contiguously, on every node', async () => {
  // fsync is disabled here only to keep 250 replicated appends well within
  // the RPC timeout; correctness tests in paxos.test.ts run with fsync on.
  const cluster = Cluster.create(5, { leaseMs: 60_000, rpcTimeoutMs: 2000, fsync: false });
  try {
    const leader = cluster.node(1);
    await leader.campaign();
    const N = 50;
    const indices = await Promise.all(
      Array.from({ length: N }, (_, i) => leader.propose(Buffer.from(`v${i + 1}`))),
    );
    assert.deepEqual([...indices].sort((a, b) => a - b), Array.from({ length: N }, (_, i) => i + 1));
    await cluster.settle();
    for (const n of cluster.nodes) {
      assert.equal(n.commitIndex, N, `node ${n.id} commitIndex`);
      for (let i = 1; i <= N; i++) {
        const v = n.entryAt(i);
        assert.ok(v, `node ${n.id} hole at slot ${i}`);
        assert.equal(v.type, EntryType.Data);
        assert.equal(v.payload.toString(), `v${i}`);
      }
    }
  } finally {
    await cluster.stop();
  }
});

test('concurrent campaigns never produce two committed values for one slot', async () => {
  const cluster = Cluster.create(5, OPTS);
  try {
    const results = await Promise.all(cluster.nodes.map((n) => n.campaign().catch(() => false)));
    assert.ok(results.some(Boolean), 'at least one campaign must win a quorum');
    let committedSlot = -1;
    for (const n of cluster.nodes) {
      if (n.currentRole !== 'leader') continue;
      try {
        committedSlot = await n.propose(Buffer.from('contended'));
        break;
      } catch {
        // Lost the ballot race; try the next claimant.
      }
    }
    assert.ok(committedSlot > 0, 'some leader must be able to commit');
    await cluster.settle();
    for (const n of cluster.nodes) {
      const v = n.entryAt(committedSlot);
      if (n.commitIndex >= committedSlot) {
        assert.ok(v, `node ${n.id} missing committed slot ${committedSlot}`);
        assert.equal(v.payload.toString(), 'contended');
      }
    }
  } finally {
    await cluster.stop();
  }
});

test('no timer/listener/handle leaks after concurrent load and stop', async () => {
  const timersOf = () =>
    process.getActiveResourcesInfo().filter((r) => r === 'Timeout' || r === 'Immediate').length;
  const before = timersOf();
  const cluster = Cluster.create(5, { leaseMs: 60_000, rpcTimeoutMs: 2000, fsync: false });
  const leader = cluster.node(1);
  await leader.campaign();
  await Promise.all(Array.from({ length: 20 }, (_, i) => leader.propose(Buffer.from(`L${i}`))));
  await cluster.settle();
  await cluster.stop();
  for (const n of cluster.nodes) {
    assert.equal(n.activeTimers, 0, `node ${n.id} leaked a timer`);
  }
  assert.equal(cluster.transport.pending, 0, 'transport queue not drained');
  assert.ok(timersOf() <= before, 'process-level timers leaked');
});
