import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  hashKey,
  LocalityAwareRouter,
  TopologyError,
  type NodeInfo,
} from '../src/shard.ts';

function makeNode(region: string, zone: string, n: number, weight = 1): NodeInfo {
  return { id: `${region}-${zone}-n${n}`, region, zone, weight, healthy: true };
}

/** 2 regions x 2 zones x 3 nodes = 12-node topology. */
async function buildRouter(shardCount = 256, replicaCount = 3) {
  const router = new LocalityAwareRouter({ shardCount, replicaCount });
  const nodes: NodeInfo[] = [];
  for (const region of ['r1', 'r2']) {
    for (const zone of ['z1', 'z2']) {
      for (let n = 0; n < 3; n++) nodes.push(makeNode(region, zone, n));
    }
  }
  await Promise.all(nodes.map((n) => router.upsertNode(n)));
  return { router, nodes };
}

test('rejects invalid constructor options and node definitions', async () => {
  assert.throws(() => new LocalityAwareRouter({ shardCount: 0 }), TopologyError);
  assert.throws(() => new LocalityAwareRouter({ shardCount: 1.5 }), TopologyError);
  assert.throws(() => new LocalityAwareRouter({ replicaCount: -1 }), TopologyError);
  const r = new LocalityAwareRouter();
  await assert.rejects(
    () => r.upsertNode({ id: '', region: 'r', zone: 'z', weight: 1, healthy: true }),
    TopologyError,
  );
  await assert.rejects(
    () => r.upsertNode({ id: 'x', region: 'r', zone: 'z', weight: 0, healthy: true }),
    TopologyError,
  );
  await assert.rejects(
    () => r.upsertNode({ id: 'x', region: 'r', zone: 'z', weight: NaN, healthy: true }),
    TopologyError,
  );
  r.dispose();
});

test('routing on an empty topology fails cleanly', () => {
  const r = new LocalityAwareRouter();
  assert.throws(() => r.route('k', { region: 'r1', zone: 'z1' }), TopologyError);
  r.dispose();
});

test('routing is deterministic and shard ids are in range', async () => {
  const { router } = await buildRouter();
  for (let i = 0; i < 200; i++) {
    const key = `key-${i}`;
    const a = router.route(key, { region: 'r1', zone: 'z1' });
    const b = router.route(key, { region: 'r1', zone: 'z1' });
    assert.equal(a.shardId, b.shardId);
    assert.equal(a.candidates[0].id, b.candidates[0].id);
    assert.ok(a.shardId >= 0 && a.shardId < 256);
  }
  router.dispose();
});

test('locality: same-zone replica is always preferred when available', async () => {
  const { router } = await buildRouter();
  const stats = { sameZone: 0, sameRegion: 0, remote: 0 };
  for (let i = 0; i < 500; i++) {
    const d = router.route(`k${i}`, { region: 'r1', zone: 'z1' });
    if (d.localityDistance === 0) {
      stats.sameZone++;
      assert.equal(d.candidates[0].region, 'r1');
      assert.equal(d.candidates[0].zone, 'z1');
    } else if (d.localityDistance === 1) {
      stats.sameRegion++;
      assert.equal(d.candidates[0].region, 'r1');
    } else {
      stats.remote++;
    }
    // candidates must be ordered by non-decreasing locality distance
    const dists = d.candidates.map((n) =>
      n.zone === 'z1' && n.region === 'r1' ? 0 : n.region === 'r1' ? 1 : 2,
    );
    for (let j = 1; j < dists.length; j++) assert.ok(dists[j] >= dists[j - 1]);
  }
  // with 3 replicas spread across zones, most keys should be servable locally
  assert.ok(
    stats.sameZone / 500 > 0.5,
    `expected majority same-zone, got ${stats.sameZone}/500`,
  );
  const s = router.getStats();
  assert.equal(s.totalRoutes, 500);
  assert.equal(s.sameZone + s.sameRegion + s.remote, 500);
  router.dispose();
});

test('replicas of every shard span distinct zones when topology allows', async () => {
  const { router } = await buildRouter(128, 3);
  for (let shard = 0; shard < 128; shard++) {
    const owners = router.shardOwners(shard);
    assert.equal(owners.length, 3);
    const zones = owners.map((id) => id.split('-').slice(0, 2).join('-'));
    assert.equal(new Set(zones).size, 3, `shard ${shard} replicas not zone-diverse`);
  }
  router.dispose();
});

