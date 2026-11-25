import assert from "node:assert/strict";
import { test } from "node:test";
import { Fraction } from "./fraction.ts";
import {
  PrivacyGate,
  type GateConfig,
  type ModuleSpec,
  type ResourceEvent,
} from "./privacy-gate.ts";
import { enumerateTree } from "./event-tree.ts";

const f = (num: bigint | number, den: bigint | number = 1) => new Fraction(num, den);

/** 四个等先验秘密、四个候选编号的对称模块。 */
function symmetricModule(
  id: string,
  diagonal: [number, number],
  offDiagonal: [number, number],
): ModuleSpec {
  const [dn, dd] = diagonal;
  const [on, od] = offDiagonal;
  const candidates = ["c0", "c1", "c2", "c3"];
  const table: ModuleSpec["table"] = {};
  for (let secret = 0; secret < 4; secret += 1) {
    table[`s${secret}`] = {};
    for (let candidate = 0; candidate < 4; candidate += 1) {
      table[`s${secret}`][`c${candidate}`] =
        candidate === secret ? [dn, dd] : [on, od];
    }
  }
  return { id, candidates, table };
}

function fourSecretConfig(modules: ModuleSpec[], bound = f(3)): GateConfig {
  return {
    secrets: ["s0", "s1", "s2", "s3"].map((id) => ({ id, prior: [1, 4] })),
    modules,
    bound,
  };
}

test("精确分数：约分、四则与严格比较，分母恰好相等可判定", () => {
  assert.equal(f(2, 4).toString(), "1/2");
  assert.equal(f(1, 3).add(f(1, 6)).toString(), "1/2");
  assert.equal(f(2, 3).mul(f(3, 2)).toString(), "1");
  assert.equal(f(5, 6).div(f(5, 2)).toString(), "1/3");
  assert.equal(f(3, 1).compareTo(f(9, 3)), 0);
  assert.equal(f(16, 5).compareTo(f(3)), 1);
  assert.throws(() => f(1, 0));
});

test("无公开证据时后验等于先验，worst ratio 为 1", () => {
  const gate = new PrivacyGate(fourSecretConfig([symmetricModule("m", [4, 5], [1, 15])]));
  const worst = gate.currentWorst();
  assert.equal(worst.posterior.toString(), "1/4");
  assert.equal(worst.ratio.toString(), "1");
  assert.deepEqual(worst.evidence, []);
});

test("配置校验：先验为正且和为 1，模块每行概率和为 1", () => {
  assert.throws(() => new PrivacyGate({
    secrets: [{ id: "a", prior: [0, 1] }],
    modules: [],
  }));
  const bad: ModuleSpec = {
    id: "bad",
    candidates: ["c0", "c1"],
    table: { s0: { c0: [1, 2], c1: [1, 3] } },
  };
  assert.throws(() => new PrivacyGate({
    secrets: [{ id: "s0", prior: [1, 1] }],
    modules: [bad],
  }));
});

test("边际各自合规、联合原像后越界：单站点放行，合并被拒", () => {
  const moduleSpec = symmetricModule("m", [4, 5], [1, 15]);
  const config = fourSecretConfig([moduleSpec]);
  const gateA = new PrivacyGate(config);
  const gateB = new PrivacyGate(config);
  const eventA: ResourceEvent = {
    eventId: "e1",
    moduleId: "m",
    observedResourceId: "xA",
    resolution: { c0: "xA", c1: "xA", c2: "yA", c3: "yA" },
  };
  const eventB: ResourceEvent = {
    eventId: "e1",
    moduleId: "m",
    observedResourceId: "xB",
    resolution: { c0: "xB", c1: "yB", c2: "xB", c3: "yB" },
  };
  const resultA = gateA.propose(eventA);
  const resultB = gateB.propose(eventB);
  assert.equal(resultA.admitted, true);
  assert.equal(resultB.admitted, true);

  const marginalWorst = gateA.currentWorst();
  assert.equal(marginalWorst.ratio.toString(), "26/15");
  assert.ok(marginalWorst.ratio.compareTo(f(3)) < 0);

  const mergeGate = new PrivacyGate(config);
  mergeGate.propose(eventA);
  const report = mergeGate.merge(gateB);
  assert.equal(report.merged, false);
  assert.deepEqual(mergeGate.recordedEventIds(), ["e1"]);
  assert.ok(report.reason?.includes("s0"));

  const jointGate = new PrivacyGate(config);
  jointGate.propose(eventA);
  const jointReplay = jointGate.propose({ ...eventB, observedResourceId: "xB" });
  assert.equal(jointReplay.admitted, false);
  assert.equal(jointReplay.publicOutput.resourceId, jointGate.rejectionResourceId);
  assert.equal(jointReplay.outcomes[0].worstRatio.toString(), "16/5");
  assert.equal(jointReplay.outcomes[0].worstPosterior.toString(), "4/5");
  assert.equal(jointReplay.outcomes[0].worstSecret, "s0");
  assert.deepEqual(jointGate.recordedEventIds(), ["e1"]);
});

