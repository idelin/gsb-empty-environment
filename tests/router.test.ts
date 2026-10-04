import { test } from 'node:test';
import assert from 'node:assert/strict';
import { LocalityAwareRouter, HopLocality, type NodeInfo } from '../src/sharding/engine.ts';

function makeNodes(spec: Array<[string, string, string, number?]>): NodeInfo[] {
  return spec.map(([id, region, zone, weight]) => ({ id, region, zone, weight: weight ?? 1 }));
}

const TOPOLOGY: Array<[string, string, string, number?]> = [
  ['r1z1n1', 'cn-east', 'z1'],
  ['r1z1n2', 'cn-east', 'z1'],
  ['r1z2n1', 'cn-east', 'z2'],
  ['r2z1n1', 'cn-north', 'z1'],
  ['r2z2n1', 'cn-north', 'z2'],
  ['r2z2n2', 'cn-north', 'z2'],
];

async function makeRouter(shardCount = 256, replicaCount = 3): Promise<LocalityAwareRouter> {
  const router = new LocalityAwareRouter({ shardCount, replicaCount });
  for (const n of makeNodes(TOPOLOGY)) await router.addNode(n);
  return router;
}

test('constructor validates options', () => {
  assert.throws(() => new LocalityAwareRouter({ shardCount: 0 }), RangeError);
  assert.throws(() => new LocalityAwareRouter({ shardCount: -5 }), RangeError);
  assert.throws(() => new LocalityAwareRouter({ shardCount: 1.5 }), RangeError);
  assert.throws(() => new LocalityAwareRouter({ replicaCount: 0 }), RangeError);
});

test('routing on an empty topology throws', () => {
  const router = new LocalityAwareRouter();
  assert.throws(() => router.route('key'), /empty/);
});

test('single-node topology: every shard maps to that node', async () => {
  const router = new LocalityAwareRouter({ shardCount: 64, replicaCount: 3 });
  await router.addNode({ id: 'solo', region: 'r', zone: 'z', weight: 1 });
  const placement = router.getPlacement();
  assert.equal(placement.length, 64);
  for (const replicas of placement) {
    assert.deepEqual(replicas, ['solo']);
  }
  const decision = router.route('anything', { region: 'r', zone: 'z' });
  assert.equal(decision.locality, HopLocality.Local);
  assert.equal(decision.crossNodeHops, 0);
});

test('replicaCount larger than node count degrades gracefully', async () => {
  const router = new LocalityAwareRouter({ shardCount: 32, replicaCount: 5 });
  for (const n of makeNodes(TOPOLOGY.slice(0, 2))) await router.addNode(n);
  for (const replicas of router.getPlacement()) {
    assert.equal(replicas.length, 2);
    assert.equal(new Set(replicas).size, 2, 'no duplicate node in a shard');
  }
});

test('every shard has the requested number of distinct replicas', async () => {
  const router = await makeRouter();
  for (const replicas of router.getPlacement()) {
    assert.equal(replicas.length, 3);
    assert.equal(new Set(replicas).size, 3);
  }
});

test('replicas prefer distinct zones (anti-affinity)', async () => {
  const router = await makeRouter(512, 3);
  let spread = 0;
  for (const replicas of router.getPlacement()) {
    const zones = new Set(
      replicas.map((id) => TOPOLOGY.find(([nid]) => nid === id)![1] + '/' + TOPOLOGY.find(([nid]) => nid === id)![2]),
    );
    if (zones.size >= 2) spread++;
  }
  // With 6 nodes in 3 zone-groups, essentially all shards should span zones.
  assert.ok(spread / 512 > 0.9, `expected >90% zone-spread shards, got ${spread}/512`);
});

test('shard assignment is deterministic across router instances', async () => {
  const a = await makeRouter();
  const b = await makeRouter();
  assert.deepEqual(a.getPlacement(), b.getPlacement());
});

test('node removal moves only a bounded fraction of shards (minimal reshuffle)', async () => {
  const router = await makeRouter(512);
  const before = router.getPlacement();
  await router.removeNode('r1z1n1');
  const after = router.getPlacement();

  let moved = 0;
  for (let i = 0; i < before.length; i++) {
    if (before[i].join(',') !== after[i].join(',')) moved++;
    assert.ok(!after[i].includes('r1z1n1'), 'removed node purged from placement');
  }
  // Ideal is ~replicaCount/nodeCount = 3/6 = 50% of shard *slots*, but each
  // shard has 3 slots so shard-level churn should stay well below 100%.
  assert.ok(moved > 0, 'some shards must move');
  assert.ok(moved / before.length < 0.9, `moved ${moved}/512 shards, expected bounded reshuffle`);
});

