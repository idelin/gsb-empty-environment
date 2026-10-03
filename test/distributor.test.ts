import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { CapacityAwareDistributor } from "../src/distributor.ts";
import { healthFactor, weightFor } from "../src/weight-policy.ts";

const KEY_COUNT = 20_000;
const keys = Array.from({ length: KEY_COUNT }, (_, i) => `request-${i}`);

function distribute(d: CapacityAwareDistributor): Map<string, number> {
  const counts = new Map<string, number>();
  for (const key of keys) {
    counts.set(d.route(key), (counts.get(d.route(key)) ?? 0) + 1);
  }
  return counts;
}

function share(counts: Map<string, number>, id: string): number {
  return (counts.get(id) ?? 0) / KEY_COUNT;
}

describe("weight policy", () => {
  it("keeps full weight at 30% cpu", () => {
    assert.equal(healthFactor({ cpu: 0.3, memory: 0.2 }), 1);
    assert.equal(weightFor({ cpu: 0.3, memory: 0.2 }), 100);
  });

  it("cuts weight to 20 at 90% cpu", () => {
    assert.equal(healthFactor({ cpu: 0.9, memory: 0.4 }), 0.2);
    assert.equal(weightFor({ cpu: 0.9, memory: 0.4 }), 20);
  });

  it("uses the bottleneck dimension (max of cpu/memory)", () => {
    assert.equal(weightFor({ cpu: 0.3, memory: 0.9 }), 20);
  });

  it("decays linearly between the soft threshold and full load", () => {
    assert.equal(weightFor({ cpu: 0.75, memory: 0 }), 50);
  });
});

describe("capacity-aware consistent hash distributor", () => {
  it("balances traffic roughly evenly across healthy nodes", () => {
    const d = new CapacityAwareDistributor();
    d.addNode("A");
    d.addNode("B");
    d.addNode("C");
    d.runCycle();
    const counts = distribute(d);
    for (const id of ["A", "B", "C"]) {
      const s = share(counts, id);
      assert.ok(s > 0.25 && s < 0.42, `${id} share ${s} should be near 1/3`);
    }
  });

  it("routes the same key to the same node within a cycle", () => {
    const d = new CapacityAwareDistributor();
    d.addNode("A");
    d.addNode("B");
    d.runCycle();
    for (const key of keys.slice(0, 1000)) {
      assert.equal(d.route(key), d.route(key));
    }
  });

  it("acceptance: A spikes 30% -> 90%, weight 100 -> 20, traffic collapses", () => {
    const d = new CapacityAwareDistributor();
    d.addNode("A", { cpu: 0.3, memory: 0.3 });
    d.addNode("B", { cpu: 0.3, memory: 0.3 });
    d.addNode("C", { cpu: 0.3, memory: 0.3 });
    d.runCycle();

    const before = distribute(d);
    const aBefore = share(before, "A");
    assert.ok(aBefore > 0.25, `A should carry ~1/3 before the spike, got ${aBefore}`);

    // CPU of A suddenly spikes to 90%; the next sensing cycle reacts.
    d.reportMetrics("A", { cpu: 0.9, memory: 0.3 });
    const report = d.runCycle();

    const nodeA = report.nodes.find((n) => n.id === "A")!;
    assert.equal(nodeA.weight, 20, "A's virtual-node weight must drop 100 -> 20");
    assert.equal(report.nodes.find((n) => n.id === "B")!.weight, 100);
    assert.equal(report.nodes.find((n) => n.id === "C")!.weight, 100);

    const after = distribute(d);
    const aAfter = share(after, "A");
    assert.ok(
      aAfter < aBefore * 0.35,
      `A's traffic must collapse: ${aBefore} -> ${aAfter}`,
    );
    assert.ok(aAfter < 0.12, `A's share should be well under 12%, got ${aAfter}`);

    // The shed traffic floods to the neighbours; total is conserved.
    const lostByA = (before.get("A") ?? 0) - (after.get("A") ?? 0);
    const gainedByOthers =
      (after.get("B")! - before.get("B")!) + (after.get("C")! - before.get("C")!);
    assert.equal(gainedByOthers, lostByA);
    assert.ok(share(after, "B") > share(before, "B"));
    assert.ok(share(after, "C") > share(before, "C"));
  });

  it("keeps unaffected nodes' keys stable when A drains (minimal disruption)", () => {
    const d = new CapacityAwareDistributor();
    d.addNode("A", { cpu: 0.3, memory: 0.3 });
    d.addNode("B", { cpu: 0.3, memory: 0.3 });
    d.addNode("C", { cpu: 0.3, memory: 0.3 });
    d.runCycle();

    const beforeRoutes = new Map(keys.map((k) => [k, d.route(k)]));
    d.reportMetrics("A", { cpu: 0.9, memory: 0.3 });
    d.runCycle();

    let moved = 0;
    let movedFromA = 0;
    for (const key of keys) {
      const before = beforeRoutes.get(key)!;
      const after = d.route(key);
      if (before !== after) {
        moved++;
        if (before === "A") movedFromA++;
      }
    }
    // Consistent hashing: keys not on A should (almost) never move.
    assert.ok(
      movedFromA / moved > 0.9,
      `most moved keys should come from A, got ${movedFromA}/${moved}`,
    );
  });

  it("recovers when A's load drops back", () => {
    const d = new CapacityAwareDistributor();
    d.addNode("A", { cpu: 0.3, memory: 0.3 });
    d.addNode("B", { cpu: 0.3, memory: 0.3 });
    d.runCycle();
    d.reportMetrics("A", { cpu: 0.9, memory: 0.3 });
    d.runCycle();
    assert.equal(d.snapshot().find((n) => n.id === "A")!.weight, 20);

    d.reportMetrics("A", { cpu: 0.3, memory: 0.3 });
    d.runCycle();
    assert.equal(d.snapshot().find((n) => n.id === "A")!.weight, 100);
    const counts = distribute(d);
    assert.ok(share(counts, "A") > 0.4, "A should take its share back");
  });
});