test("同一次抽样的两个投影取交集原像，不按两次独立抽样连乘", () => {
  const moduleSpec = symmetricModule("m", [4, 5], [1, 15]);
  const config = fourSecretConfig([moduleSpec]);
  const gate = new PrivacyGate(config);
  gate.propose({
    eventId: "e1",
    moduleId: "m",
    observedResourceId: "yA",
    resolution: { c0: "xA", c1: "xA", c2: "yA", c3: "yA" },
  });
  const replay = gate.propose({
    eventId: "e1",
    moduleId: "m",
    observedResourceId: "zB",
    resolution: { c0: "xB", c1: "xB", c2: "zB", c3: "zB" },
  });
  assert.equal(replay.admitted, true);
  assert.deepEqual(replay.outcomes[0].allowedCandidates, ["c2", "c3"]);
  const posteriors = gate.posteriors();
  assert.equal(posteriors.s0.toString(), "1/15");
  assert.equal(posteriors.s1.toString(), "1/15");
  assert.equal(posteriors.s2.toString(), "13/30");
  assert.equal(posteriors.s3.toString(), "13/30");

  const doubleCounted = new PrivacyGate(config);
  doubleCounted.propose({
    eventId: "e1",
    moduleId: "m",
    observedResourceId: "xA",
    resolution: { c0: "xA", c1: "xA", c2: "yA", c3: "yA" },
  });
  doubleCounted.propose({
    eventId: "e2",
    moduleId: "m",
    observedResourceId: "xB",
    resolution: { c0: "xB", c1: "yB", c2: "xB", c3: "yB" },
  });
  assert.notEqual(doubleCounted.posteriors().s0.toString(), posteriors.s0.toString());
});

function deterministicModule(): ModuleSpec {
  return {
    id: "det",
    candidates: ["c0", "c1", "c2", "c3"],
    table: {
      s0: { c0: [1, 1], c1: [0, 1], c2: [0, 1], c3: [0, 1] },
      s1: { c0: [0, 1], c1: [1, 1], c2: [0, 1], c3: [0, 1] },
      s2: { c0: [0, 1], c1: [0, 1], c2: [1, 1], c3: [0, 1] },
      s3: { c0: [0, 1], c1: [0, 1], c2: [0, 1], c3: [1, 1] },
    },
  };
}

test("任一可能结果超额 => 全秘密同一固定拒绝，无部分扣费、无差异化错误码", () => {
  const config = fourSecretConfig([deterministicModule()]);
  const gate = new PrivacyGate(config);
  const result = gate.propose({
    eventId: "e1",
    moduleId: "det",
    observedResourceId: "r0",
    resolution: { c0: "r0", c1: "r1", c2: "r2", c3: "r3" },
  });
  assert.equal(result.admitted, false);
  assert.equal(result.publicOutput.rejection, true);
  assert.equal(result.publicOutput.resourceId, gate.rejectionResourceId);
  assert.equal(gate.rejections(), 1);
  assert.deepEqual(gate.recordedEventIds(), []);
  assert.deepEqual(gate.publicLog(), []);
  assert.equal(gate.currentWorst().ratio.toString(), "1");
  assert.ok(result.reason?.includes("s0"));
});

test("零概率分支：穷举跳过、不可能结果被标记，不发生除零", () => {
  const config = fourSecretConfig([deterministicModule()]);
  const gate = new PrivacyGate(config);
  const report = gate.hypothetical(
    {
      eventId: "trial",
      moduleId: "det",
      resolution: { c0: "r0", c1: "r1", c2: "r2", c3: "r3" },
    },
    "r0",
  );
  assert.equal(report.impossible, false);
  assert.equal(report.likelihoods.s1.toString(), "0");
  assert.equal(report.posteriors.s1.toString(), "0");

  const enumerated = enumerateTree(
    new PrivacyGate(config),
    () => ({
      eventId: "only",
      moduleId: "det",
      resolution: { c0: "r0", c1: "r1", c2: "r2", c3: "r3" },
    }),
    1,
  );
  assert.equal(enumerated.leaves.length, 1);
  assert.equal(enumerated.leaves[0].admitted, false);
});

