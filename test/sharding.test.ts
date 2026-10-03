import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ShardingEngine } from '../src/sharding/engine.ts';
import { ShardMap } from '../src/sharding/shardmap.ts';
import { Topology } from '../src/sharding/topology.ts';
import { fnv1a32 } from '../src/sharding/hash.ts';

function makeTopology(): Topology {
  const topology = new Topology();
  topology.addNode({ id: 'e1', region: 'east', zone: 'ea' });
  topology.addNode({ id: 'e2', region: 'east', zone: 'ea' });
  topology.addNode({ id: 'e3', region: 'east', zone: 'eb' });
  topology.addNode({ id: 'w1', region: 'west', zone: 'wa' });
  topology.addNode({ id: 'w2', region: 'west', zone: 'wa' });
  return topology;
}

test('virtual shards distribute evenly across nodes', () => {
  const topology = makeTopology();
  const map = ShardMap.build(topology, 1024, 1, 0);
  const counts = map.replicaCounts();
  assert.equal(counts.size, 5);
  const avg = 1024 / 5;
  for (const [id, count] of counts) {
    assert.ok(count > avg * 0.5, `${id} holds too few shards: ${count}`);
    assert.ok(count < avg * 1.6, `${id} holds too many shards: ${count}`);
    assert.equal(count, map.replicaCounts().get(id));
  }
});

test('every virtual shard has exactly RF distinct live replicas', () => {
  const topology = makeTopology();
  const map = ShardMap.build(topology, 512, 3, 0);
  for (let v = 0; v < 512; v++) {
    const replicas = map.replicasOfVshard(v);
    assert.equal(replicas.length, 3);
    assert.equal(new Set(replicas).size, 3, 'replicas must be distinct');
    for (const id of replicas) assert.ok(topology.has(id));
  }
});

test('replication factor is clamped to the number of available nodes', () => {
  const topology = new Topology();
  topology.addNode({ id: 'solo', region: 'r', zone: 'z' });
  const map = ShardMap.build(topology, 64, 3, 0);
  assert.equal(map.replicationFactor, 1);
  for (let v = 0; v < 64; v++) assert.deepEqual(map.replicasOfVshard(v), ['solo']);
});

test('invalid configuration and lookup boundaries throw', () => {
  const topology = makeTopology();
  assert.throws(() => ShardMap.build(topology, 0, 3, 0), RangeError);
  assert.throws(() => ShardMap.build(topology, -5, 3, 0), RangeError);
  assert.throws(() => ShardMap.build(topology, 1.5, 3, 0), RangeError);
  assert.throws(() => ShardMap.build(topology, 16, 0, 0), RangeError);
  const map = ShardMap.build(topology, 16, 2, 0);
  assert.throws(() => map.replicasOfVshard(-1), RangeError);
  assert.throws(() => map.replicasOfVshard(16), RangeError);
  assert.throws(() => map.replicasOfVshard(1.2), RangeError);
});

test('topology boundary conditions are enforced', () => {
  const topology = new Topology();
  assert.throws(() => topology.addNode({ id: '', region: 'r', zone: 'z' }), /empty/);
  assert.throws(() => topology.addNode({ id: 'n', region: '', zone: 'z' }), /region and zone/);
  topology.addNode({ id: 'n', region: 'r', zone: 'z' });
  assert.throws(() => topology.addNode({ id: 'n', region: 'r', zone: 'z' }), /duplicate/);
  assert.throws(() => topology.removeNode('ghost'), /unknown node/);
  assert.throws(() => topology.setRegionLatency('a', 'b', -1), RangeError);
  topology.dispose();
  assert.throws(() => topology.addNode({ id: 'x', region: 'r', zone: 'z' }), /disposed/);
});

test('key hashing is deterministic and handles edge-case keys', () => {
  const topology = makeTopology();
  const a = ShardMap.build(topology, 256, 2, 0);
  const b = ShardMap.build(topology, 256, 2, 1);
  for (const key of ['', 'a', 'user-key-\u{1F511}', 'x'.repeat(4096), ' ']) {
    assert.equal(a.vshardOf(key), b.vshardOf(key), `vshard stable for key ${JSON.stringify(key)}`);
    assert.deepEqual(a.replicasOf(key), b.replicasOf(key));
  }
  assert.equal(fnv1a32('same'), fnv1a32('same'));
});

test('reads route to the cheapest replica in the hierarchy', () => {
  const topology = makeTopology();
  topology.setRegionLatency('east', 'west', 42);
  const engine = new ShardingEngine(topology, { vshardCount: 256, replicationFactor: 3 });
  const client = topology.get('e1');
  assert.ok(client);
  for (let i = 0; i < 2000; i++) {
    const key = `key-${i}`;
    const route = engine.router.routeRead(key, 'e1');
    const replicas = engine.getShardMap().replicasOf(key);
    assert.ok(replicas.includes(route.nodeId), 'route targets a replica');
    const expected = Math.min(...replicas.map((id) => topology.cost(client, topology.get(id)!)));
    assert.equal(route.cost, expected, `minimal hierarchical cost for ${key}`);
    const chosenZone = topology.get(route.nodeId);
    if (replicas.some((id) => topology.get(id)?.zone === 'ea')) {
      assert.equal(chosenZone?.zone, 'ea', 'same-zone replica preferred');
      assert.equal(route.local, true);
    }
  }
  engine.dispose();
  topology.dispose();
});

