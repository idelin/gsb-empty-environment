import { test } from "node:test";
import assert from "node:assert/strict";
import { HashRing, fnv1a, type RingNode } from "../src/hash-ring.ts";

function makeNodes(count: number, zones = 3): RingNode[] {
  return Array.from({ length: count }, (_, i) => ({
    id: `node-${i}`,
    zone: `zone-${i % zones}`,
  }));
}

test("lookup is deterministic and hits only member nodes", () => {
  const ring = new HashRing(makeNodes(9));
  const ids = new Set(ring.nodeIds());
  for (let i = 0; i < 5000; i++) {
    const key = `key-${i}`;
    const owner = ring.lookup(key);
    assert.ok(ids.has(owner));
    assert.equal(ring.lookup(key), owner);
  }
});

test("lookup throws on an empty ring", () => {
  const ring = new HashRing();
  assert.throws(() => ring.lookup("k"), /empty/);
});

test("load is balanced across nodes within statistical tolerance", () => {
  const nodes = makeNodes(8);
  const ring = new HashRing(nodes);
  const counts = new Map<string, number>();
  const total = 100_000;
  for (let i = 0; i < total; i++) {
    const owner = ring.lookup(`key-${i}`);
    counts.set(owner, (counts.get(owner) ?? 0) + 1);
  }
  const expected = total / nodes.length;
  for (const node of nodes) {
    const got = counts.get(node.id) ?? 0;
    assert.ok(
      Math.abs(got - expected) / expected < 0.25,
      `${node.id} holds ${got} keys, expected ~${expected}`,
    );
  }
});

test("removing one node moves roughly 1/N of keys and nothing else", () => {
  const nodes = makeNodes(10);
  const before = new HashRing(nodes);
  const after = before.clone();
  after.removeNode("node-3");

  let moved = 0;
  const total = 50_000;
  for (let i = 0; i < total; i++) {
    const key = `key-${i}`;
    const from = before.lookup(key);
    const to = after.lookup(key);
    if (from !== to) {
      moved++;
      assert.equal(from, "node-3", "only keys of the removed node may move");
    }
  }
  const ratio = moved / total;
  assert.ok(ratio > 0.05 && ratio < 0.18, `move ratio ${ratio} out of bounds`);
});

test("lookupDistinct prefers distinct zones (topology-aware spread)", () => {
  const ring = new HashRing(makeNodes(9, 3));
  for (let i = 0; i < 2000; i++) {
    const replicas = ring.lookupDistinct(`key-${i}`, 3);
    assert.equal(replicas.length, 3);
    assert.equal(new Set(replicas).size, 3, "replicas must be distinct nodes");
    const zones = replicas.map((id) => ring.zoneOf(id));
    assert.equal(new Set(zones).size, 3, "replicas must span distinct zones");
  }
});

test("clone is independent from the source ring", () => {
  const ring = new HashRing(makeNodes(6));
  const copy = ring.clone();
  copy.removeNode("node-0");
  assert.ok(ring.hasNode("node-0"));
  assert.ok(!copy.hasNode("node-0"));
  assert.notEqual(ring.version, copy.version);
});

test("diffOwnership reports exactly the changed vnode ranges", () => {
  const before = new HashRing(makeNodes(6));
  const after = before.clone();
  after.removeNode("node-2");
  const changes = before.diffOwnership(after);
  assert.ok(changes.length > 0);
  for (const change of changes) {
    assert.equal(change.from, "node-2");
    assert.notEqual(change.to, "node-2");
  }
});

test("fnv1a is stable and well distributed over the 32-bit space", () => {
  assert.equal(fnv1a("hello"), fnv1a("hello"));
  let buckets = 0;
  const seen = new Set<number>();
  for (let i = 0; i < 10_000; i++) seen.add(fnv1a(`k${i}`) >>> 28);
  for (let b = 0; b < 16; b++) if (seen.has(b)) buckets++;
  assert.equal(buckets, 16, "top 4 bits should cover all 16 buckets");
});
