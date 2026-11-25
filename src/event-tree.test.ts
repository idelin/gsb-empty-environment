import assert from "node:assert/strict";
import { test } from "node:test";
import { Fraction } from "./fraction.ts";
import { PrivacyGate, type ResourceEvent } from "./privacy-gate.ts";
import { enumerateTree } from "./event-tree.ts";

const f = (num: bigint | number, den: bigint | number = 1) => new Fraction(num, den);

function moduleSpec(id: string, diagonal: [number, number], off: [number, number]) {
  const candidates = ["c0", "c1", "c2", "c3"];
  const table: Record<string, Record<string, [number, number]>> = {};
  for (let s = 0; s < 4; s += 1) {
    table[`s${s}`] = {};
    for (let c = 0; c < 4; c += 1) {
      table[`s${s}`][`c${c}`] = c === s ? diagonal : off;
    }
  }
  return { id, candidates, table };
}

function gate() {
  return new PrivacyGate({
    secrets: ["s0", "s1", "s2", "s3"].map((id) => ({ id, prior: [1, 4] })),
    modules: [moduleSpec("m", [4, 5], [1, 15])],
    bound: f(3),
  });
}

test("穷举单步树：每个可区分资源一个节点，混合概率和为 1，边际均合规", () => {
  const report = enumerateTree(gate(), () => ({
    eventId: "e1",
    moduleId: "m",
    resolution: { c0: "xA", c1: "xA", c2: "yA", c3: "yA" },
  }), 1);
  assert.equal(report.nodes.length, 3);
  assert.equal(report.reachableMass.toString(), "1");
  assert.equal(report.allWithinBound, true);
  assert.equal(report.worst.worstRatio.toString(), "26/15");
  const leafMass = report.leaves
    .map((leaf) => leaf.mixtureProbability)
    .reduce((acc, value) => acc.add(value), Fraction.ZERO);
  assert.equal(leafMass.toString(), "1");
});

test("穷举自适应策略：依据上一步公开证据选择模块，并在深度上限停止", () => {
  const adaptiveGate = new PrivacyGate({
    secrets: ["s0", "s1", "s2", "s3"].map((id) => ({ id, prior: [1, 4] })),
    modules: [
      moduleSpec("m1", [4, 5], [1, 15]),
      moduleSpec("m2", [2, 3], [1, 9]),
    ],
    bound: f(3),
  });
  const report = enumerateTree(adaptiveGate, (evidence) => {
    const event: ResourceEvent = {
      eventId: `step-${evidence.length}`,
      moduleId: evidence.length === 0 ? "m1" : "m2",
      resolution: { c0: "x", c1: "x", c2: "y", c3: "y" },
    };
    return event;
  }, 2);
  assert.ok(report.nodes.every((node) => node.depth <= 2));
  assert.equal(new Set(report.leaves.map((leaf) => leaf.depth)).size, 1);
  assert.equal(report.leaves[0].depth, 2);
});

test("穷举器识别会越界的模块：出现拒绝叶子且状态未被修改", () => {
  const deterministic = {
    id: "det",
    candidates: ["c0", "c1", "c2", "c3"],
    table: {
      s0: { c0: [1, 1], c1: [0, 1], c2: [0, 1], c3: [0, 1] },
      s1: { c0: [0, 1], c1: [1, 1], c2: [0, 1], c3: [0, 1] },
      s2: { c0: [0, 1], c1: [0, 1], c2: [1, 1], c3: [0, 1] },
      s3: { c0: [0, 1], c1: [0, 1], c2: [0, 1], c3: [1, 1] },
    },
  };
  const hostileGate = new PrivacyGate({
    secrets: ["s0", "s1", "s2", "s3"].map((id) => ({ id, prior: [1, 4] })),
    modules: [deterministic],
    bound: f(3),
  });
  const report = enumerateTree(hostileGate, () => ({
    eventId: "e1",
    moduleId: "det",
    resolution: { c0: "r0", c1: "r1", c2: "r2", c3: "r3" },
  }), 1);
  assert.equal(report.allWithinBound, false);
  assert.equal(report.leaves[0].admitted, false);
  assert.deepEqual(hostileGate.publicLog(), []);
});
