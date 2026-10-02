import { test } from "node:test";
import assert from "node:assert/strict";
import { ShardEngine } from "../src/sharding/engine.ts";
import { NoReplicaError } from "../src/sharding/router.ts";
import { fnv1a } from "../src/sharding/hash.ts";
import type { TopoNode } from "../src/sharding/topology.ts";

function makeNodes(count: number, regions = ["r1", "r2"], zonesPerRegion = 2): TopoNode[] {
  const nodes: TopoNode[] = [];
  for (let i = 0; i < count; i++) {
    const region = regions[i % regions.length];
    const zone = `${region}-z${(Math.floor(i / regions.length) % zonesPerRegion) + 1}`;
    nodes.push({ id: `node-${i}`, region, zone });
  }
  return nodes;
}

function lcg(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (Math.imul(state, 1664525) + 1013904223) >>> 0;
    return state / 0x100000000;
  };
}

test("shardOf is deterministic and distributes keys across virtual shards", () => {
  const engine = new ShardEngine(64, 3);
  assert.equal(engine.shardOf("alpha"), engine.shardOf("alpha"));
  assert.notEqual(fnv1a("alpha"), fnv1a("beta"));

  const buckets = new Array(64).fill(0);
  for (let i = 0; i < 10000; i++) buckets[engine.shardOf(`key-${i}`)]++;
  const mean = 10000 / 64;
  const min = Math.min(...buckets);
  const max = Math.max(...buckets);
  assert.ok(min > 0, "every virtual shard receives keys");
  assert.ok(max < mean * 3, `max bucket ${max} within 3x mean ${mean}`);
});

test("placement: exact replica factor, distinct nodes, zone diversity", () => {
  const engine = new ShardEngine(256, 3);
  for (const node of makeNodes(8)) engine.join(node);

  assert.deepEqual(engine.invariants(), []);
  const stats = engine.stats();
  assert.equal(stats.totalAssignments, 256 * 3);
  const mean = (256 * 3) / 8;
  for (const [id, count] of stats.perNode) {
    assert.ok(
      count > mean * 0.4 && count < mean * 1.6,
      `${id} holds ${count} replicas (mean ${mean})`,
    );
  }
});

test("rebalance on join moves a minimal fraction of replicas", () => {
  const engine = new ShardEngine(512, 3);
  for (const node of makeNodes(10)) engine.join(node);
  const before = engine.getTable()!;

  engine.join({ id: "node-new", region: "r1", zone: "r1-z1" });
  const after = engine.getTable()!;

  let moved = 0;
  for (let shard = 0; shard < 512; shard++) {
    const a = before.assignments.get(shard)!;
    const b = after.assignments.get(shard)!;
    if (a.length !== b.length || a.some((id, i) => id !== b[i])) moved++;
  }
  const total = 512 * 3;
  assert.ok(
    moved < total * 0.25,
    `moved ${moved}/${total} shard placements (< 25% expected for rendezvous hashing)`,
  );
  assert.deepEqual(engine.invariants(), []);
});

test("node leave reassigns its replicas and never references dead nodes", () => {
  const engine = new ShardEngine(128, 3);
  const nodes = makeNodes(6);
  for (const node of nodes) engine.join(node);

  engine.leave(nodes[0].id);
  assert.deepEqual(engine.invariants(), []);
  const table = engine.getTable()!;
  for (const replicas of table.assignments.values()) {
    assert.ok(!replicas.includes(nodes[0].id));
  }
  assert.equal(engine.stats().totalAssignments, 128 * 3);
});

test("locality-aware routing prefers same zone, then same region", () => {
  const engine = new ShardEngine(256, 3);
  // 2 regions x 2 zones x 2 nodes = 8 nodes; replica factor 3 spreads over
  // 3 distinct zones, so every region always holds at least one replica.
  for (const node of makeNodes(8, ["r1", "r2"], 2)) engine.join(node);

  let sameZone = 0;
  for (let i = 0; i < 2000; i++) {
    const key = `user-${i}`;
    const decision = engine.route(key, { region: "r1", zone: "r1-z1" });
    assert.ok(decision.distance <= 2, `${key} never routed cross-region`);
    if (decision.distance === 1) sameZone++;
  }
  assert.ok(sameZone > 0, "some keys are served from the client's own zone");
});

test("locality-aware routing beats random replica selection", () => {
  const engine = new ShardEngine(256, 3);
  const nodes = makeNodes(8, ["r1", "r2"], 2);
  for (const node of nodes) engine.join(node);
  const byId = new Map(nodes.map((node) => [node.id, node]));

  const rand = lcg(42);
  const clients = [
    { region: "r1", zone: "r1-z1" },
    { region: "r1", zone: "r1-z2" },
    { region: "r2", zone: "r2-z1" },
    { region: "r2", zone: "r2-z2" },
  ];
  let localityCost = 0;
  let randomCost = 0;
  for (let i = 0; i < 5000; i++) {
    const client = clients[i % clients.length];
    const decision = engine.route(`key-${i}`, client);
    localityCost += decision.distance;
    const again = engine.route(`key-${i}`, client);
    assert.equal(again.nodeId, decision.nodeId, "routing is deterministic");
    const randomReplica = decision.replicas[Math.floor(rand() * decision.replicas.length)];
    randomCost += distanceOf(byId.get(randomReplica)!, client);
  }
  assert.ok(
    localityCost < randomCost,
    `locality cost ${localityCost} < random cost ${randomCost}`,
  );
});

