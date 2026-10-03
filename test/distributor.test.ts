import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { LoadAwareDistributor } from "../src/distributor.ts";

const KEY_COUNT = 100_000;
const allKeys = Array.from({ length: KEY_COUNT }, (_, i) => `user-${i}`);

describe("验收场景：节点 A CPU 30% → 90%，自动分流泻洪", () => {
  it("A 的虚拟节点权重由 100 削减至 20，承载流量大幅骤降", () => {
    const d = new LoadAwareDistributor();
    d.addNode("A", { cpu: 0.3, memory: 0.3 });
    d.addNode("B", { cpu: 0.3, memory: 0.3 });
    d.addNode("C", { cpu: 0.3, memory: 0.3 });

    // 基线：三节点等权，A 承载约 1/3 流量
    const before = new Map<string, string>();
    for (const k of allKeys) before.set(k, d.route(k));
    const aBefore = [...before.values()].filter((n) => n === "A").length / KEY_COUNT;
    assert.ok(aBefore > 0.28 && aBefore < 0.39, `baseline A share=${aBefore}`);

    // A 的 CPU 骤升至 90%，下一个调控周期生效
    d.updateLoad("A", { cpu: 0.9, memory: 0.3 });
    const weights = d.cycle();
    assert.equal(weights.get("A"), 20, "A 的虚拟节点权重应削减至 20");
    assert.equal(weights.get("B"), 100);
    assert.equal(weights.get("C"), 100);

    // 泻洪后：A 承载流量大幅骤降（预期 ≈ 20/220 ≈ 9%）
    const after = new Map<string, string>();
    for (const k of allKeys) after.set(k, d.route(k));
    const aAfter = [...after.values()].filter((n) => n === "A").length / KEY_COUNT;
    assert.ok(aAfter < 0.12, `A 的流量应骤降，实际 share=${aAfter}`);
    assert.ok(aAfter < aBefore / 3, "A 的流量应降至原来的 1/3 以下");

    // 分流去向：B、C 承接了 A 溢出的流量，总量守恒（无请求丢失，避免雪崩）
    const bAfter = [...after.values()].filter((n) => n === "B").length / KEY_COUNT;
    const cAfter = [...after.values()].filter((n) => n === "C").length / KEY_COUNT;
    assert.ok(bAfter > 0.38 && cAfter > 0.38, `B=${bAfter} C=${cAfter}`);
    assert.ok(Math.abs(aAfter + bAfter + cAfter - 1) < 1e-9);

    // 最小扰动：原本落在 B/C 上的键 100% 不迁移（一致性哈希性质）
    let stable = 0;
    let bcTotal = 0;
    for (const k of allKeys) {
      if (before.get(k) !== "A") {
        bcTotal++;
        if (after.get(k) === before.get(k)) stable++;
      }
    }
    assert.equal(stable, bcTotal, "B/C 上的键不应发生迁移");
  });

  it("负载恢复后权重回升，流量重新均衡", () => {
    const d = new LoadAwareDistributor();
    d.addNode("A", { cpu: 0.9, memory: 0.3 });
    d.addNode("B", { cpu: 0.3, memory: 0.3 });
    assert.equal(d.ring.getVNodeCount("A"), 20);

    d.updateLoad("A", { cpu: 0.3, memory: 0.3 });
    d.cycle();
    assert.equal(d.ring.getVNodeCount("A"), 100);
  });
});
