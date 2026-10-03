import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { ConsistentHashRing } from "../src/ring.ts";

function keys(n: number): string[] {
  return Array.from({ length: n }, (_, i) => `user-${i}`);
}

describe("ConsistentHashRing", () => {
  it("空环返回 undefined", () => {
    assert.equal(new ConsistentHashRing().getNode("k"), undefined);
  });

  it("同一键路由结果稳定", () => {
    const ring = new ConsistentHashRing();
    ring.setNodeWeight("A", 100);
    ring.setNodeWeight("B", 100);
    assert.equal(ring.getNode("user-42"), ring.getNode("user-42"));
  });

  it("虚拟节点数量等于权重", () => {
    const ring = new ConsistentHashRing();
    ring.setNodeWeight("A", 100);
    ring.setNodeWeight("B", 20);
    assert.equal(ring.getVNodeCount("A"), 100);
    assert.equal(ring.getVNodeCount("B"), 20);
    assert.equal(ring.size, 120);
  });

  it("等权节点流量分布大致均匀", () => {
    const ring = new ConsistentHashRing();
    for (const id of ["A", "B", "C"]) ring.setNodeWeight(id, 100);
    const counts = new Map<string, number>();
    for (const k of keys(100_000)) {
      const node = ring.getNode(k)!;
      counts.set(node, (counts.get(node) ?? 0) + 1);
    }
    for (const id of ["A", "B", "C"]) {
      const share = counts.get(id)! / 100_000;
      assert.ok(share > 0.28 && share < 0.39, `${id} share=${share}`);
    }
  });

  it("权重为 0 时节点被摘除", () => {
    const ring = new ConsistentHashRing();
    ring.setNodeWeight("A", 100);
    ring.setNodeWeight("A", 0);
    assert.equal(ring.getVNodeCount("A"), 0);
    assert.equal(ring.size, 0);
  });
});