test('replicaCount larger than cluster degrades gracefully', async () => {
  const router = new LocalityAwareRouter({ shardCount: 16, replicaCount: 5 });
  await router.upsertNode(makeNode('r1', 'z1', 0));
  await router.upsertNode(makeNode('r1', 'z1', 1));
  const d = router.route('k', { region: 'r1', zone: 'z1' });
  assert.equal(d.candidates.length, 2);
  router.dispose();
});

test('unhealthy nodes are excluded; failover goes to next-best locality', async () => {
  const { router } = await buildRouter();
  const key = 'failover-key';
  const before = router.route(key, { region: 'r1', zone: 'z1' });
  const victim = before.candidates[0];
  await router.setHealthy(victim.id, false);
  const after = router.route(key, { region: 'r1', zone: 'z1' });
  assert.notEqual(after.candidates[0].id, victim.id);
  assert.ok(!after.candidates.some((n) => n.id === victim.id));
  await assert.rejects(() => router.setHealthy('ghost', true), TopologyError);
  router.dispose();
});

test('all replicas unhealthy -> TopologyError, recover after heal', async () => {
  const router = new LocalityAwareRouter({ shardCount: 8, replicaCount: 2 });
  await router.upsertNode(makeNode('r1', 'z1', 0));
  await router.upsertNode(makeNode('r1', 'z1', 1));
  await router.setHealthy('r1-z1-n0', false);
  await router.setHealthy('r1-z1-n1', false);
  assert.throws(() => router.route('k', { region: 'r1', zone: 'z1' }), TopologyError);
  await router.setHealthy('r1-z1-n0', true);
  assert.equal(router.route('k', { region: 'r1', zone: 'z1' }).candidates[0].id, 'r1-z1-n0');
  router.dispose();
});

test('virtual shards balance primaries roughly by weight', async () => {
  const router = new LocalityAwareRouter({ shardCount: 2048, replicaCount: 1 });
  await router.upsertNode({ ...makeNode('r1', 'z1', 0), weight: 3 });
  await router.upsertNode({ ...makeNode('r1', 'z1', 1), weight: 1 });
  const heavy = router.primaryCount('r1-z1-n0');
  const light = router.primaryCount('r1-z1-n1');
  // expected ratio 3:1; allow generous statistical slack
  assert.ok(heavy > light * 1.5, `heavy=${heavy} light=${light}`);
  assert.equal(heavy + light, 2048);
  router.dispose();
});

test('node removal moves only a minority of keys (consistent hashing)', async () => {
  const { router, nodes } = await buildRouter(512, 1);
  const keys = Array.from({ length: 2000 }, (_, i) => `key-${i}`);
  const before = new Map(keys.map((k) => [k, router.route(k, { region: 'r1', zone: 'z1' }).candidates[0].id]));
  await router.removeNode(nodes[0].id);
  let moved = 0;
  for (const k of keys) {
    if (router.route(k, { region: 'r1', zone: 'z1' }).candidates[0].id !== before.get(k)) moved++;
  }
  // ideal: 1/12 of keys move; assert well under half
  assert.ok(moved / keys.length < 0.3, `moved ${moved}/${keys.length}`);
  // removed node owns nothing anymore
  assert.equal(router.primaryCount(nodes[0].id), 0);
  assert.equal(router.nodeCount(), nodes.length - 1);
  router.dispose();
});

test('removing an unknown node returns false and changes nothing', async () => {
  const { router } = await buildRouter();
  const v = router.version;
  assert.equal(await router.removeNode('ghost'), false);
  assert.equal(router.version, v);
  router.dispose();
});

