import assert from "node:assert/strict";
import { test } from "node:test";
import { PrivacyGate, type ResourceEvent } from "./privacy-gate.ts";

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

function config() {
  return {
    secrets: ["s0", "s1", "s2", "s3"].map((id) => ({ id, prior: [1, 4] as [number, number] })),
    modules: [moduleSpec("m", [4, 5], [1, 15])],
  };
}

test("合并按共同 eventId 去重、其余按原始顺序追加，且整体可回滚", () => {
  const left = new PrivacyGate(config());
  left.propose({
    eventId: "shared",
    moduleId: "m",
    observedResourceId: "xA",
    resolution: { c0: "xA", c1: "xA", c2: "yA", c3: "yA" },
  });
  left.propose({
    eventId: "left-only",
    moduleId: "m",
    observedResourceId: "xA",
    resolution: { c0: "xA", c1: "xA", c2: "yA", c3: "yA" },
  });

  const right = new PrivacyGate(config());
  right.propose({
    eventId: "shared",
    moduleId: "m",
    observedResourceId: "h1",
    resolution: { c0: "h1", c1: "h1", c2: "h2", c3: "h2" },
  });
  right.propose({
    eventId: "right-only",
    moduleId: "m",
    observedResourceId: "xA",
    resolution: { c0: "xA", c1: "xA", c2: "yA", c3: "yA" },
  });

  const before = left.publicLog().map((event) => event.eventId);
  const report = left.merge(right);
  assert.equal(report.merged, true);
  assert.equal(report.replayed, 1);
  assert.equal(report.appended, 1);
  assert.deepEqual(left.recordedEventIds(), ["shared", "left-only", "right-only"]);
  assert.equal(left.publicLog().length, 3);

  const hostile = new PrivacyGate(config());
  hostile.propose({
    eventId: "shared",
    moduleId: "m",
    observedResourceId: "k2",
    resolution: { c0: "k1", c1: "k1", c2: "k2", c3: "k2" },
  });
  const rollbackGate = new PrivacyGate(config());
  rollbackGate.propose({
    eventId: "shared",
    moduleId: "m",
    observedResourceId: "xA",
    resolution: { c0: "xA", c1: "xA", c2: "yA", c3: "yA" },
  });
  const rollback = rollbackGate.merge(hostile);
  assert.equal(rollback.merged, false);
  assert.deepEqual(rollbackGate.publicLog().map((event) => event.eventId), ["shared"]);
  assert.deepEqual(
    rollbackGate.publicLog().map((event) => event.eventId),
    before.filter((id) => id === "shared"),
  );
});

test("重放（一次抽样两投影）与两次独立抽样给出不同后验", () => {
  const replayGate = new PrivacyGate(config());
  const first: ResourceEvent = {
    eventId: "e1",
    moduleId: "m",
    observedResourceId: "yA",
    resolution: { c0: "xA", c1: "xA", c2: "yA", c3: "yA" },
  };
  replayGate.propose(first);
  replayGate.propose({
    eventId: "e1",
    moduleId: "m",
    observedResourceId: "zB",
    resolution: { c0: "xB", c1: "xB", c2: "zB", c3: "zB" },
  });

  const independentGate = new PrivacyGate(config());
  independentGate.propose(first);
  independentGate.propose({
    eventId: "e2",
    moduleId: "m",
    observedResourceId: "zB",
    resolution: { c0: "xB", c1: "xB", c2: "zB", c3: "zB" },
  });

  for (const secret of ["s0", "s1", "s2", "s3"]) {
    assert.notEqual(
      replayGate.posteriors()[secret].toString(),
      independentGate.posteriors()[secret].toString(),
      `${secret} 应区分一次抽样与两次独立抽样`,
    );
  }
});
