import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster, Client, prng } from '../src/cluster.ts';
import { NotLeaderError } from '../src/node.ts';
import { assertNoLeaks } from './helpers.ts';

test('concurrent clients: linearizable, hole-free, exactly-once commits', async () => {
  const rand = prng(42);
  const cluster = new Cluster({ size: 5, leaseMs: 5000, latency: () => rand() * 4 });
  const leader = cluster.node(3);
  await leader.campaign();

  const clients = Array.from({ length: 8 }, (_, i) => new Client(`c${i}`, cluster));
  const submissions: Array<Promise<number>> = [];
  for (const c of clients) {
    for (let k = 0; k < 5; k++) {
      submissions.push(c.submit(`op-${c.id}-${k}`, { prefer: leader }));
    }
  }
  const slots = await Promise.all(submissions);
  await cluster.settle();

  // 40 distinct commands occupy slots 1..40 with no holes and no duplicates.
  assert.equal(slots.length, 40);
  assert.deepEqual([...slots].sort((a, b) => a - b), Array.from({ length: 40 }, (_, i) => i + 1));

  const committed = leader.committedValues();
  assert.equal(committed.length, 40);
  const seen = new Set<string>();
  for (const v of committed) {
    assert.ok(v, 'no no-op fillers expected on a stable leader');
    const key = `${v.clientId}:${v.seq}`;
    assert.ok(!seen.has(key), `duplicate commit of ${key}`);
    seen.add(key);
  }

  // Every node converges to the identical committed prefix (linearizability).
  for (const n of cluster.nodes) {
    assert.deepEqual(n.committedValues(), committed, `node ${n.id} diverged`);
  }
  await assertNoLeaks(cluster);
});

test('dueling leaders: agreement holds, committed values never conflict', async () => {
  const rand = prng(7);
  const cluster = new Cluster({ size: 5, leaseMs: 5000, latency: () => rand() * 3 });
  const n1 = cluster.node(1);
  const n2 = cluster.node(2);

  // Ten rounds of concurrent campaigns and competing proposals.
  for (let round = 0; round < 10; round++) {
    await Promise.all([n1.campaign(), n2.campaign()]);
    const writes = [
      { node: n1, value: { clientId: 'n1', seq: round, data: `n1-${round}` } },
      { node: n2, value: { clientId: 'n2', seq: round, data: `n2-${round}` } },
    ];
    await Promise.all(
      writes.map((w) =>
        w.node
          .propose(w.value)
          .then(
            () => 'ok',
            (e) => (e instanceof NotLeaderError ? 'rejected' : Promise.reject(e)),
          ),
      ),
    );
  }
  // Let the current leader(s) disseminate their committed prefix.
  for (const n of cluster.leaders()) await n.broadcastCommits();
  await cluster.settle();

  // Agreement: for every slot, all nodes that committed it chose the same value.
  const reference = cluster.nodes
    .map((n) => n.committedValues())
    .reduce((a, b) => (a.length >= b.length ? a : b));
  for (const n of cluster.nodes) {
    const mine = n.committedValues();
    assert.deepEqual(
      mine,
      reference.slice(0, mine.length),
      `node ${n.id} committed prefix conflicts with the cluster`,
    );
  }
  await assertNoLeaks(cluster);
});

test('five simultaneous campaigns: at most one value decided per slot', async () => {
  const rand = prng(99);
  const cluster = new Cluster({ size: 5, leaseMs: 5000, latency: () => rand() * 3 });

  await Promise.all(cluster.nodes.map((n) => n.campaign()));

  // Every would-be leader tries to commit its own value at slot 1.
  const results = await Promise.all(
    cluster.nodes.map((n) =>
      n
        .propose({ clientId: `n${n.id}`, seq: 0, data: `claim-${n.id}` })
        .then(
          () => 'ok' as const,
          (e) => (e instanceof NotLeaderError ? ('rejected' as const) : Promise.reject(e)),
        ),
    ),
  );
  assert.ok(results.includes('ok'), 'someone must make progress');

  for (const n of cluster.leaders()) await n.broadcastCommits();
  await cluster.settle();

  const decided = new Set<string>();
  for (const n of cluster.nodes) {
    const v = n.committedValues()[0];
    if (v) decided.add(v.data);
  }
  assert.equal(decided.size, 1, `slot 1 decided more than once: ${[...decided]}`);
  await assertNoLeaks(cluster);
});

test('stale leader cannot commit after a higher promise exists', async () => {
  const cluster = new Cluster({ size: 5, leaseMs: 5000 });
  const oldLeader = cluster.node(1);
  await oldLeader.campaign();
  await oldLeader.propose({ clientId: 'c', seq: 0, data: 'before' });

  // A new leader wins Phase 1 with a higher ballot.
  const newLeader = cluster.node(4);
  assert.equal(await newLeader.campaign(), true);

  // The stale leader's write must fail and its value must never be committed.
  await assert.rejects(
    oldLeader.propose({ clientId: 'c', seq: 1, data: 'stale-write' }),
    NotLeaderError,
  );
  await cluster.settle();
  for (const n of cluster.nodes) {
    assert.ok(
      !n.committedValues().some((v) => v?.data === 'stale-write'),
      `node ${n.id} committed a stale value`,
    );
  }
  await assertNoLeaks(cluster);
});

test('commit watermark never regresses under concurrent load', async () => {
  const rand = prng(1234);
  const cluster = new Cluster({ size: 5, leaseMs: 5000, latency: () => rand() * 2 });
  const client = new Client('wm', cluster);

  const watermarks: number[][] = cluster.nodes.map(() => []);
  const sampler = setInterval(() => {
    for (const n of cluster.nodes) watermarks[n.id - 1]!.push(n.commitIndex);
  }, 1);

  try {
    await Promise.all(Array.from({ length: 30 }, (_, i) => client.submit(`op-${i}`)));
    for (const n of cluster.leaders()) await n.broadcastCommits();
    await cluster.settle();
  } finally {
    clearInterval(sampler);
  }

  for (const series of watermarks) {
    for (let i = 1; i < series.length; i++) {
      assert.ok(series[i]! >= series[i - 1]!, `watermark regressed: ${series}`);
    }
  }
  for (const n of cluster.nodes) assert.equal(n.commitIndex, 30);
  await assertNoLeaks(cluster);
});
