import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { CapacityController, DEFAULT_POLICY, healthFactor } from "../src/controller.ts";
import { ConsistentHashRing } from "../src/ring.ts";

describe("CapacityController 权重换算", () => {
  it("CPU 30%（低于软阈值）保持满权重 100", () => {
    const c = new CapacityController(new ConsistentHashRing());
    assert.equal(c.targetWeight({ cpu: 0.3, memory: 0.2 }), 100);
  });

  it("CPU 90% 骤升后权重削减至 20（验收边界）", () => {
    const c = new CapacityController(new ConsistentHashRing());
    assert.equal(c.targetWeight({ cpu: 0.9, memory: 0.2 }), 20);
  });

  it("内存压力同样触发收缩（取 CPU/内存较大者）", () => {
    const c = new CapacityController(new ConsistentHashRing());
    assert.equal(c.targetWeight({ cpu: 0.3, memory: 0.9 }), 20);
  });

  it("健康度因子钳制在下限之上，节点不会被完全摘除", () => {
    assert.equal(healthFactor(0.99), DEFAULT_POLICY.minFactor);
    assert.equal(healthFactor(1.0), DEFAULT_POLICY.minFactor);
  });

  it("reconcile 将权重应用到哈希环的虚拟节点数", () => {
    const ring = new ConsistentHashRing();
    const c = new CapacityController(ring);
    c.reportLoad("A", { cpu: 0.3, memory: 0.2 });
    c.reportLoad("B", { cpu: 0.9, memory: 0.2 });
    const applied = c.reconcile();
    assert.equal(applied.get("A"), 100);
    assert.equal(applied.get("B"), 20);
    assert.equal(ring.getVNodeCount("A"), 100);
    assert.equal(ring.getVNodeCount("B"), 20);
  });
});