test('concurrent topology mutations serialize and stay consistent', async () => {
  const router = new LocalityAwareRouter({ shardCount: 128, replicaCount: 3 });
  const ops: Promise<unknown>[] = [];
  for (let i = 0; i < 50; i++) {
    ops.push(router.upsertNode(makeNode(`r${i % 2}`, `z${i % 3}`, i)));
  }
  for (let i = 0; i < 25; i++) {
    ops.push(router.removeNode(`r${i % 2}-z${i % 3}-n${i}`));
  }
  for (let i = 25; i < 40; i++) {
    ops.push(router.setHealthy(`r${i % 2}-z${i % 3}-n${i}`, false));
  }
  await Promise.all(ops);
  // 50 upserts, 25 removes -> 25 nodes remain
  assert.equal(router.nodeCount(), 25);
  // every mutation published a new snapshot version
  assert.equal(router.version, 50 + 25 + 15);
  // routing still works and every shard has full replica coverage
  for (let shard = 0; shard < 128; shard++) {
    assert.equal(router.shardOwners(shard).length, 3);
  }
  const d = router.route('anything', { region: 'r0', zone: 'z0' });
  assert.ok(d.candidates.length > 0);
  router.dispose();
});

test('concurrent routing during mutation never observes torn state', async () => {
  const router = new LocalityAwareRouter({ shardCount: 64, replicaCount: 2 });
  await router.upsertNode(makeNode('r1', 'z1', 0));
  const errors: unknown[] = [];
  const readers = Array.from({ length: 20 }, async (_, t) => {
    for (let i = 0; i < 100; i++) {
      try {
        const d = router.route(`t${t}-k${i}`, { region: 'r1', zone: 'z1' });
        assert.ok(d.candidates.length >= 1);
        assert.ok(d.version >= 1);
      } catch (err) {
        errors.push(err);
      }
      await Promise.resolve(); // yield to let writers interleave
    }
  });
  const writers = Array.from({ length: 10 }, (_, i) =>
    router.upsertNode(makeNode('r1', 'z1', i + 1)),
  );
  await Promise.all([...readers, ...writers]);
  assert.deepEqual(errors, []);
  assert.equal(router.nodeCount(), 11);
  router.dispose();
});

test('acquire/release tracks load and never leaks counters', async () => {
  const { router } = await buildRouter();
  const id = router.route('k', { region: 'r1', zone: 'z1' }).candidates[0].id;
  const releases = Array.from({ length: 10 }, () => router.acquire(id));
  // double-release is a no-op
  releases.forEach((r) => r());
  releases.forEach((r) => r());
  // load is back to zero: routing tie-break must not see stale load
  const d = router.route('k', { region: 'r1', zone: 'z1' });
  assert.equal(d.candidates[0].id, id);
  router.dispose();
});

test('dispose clears all state and further use is rejected (no leaks)', async () => {
  const { router } = await buildRouter();
  const release = router.acquire('r1-z1-n0');
  release();
  router.dispose();
  assert.equal(router.nodeCount(), 0);
  assert.throws(() => router.route('k', { region: 'r1', zone: 'z1' }), TopologyError);
  await assert.rejects(() => router.upsertNode(makeNode('r1', 'z1', 9)), TopologyError);
  await assert.rejects(() => router.removeNode('r1-z1-n0'), TopologyError);
  assert.throws(() => router.acquire('x'), TopologyError);
});

test('no leaked handles or timers after full lifecycle', async () => {
  const handlesBefore = (process as unknown as { _getActiveHandles(): unknown[] })._getActiveHandles().length;
  for (let round = 0; round < 5; round++) {
    const { router, nodes } = await buildRouter(64, 3);
    for (let i = 0; i < 100; i++) router.route(`k${i}`, { region: 'r1', zone: 'z1' });
    await Promise.all(nodes.slice(0, 4).map((n) => router.removeNode(n.id)));
    router.dispose();
  }
  await new Promise((r) => setImmediate(r));
  const handlesAfter = (process as unknown as { _getActiveHandles(): unknown[] })._getActiveHandles().length;
  assert.ok(
    handlesAfter <= handlesBefore,
    `handle leak: before=${handlesBefore} after=${handlesAfter}`,
  );
});

test('hashKey is deterministic and well distributed over shard space', () => {
  assert.equal(hashKey('hello'), hashKey('hello'));
  assert.notEqual(hashKey('hello'), hashKey('hellp'));
  const buckets = new Array<number>(16).fill(0);
  for (let i = 0; i < 16000; i++) buckets[hashKey(`key-${i}`) % 16]++;
  const expected = 1000;
  for (const b of buckets) {
    assert.ok(Math.abs(b - expected) < expected * 0.3, `bucket skew: ${b}`);
  }
});