test('adding a node in an existing zone only changes shards it wins', async () => {
  const router = await makeRouter(512);
  const before = router.getPlacement();
  // Same region+zone as existing nodes: no new locality dimension, so HRW
  // guarantees untouched shards stay identical and won shards lose <=1 member.
  await router.addNode({ id: 'r1z1n3', region: 'cn-east', zone: 'z1', weight: 1 });
  const after = router.getPlacement();
  let won = 0;
  for (let i = 0; i < before.length; i++) {
    if (after[i].includes('r1z1n3')) {
      won++;
      const kept = before[i].filter((id) => after[i].includes(id));
      assert.ok(kept.length >= before[i].length - 1, `shard ${i} lost too many members`);
    } else {
      assert.deepEqual(after[i], before[i], `shard ${i} changed without the new node winning it`);
    }
  }
  assert.ok(won > 0, 'new node should win some shards');
  assert.ok(won < before.length / 2, `new node won ${won}/512 shards, expected a minority`);
  assert.equal(router.nodeCount, 7);
});

test('adding a node in a NEW region improves region diversity of replicas', async () => {
  const router = await makeRouter(512);
  const regionOf = (id: string) => TOPOLOGY.find(([nid]) => nid === id)?.[1] ?? 'cn-south';
  const diversity = (p: string[][]) =>
    p.filter((replicas) => new Set(replicas.map(regionOf)).size >= 3).length;
  const beforeDiversity = diversity(router.getPlacement());
  await router.addNode({ id: 'r3z1n1', region: 'cn-south', zone: 'z1', weight: 1 });
  const afterDiversity = diversity(router.getPlacement());
  assert.ok(
    afterDiversity > beforeDiversity,
    `region diversity should improve: ${beforeDiversity} -> ${afterDiversity} (of 512 shards)`,
  );
});

test('locality-aware ordering prefers same zone, then region, then remote', async () => {
  const router = await makeRouter();
  // Find a key whose shard has replicas in both regions.
  let key = '';
  for (let i = 0; i < 10000 && !key; i++) {
    const candidate = `key-${i}`;
    const replicas = router.replicasOf(router.shardOf(candidate));
    const regions = new Set(replicas.map((id) => TOPOLOGY.find(([nid]) => nid === id)![1]));
    if (regions.size === 2) key = candidate;
  }
  assert.ok(key, 'expected a cross-region shard in 10k keys');

  const east = router.route(key, { region: 'cn-east', zone: 'z1' });
  const firstRegion = TOPOLOGY.find(([nid]) => nid === east.replicas[0].id)![1];
  assert.equal(firstRegion, 'cn-east');
  assert.notEqual(east.locality, HopLocality.Remote);

  const nowhere = router.route(key, { region: 'eu-west', zone: 'z9' });
  assert.equal(nowhere.locality, HopLocality.Remote);
  assert.equal(nowhere.crossNodeHops, 1);
});

test('locality metrics accumulate and reset correctly', async () => {
  const router = await makeRouter();
  router.resetMetrics();
  for (let i = 0; i < 200; i++) router.route(`k${i}`, { region: 'cn-east', zone: 'z1' });
  for (let i = 0; i < 50; i++) router.route(`k${i}`, { region: 'xx', zone: 'yy' });
  const m = router.getMetrics();
  assert.equal(m.totalRoutes, 250);
  assert.equal(m.localRoutes + m.regionalRoutes + m.remoteRoutes, 250);
  assert.equal(m.remoteRoutes, 50);
  assert.ok(m.localRoutes > 0, 'local zone should absorb a good share of traffic');
  router.resetMetrics();
  assert.equal(router.getMetrics().totalRoutes, 0);
});

test('local routing dominates cross-node traffic for a local client', async () => {
  const router = await makeRouter(1024);
  router.resetMetrics();
  for (let i = 0; i < 2000; i++) router.route(`user:${i}`, { region: 'cn-east', zone: 'z1' });
  const m = router.getMetrics();
  const localRatio = m.localRoutes / m.totalRoutes;
  // 2 of 6 nodes are in cn-east/z1; random placement would give ~1/3 local.
  assert.ok(localRatio > 0.25, `local ratio ${localRatio} too low`);
  assert.ok(m.crossNodeHops <= m.totalRoutes);
});