test("zone names colliding across regions still guarantee region locality", () => {
  const engine = new ShardEngine(256, 3);
  // Same zone names ("z1"/"z2") reused in both regions, as in real clouds.
  for (const region of ["r1", "r2"]) {
    for (const zone of ["z1", "z2"]) {
      for (let i = 0; i < 2; i++) {
        engine.join({ id: `${region}-${zone}-${i}`, region, zone });
      }
    }
  }
  assert.deepEqual(engine.invariants(), [], "zone diversity uses region-qualified zones");
  for (let i = 0; i < 2000; i++) {
    for (const client of [
      { region: "r1", zone: "z1" },
      { region: "r2", zone: "z2" },
    ]) {
      const decision = engine.route(`key-${i}`, client);
      assert.ok(decision.distance <= 2, `key-${i} routed cross-region`);
    }
  }
});

function distanceOf(node: TopoNode, client: { region: string; zone: string }): number {
  if (node.region === client.region && node.zone === client.zone) return 1;
  if (node.region === client.region) return 2;
  return 3;
}

test("failover: dead nearest node is skipped, all-dead shard raises", () => {
  const engine = new ShardEngine(64, 3);
  const nodes = makeNodes(6);
  for (const node of nodes) engine.join(node);

  const client = { region: "r1", zone: "r1-z1" };
  const first = engine.route("hot-key", client);
  engine.leave(first.nodeId);
  const second = engine.route("hot-key", client);
  assert.notEqual(second.nodeId, first.nodeId);
  assert.ok(second.distance >= first.distance);

  for (const node of nodes) {
    if (node.id !== first.nodeId) engine.leave(node.id);
  }
  assert.throws(() => engine.route("hot-key", client), NoReplicaError);
});

test("edge cases: empty engine, single node, replicaFactor > node count", () => {
  const empty = new ShardEngine(16, 3);
  assert.throws(() => empty.route("k", { region: "r1", zone: "z1" }), NoReplicaError);

  const single = new ShardEngine(16, 3);
  single.join({ id: "solo", region: "r1", zone: "z1" });
  assert.deepEqual(single.invariants(), []);
  const decision = single.route("k", { region: "r2", zone: "z9" });
  assert.equal(decision.nodeId, "solo");
  assert.equal(decision.distance, 3);

  const pair = new ShardEngine(16, 5);
  pair.join({ id: "a", region: "r1", zone: "z1" });
  pair.join({ id: "b", region: "r1", zone: "z2" });
  assert.deepEqual(pair.invariants(), []);
  for (const replicas of pair.getTable()!.assignments.values()) {
    assert.equal(replicas.length, 2, "replicas clamped to node count");
  }
});

test("routing table snapshots are immutable and isolated across rebalances", () => {
  const engine = new ShardEngine(64, 3);
  for (const node of makeNodes(6)) engine.join(node);
  const v1 = engine.getTable()!;
  const snapshotCopy = new Map<number, string[]>();
  for (const [shard, replicas] of v1.assignments) {
    snapshotCopy.set(shard, [...replicas]);
  }

  engine.join({ id: "node-x", region: "r1", zone: "r1-z1" });
  const v2 = engine.getTable()!;
  assert.ok(v2.version > v1.version);
  for (const [shard, replicas] of v1.assignments) {
    assert.deepEqual([...replicas], snapshotCopy.get(shard)!, `v1 shard ${shard} untouched`);
  }
});

test("concurrent routes and rebalances observe consistent snapshots", async () => {
  const engine = new ShardEngine(128, 3);
  for (const node of makeNodes(8)) engine.join(node);

  const decisions: { nodeId: string; replicas: readonly string[]; version: number }[] = [];
  const tasks: Promise<void>[] = [];
  for (let i = 0; i < 300; i++) {
    tasks.push(
      (async () => {
        await Promise.resolve();
        const decision = engine.route(`key-${i}`, { region: "r1", zone: "r1-z1" });
        decisions.push(decision);
        if (i % 25 === 0) {
          engine.join({ id: `extra-${i}`, region: "r2", zone: "r2-z1" });
        }
      })(),
    );
  }
  await Promise.all(tasks);

  assert.equal(decisions.length, 300);
  for (const decision of decisions) {
    assert.ok(
      decision.replicas.includes(decision.nodeId),
      "routed node belongs to the replica set of the same snapshot",
    );
    assert.equal(decision.replicas.length, 3, "snapshot has full replica factor");
  }
  assert.deepEqual(engine.invariants(), []);

  // No leak: every joined node is accounted for exactly, totals match.
  const stats = engine.stats();
  assert.equal(stats.members, 8 + 12);
  assert.equal(stats.totalAssignments, 128 * 3);
  let sum = 0;
  for (const count of stats.perNode.values()) sum += count;
  assert.equal(sum, stats.totalAssignments);
});

test("no state leaks after full membership teardown", () => {
  const engine = new ShardEngine(64, 3);
  const nodes = makeNodes(8);
  for (const node of nodes) engine.join(node);
  assert.equal(engine.stats().totalAssignments, 64 * 3);

  for (const node of nodes) engine.leave(node.id);
  const stats = engine.stats();
  assert.equal(stats.members, 0);
  assert.equal(stats.totalAssignments, 0);
  assert.equal(stats.perNode.size, 0);
  assert.equal(engine.memberCount, 0);
  assert.deepEqual(engine.invariants(), []);
  assert.throws(() => engine.route("k", { region: "r1", zone: "z1" }), NoReplicaError);
});