test("条件退避空观测：空桶按相容候选概率和计分，仍携带似然信息", () => {
  const moduleSpec: ModuleSpec = {
    id: "backoff",
    candidates: ["c0", "c1", "c2"],
    table: {
      s0: { c0: [1, 2], c1: [1, 4], c2: [1, 4] },
      s1: { c0: [1, 4], c1: [1, 2], c2: [1, 4] },
      s2: { c0: [1, 4], c1: [1, 4], c2: [1, 2] },
    },
  };
  const config = {
    secrets: ["s0", "s1", "s2"].map((id) => ({ id, prior: [1, 3] })),
    modules: [moduleSpec],
  } as const;
  const event: ResourceEvent = {
    eventId: "e1",
    moduleId: "backoff",
    observedResourceId: "visible",
    resolution: { c0: "visible", c1: null, c2: null },
  };
  const gate = new PrivacyGate(config);
  const result = gate.propose(event);
  assert.equal(result.admitted, true);
  const empty = result.outcomes.find((outcome) => outcome.resourceId === null)!;
  assert.deepEqual(empty.allowedCandidates, ["c1", "c2"]);
  const emptyReport = gate.hypothetical(event, null);
  assert.equal(emptyReport.posteriors.s0.toString(), "2/5");
  assert.equal(emptyReport.posteriors.s1.toString(), "3/10");
  assert.equal(emptyReport.posteriors.s2.toString(), "3/10");

  const emptyGate = new PrivacyGate(config);
  const allBackoff: ResourceEvent = {
    eventId: "e1",
    moduleId: "backoff",
    observedResourceId: null,
    resolution: { c0: null, c1: null, c2: null },
  };
  emptyGate.propose(allBackoff);
  const posteriors = gate.posteriors();
  const emptyPosteriors = emptyGate.posteriors();
  assert.equal(emptyPosteriors.s0.toString(), "1/3");
  assert.equal(emptyPosteriors.s1.toString(), "1/3");
  assert.equal(emptyPosteriors.s2.toString(), "1/3");
  assert.equal(posteriors.s0.toString(), "1/2");
  assert.equal(posteriors.s1.toString(), "1/4");
  assert.equal(posteriors.s2.toString(), "1/4");
  assert.equal(emptyGate.publicLog()[0].resourceId, null);
});

test("分母恰好等于三倍：等于 bound 放行，严格大于才拒绝", () => {
  const moduleSpec: ModuleSpec = {
    id: "exact",
    candidates: ["c0", "c1", "c2"],
    table: {
      s0: { c0: [1, 1], c1: [0, 1], c2: [0, 1] },
      s1: { c0: [0, 1], c1: [1, 1], c2: [0, 1] },
      s2: { c0: [0, 1], c1: [0, 1], c2: [1, 1] },
    },
  };
  const gate = new PrivacyGate({
    secrets: ["s0", "s1", "s2"].map((id) => ({ id, prior: [1, 3] })),
    modules: [moduleSpec],
  });
  const result = gate.propose({
    eventId: "e1",
    moduleId: "exact",
    observedResourceId: "r0",
    resolution: { c0: "r0", c1: "r1", c2: "r2" },
  });
  assert.equal(result.admitted, true);
  assert.equal(gate.currentWorst().posterior.toString(), "1");
  assert.equal(gate.currentWorst().ratio.toString(), "3");
  assert.equal(gate.currentWorst().ratio.compareTo(f(3)), 0);
});

test("重放：相同 eventId 与相同投影不改变后验，重复事件不计数", () => {
  const moduleSpec = symmetricModule("m", [4, 5], [1, 15]);
  const config = fourSecretConfig([moduleSpec]);
  const gate = new PrivacyGate(config);
  const event: ResourceEvent = {
    eventId: "e1",
    moduleId: "m",
    observedResourceId: "xA",
    resolution: { c0: "xA", c1: "xA", c2: "yA", c3: "yA" },
  };
  gate.propose(event);
  const before = gate.posteriors();
  const replay = gate.propose({ ...event, observedResourceId: "xA" });
  assert.equal(replay.admitted, true);
  assert.deepEqual(gate.recordedEventIds(), ["e1"]);
  assert.equal(gate.publicLog().length, 1);
  for (const secret of Object.keys(before)) {
    assert.equal(gate.posteriors()[secret].toString(), before[secret].toString());
  }
});
