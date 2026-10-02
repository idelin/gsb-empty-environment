import { test } from "node:test";
import assert from "node:assert/strict";
import { ConsistentHashRing } from "../src/ring.ts";
import type { RingNode } from "../src/ring.ts";

function makeNodes(count: number, zones: string[]): RingNode[] {
  return Array.from({ length: count }, (_, i) => ({
    id: `node-${i}`,
    topology: { region: "cn-east", zone: zones[i % zones.length] },
  }));
}

test("环上路由是确定性的", () => {
  const ring = new ConsistentHashRing(makeNodes(6, ["z1", "z2", "z3"]));
  const first = ring.getNodeForKey("user:10086");
  for (let i = 0; i < 100; i++) {
    assert.equal(ring.getNodeForKey("user:10086"), first);
  }
  assert.ok(first !== undefined);
});

test("空环返回 undefined，单节点环全量命中", () => {
  const empty = new ConsistentHashRing();
  assert.equal(empty.getNodeForKey("k"), undefined);

  const single = new ConsistentHashRing([{ id: "only" }]);
  for (let i = 0; i < 50; i++) {
    assert.equal(single.getNodeForKey(`k${i}`), "only");
  }
});

test("负载在节点间近似均匀分布", () => {
  const nodes = makeNodes(5, ["z1"]);
  const ring = new ConsistentHashRing(nodes);
  const counts = new Map<string, number>();
  const total = 20000;
  for (let i = 0; i < total; i++) {
    const id = ring.getNodeForKey(`key-${i}`)!;
    counts.set(id, (counts.get(id) ?? 0) + 1);
  }
  const expected = total / nodes.length;
  for (const node of nodes) {
    const got = counts.get(node.id) ?? 0;
    assert.ok(
      Math.abs(got - expected) / expected < 0.25,
      `node ${node.id} deviates too much: ${got} vs ${expected}`,
    );
  }
});

test("移除节点只迁移其持有的键（一致性哈希最小扰动）", () => {
  const nodes = makeNodes(6, ["z1", "z2"]);
  const before = new ConsistentHashRing(nodes);
  const after = before.removeNode("node-3");

  let moved = 0;
  let movedToRemoved = 0;
  const total = 20000;
  for (let i = 0; i < total; i++) {
    const key = `key-${i}`;
    const from = before.getNodeForKey(key);
    const to = after.getNodeForKey(key);
    if (from !== to) {
      moved++;
      if (to === "node-3") movedToRemoved++;
    }
  }
  const ratio = moved / total;
  assert.ok(ratio > 0.1 && ratio < 0.3, `moved ratio ${ratio} should be ~1/6`);
  assert.equal(movedToRemoved, 0, "no key may move onto the removed node");
});

test("拓扑感知：副本优先跨 zone 分散", () => {
  const nodes = makeNodes(9, ["z1", "z2", "z3"]);
  const ring = new ConsistentHashRing(nodes);
  for (let i = 0; i < 500; i++) {
    const replicas = ring.getReplicas(`key-${i}`, 3);
    assert.equal(replicas.length, 3);
    assert.equal(new Set(replicas).size, 3, "replicas must be distinct nodes");
    const zones = replicas.map((id) => ring.getNode(id)!.topology!.zone);
    assert.equal(new Set(zones).size, 3, "replicas must span distinct zones");
  }
});

test("zone 数量不足时放宽拓扑约束补齐副本", () => {
  const nodes = makeNodes(6, ["z1"]);
  const ring = new ConsistentHashRing(nodes);
  const replicas = ring.getReplicas("some-key", 3);
  assert.equal(replicas.length, 3);
  assert.equal(new Set(replicas).size, 3);
});

test("环不可变式更新：派生新环不影响原环", () => {
  const base = new ConsistentHashRing(makeNodes(4, ["z1"]));
  const derived = base.addNode({ id: "node-4", topology: { zone: "z2" } });
  assert.equal(base.size, 4);
  assert.equal(derived.size, 5);
  assert.ok(!base.hasNode("node-4"));
  assert.ok(derived.hasNode("node-4"));
});

test("重复增删节点抛出可读错误", () => {
  const ring = new ConsistentHashRing([{ id: "a" }]);
  assert.throws(() => ring.addNode({ id: "a" }), /already exists/);
  assert.throws(() => ring.removeNode("ghost"), /not found/);
});