test('concurrent topology mutations are serialized and consistent', async () => {
  const router = new LocalityAwareRouter({ shardCount: 128, replicaCount: 2 });
  const additions: Promise<void>[] = [];
  for (let i = 0; i < 50; i++) {
    additions.push(router.addNode({ id: `n${i}`, region: `r${i % 3}`, zone: `z${i % 4}`, weight: 1 }));
  }
  await Promise.all(additions);
  assert.equal(router.nodeCount, 50);

  // Interleave removals and additions; every mutation must be atomic.
  const ops: Promise<unknown>[] = [];
  for (let i = 0; i < 50; i++) {
    if (i % 2 === 0) ops.push(router.removeNode(`n${i}`));
    else ops.push(router.addNode({ id: `m${i}`, region: 'r0', zone: 'z0', weight: 2 }));
  }
  // Concurrent routing during mutation must never observe a torn state.
  for (let i = 0; i < 200; i++) {
    ops.push(
      Promise.resolve().then(() => {
        const d = router.route(`ck${i}`);
        assert.ok(d.replicas.length > 0);
        for (const r of d.replicas) assert.ok(r.id, 'replica is a full node object');
      }),
    );
  }
  await Promise.all(ops);

  const expected = 50 - 25 + 25; // 50 added, 25 removed (even ids), 25 added
  assert.equal(router.nodeCount, expected);
  for (const replicas of router.getPlacement()) {
    for (const id of replicas) {
      assert.ok(!/^n\d*[02468]$/.test(id) || Number(id.slice(1)) % 2 === 1, `evicted node ${id} leaked into placement`);
    }
  }
});

test('concurrent duplicate adds and remove-of-missing are idempotent-safe', async () => {
  const router = new LocalityAwareRouter({ shardCount: 16 });
  await Promise.all(
    Array.from({ length: 20 }, () => router.addNode({ id: 'dup', region: 'r', zone: 'z', weight: 1 })),
  );
  assert.equal(router.nodeCount, 1);
  const results = await Promise.all(Array.from({ length: 10 }, () => router.removeNode('dup')));
  assert.equal(results.filter(Boolean).length, 1, 'exactly one removal reports success');
  assert.equal(await router.removeNode('never-existed'), false);
});

test('leak-free teardown: removing all nodes empties every index', async () => {
  const router = await makeRouter(128);
  assert.equal(router.nodeCount, 6);
  for (const [id] of TOPOLOGY) await router.removeNode(id);
  assert.equal(router.nodeCount, 0);
  for (const replicas of router.getPlacement()) {
    assert.deepEqual(replicas, []);
  }
  assert.throws(() => router.route('k'), /empty/);
});

test('no retained references after teardown (GC / WeakRef probe)', async () => {
  if (typeof (globalThis as { gc?: () => void }).gc !== 'function') {
    return; // requires node --expose-gc; skipped otherwise
  }
  const router = new LocalityAwareRouter({ shardCount: 64 });
  let node: NodeInfo | null = { id: 'ephemeral', region: 'r', zone: 'z', weight: 1 };
  const ref = new WeakRef(node);
  await router.addNode(node);
  await router.removeNode('ephemeral');
  node = null;
  (globalThis as { gc: () => void }).gc();
  await new Promise((resolve) => setImmediate(resolve));
  (globalThis as { gc: () => void }).gc();
  assert.equal(ref.deref(), undefined, 'removed node must not be retained by the router');
});

test('getPlacement returns defensive copies (mutation cannot corrupt state)', async () => {
  const router = await makeRouter(16);
  const snapshot = router.getPlacement();
  snapshot[0].push('injected');
  snapshot.length = 0;
  assert.equal(router.getPlacement().length, 16);
  assert.ok(!router.getPlacement()[0].includes('injected'));
});

test('weight influences shard share', async () => {
  const router = new LocalityAwareRouter({ shardCount: 1024, replicaCount: 1 });
  await router.addNode({ id: 'heavy', region: 'r', zone: 'z', weight: 10 });
  await router.addNode({ id: 'light', region: 'r', zone: 'z', weight: 1 });
  let heavy = 0;
  for (const [id] of router.getPlacement()) if (id === 'heavy') heavy++;
  assert.ok(heavy > 1024 * 0.6, `heavy node should win most shards, got ${heavy}/1024`);
});

test('invalid node input is rejected', async () => {
  const router = new LocalityAwareRouter();
  await assert.rejects(() => router.addNode({ id: '', region: 'r', zone: 'z', weight: 1 }), RangeError);
  await assert.rejects(() => router.addNode({ id: 'x', region: 'r', zone: 'z', weight: 0 }), RangeError);
});