test('cross-region latency overrides drive locality decisions', () => {
  const topology = new Topology();
  topology.addNode({ id: 'c', region: 'east', zone: 'z1' });
  topology.addNode({ id: 'r1', region: 'west', zone: 'z2' });
  topology.addNode({ id: 'r2', region: 'north', zone: 'z3' });
  topology.setRegionLatency('east', 'north', 3);
  topology.setRegionLatency('east', 'west', 50);
  const engine = new ShardingEngine(topology, { vshardCount: 8, replicationFactor: 3 });
  for (let i = 0; i < 500; i++) {
    const route = engine.router.routeRead(`k${i}`, 'c');
    assert.equal(route.nodeId, 'c', 'local replica always wins');
    assert.equal(route.cost, 0);
  }
  // Remove the local node: reads from r1 must now stay on r1 itself.
  topology.removeNode('c');
  const engine2 = new ShardingEngine(topology, { vshardCount: 8, replicationFactor: 2 });
  for (let i = 0; i < 500; i++) {
    const route = engine2.router.routeRead(`k${i}`, 'r1');
    assert.equal(route.nodeId, 'r1');
  }
  engine.dispose();
  engine2.dispose();
  topology.dispose();
});

test('writes always go to the primary replica', () => {
  const topology = makeTopology();
  const engine = new ShardingEngine(topology, { vshardCount: 128, replicationFactor: 3 });
  for (let i = 0; i < 1000; i++) {
    const key = `w-${i}`;
    assert.equal(engine.router.routeWrite(key).nodeId, engine.getShardMap().primaryOf(key));
  }
  engine.dispose();
  topology.dispose();
});

test('routing on an empty cluster throws a clear error', () => {
  const topology = new Topology();
  const engine = new ShardingEngine(topology, { vshardCount: 16, replicationFactor: 3 });
  assert.throws(() => engine.router.routeRead('k'), /no nodes available/);
  assert.throws(() => engine.router.routeWrite('k'), /no nodes available/);
  engine.dispose();
  topology.dispose();
});

test('unknown client node is rejected', () => {
  const topology = makeTopology();
  const engine = new ShardingEngine(topology, { vshardCount: 16, replicationFactor: 2 });
  assert.throws(() => engine.router.routeRead('k', 'ghost'), /unknown client node/);
  engine.dispose();
  topology.dispose();
});

test('batch planning groups keys by target node without loss', () => {
  const topology = makeTopology();
  const engine = new ShardingEngine(topology, { vshardCount: 256, replicationFactor: 2 });
  const keys = Array.from({ length: 3000 }, (_, i) => `batch-${i}`);
  const plan = engine.router.planBatch(keys, 'w1');
  const merged = [...plan.values()].flat();
  assert.equal(merged.length, keys.length, 'no key lost or duplicated');
  assert.deepEqual([...merged].sort(), [...keys].sort());
  for (const [nodeId, bucket] of plan) {
    for (const key of bucket) {
      assert.equal(engine.router.routeRead(key, 'w1').nodeId, nodeId);
    }
  }
  engine.dispose();
  topology.dispose();
});

test('adding a node moves a minimal, bounded fraction of shards', async () => {
  const topology = makeTopology();
  const engine = new ShardingEngine(topology, { vshardCount: 1024, replicationFactor: 3 });
  const before = engine.getShardMap();
  const plan = await engine.addNode({ id: 'w3', region: 'west', zone: 'wb' });
  assert.equal(plan.fromVersion, before.version);
  assert.equal(plan.toVersion, before.version + 1);
  // Ideal rendezvous movement: RF/(n+1) = 3/6 = 50% of vshards gain the new
  // node; crucially the other 50% are completely untouched.
  const fraction = plan.moves.length / 1024;
  assert.ok(fraction > 0.35 && fraction < 0.65, `moved fraction ${fraction} near ideal 0.5`);
  for (const move of plan.moves) {
    assert.ok(move.to.includes('w3'), 'every move involves the new node');
    assert.ok(!move.from.includes('w3'));
  }
  assert.equal(before.diffCount(engine.getShardMap()), plan.moves.length);
  // The new node ends up with roughly its fair 1/6 share of replicas.
  const share = (engine.getShardMap().replicaCounts().get('w3') ?? 0) / (1024 * 3);
  assert.ok(share > 0.1 && share < 0.24, `new node replica share ${share}`);
  engine.dispose();
  topology.dispose();
});

test('removing a node only reshuffles the shards it held', async () => {
  const topology = makeTopology();
  const engine = new ShardingEngine(topology, { vshardCount: 1024, replicationFactor: 3 });
  const plan = await engine.removeNode('e2');
  for (const move of plan.moves) {
    assert.ok(move.from.includes('e2'), 'only shards of the removed node move');
    assert.ok(!move.to.includes('e2'), 'removed node disappears from assignments');
  }
  const map = engine.getShardMap();
  for (let v = 0; v < map.vshardCount; v++) {
    assert.ok(!map.replicasOfVshard(v).includes('e2'));
  }
  engine.dispose();
  topology.dispose();
});

test('router stats track local vs remote reads and reset cleanly', () => {
  const topology = makeTopology();
  const engine = new ShardingEngine(topology, { vshardCount: 256, replicationFactor: 3 });
  for (let i = 0; i < 1000; i++) engine.router.routeRead(`s-${i}`, 'e1');
  const stats = engine.router.stats;
  assert.equal(stats.localReads + stats.remoteReads, 1000);
  assert.ok(stats.localReads > 0, 'some reads stay local');
  engine.router.resetStats();
  assert.deepEqual(engine.router.stats, { localReads: 0, remoteReads: 0 });
  engine.dispose();
  topology.dispose();
});
