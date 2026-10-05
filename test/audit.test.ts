import assert from "node:assert/strict";
import { test } from "node:test";
import { audit } from "../src/audit.ts";
import { flawedDemoBuild, soundDemoBuild } from "../src/examples.ts";
import type { BuildSpec } from "../src/model.ts";

const result = audit(flawedDemoBuild);

test("识别未声明却影响输出的输入（环境变量 / 工具版本 / 条件读取）", () => {
  assert.deepEqual(
    [...result.undeclaredButUsed].sort(),
    ["env:FEATURE_X", "env:LOG_LEVEL", "read:extra.conf", "tool:compiler"].sort(),
  );
});

test("声明性分析：FLAG_A/FLAG_B 虽被有损编码，仍算声明", () => {
  assert.deepEqual(
    [...result.declared].sort(),
    ["env:FLAG_A", "env:FLAG_B", "env:NODE_ENV"].sort(),
  );
});

test("最小反例集合恰好是预期的 5 个", () => {
  const sets = result.counterexamples.map((c) => `${c.kind}:${c.vars.join("+")}`).sort();
  assert.deepEqual(sets, [
    "artifact:env:FEATURE_X",
    "artifact:env:FLAG_A+env:FLAG_B",
    "artifact:read:extra.conf",
    "artifact:tool:compiler",
    "log-only:env:LOG_LEVEL",
  ]);
});

test("每个反例两侧缓存键确实相同、输出确实不同", () => {
  for (const cex of result.counterexamples) {
    assert.equal(flawedDemoBuild.key(cex.a), cex.key);
    assert.equal(flawedDemoBuild.key(cex.b), cex.key);
    if (cex.kind === "artifact") {
      assert.notEqual(cex.outputA.artifact, cex.outputB.artifact);
    } else {
      assert.equal(cex.outputA.artifact, cex.outputB.artifact);
      assert.notDeepEqual(cex.outputA.logs, cex.outputB.logs);
    }
  }
});

test("条件读取的反例见证发生在条件满足的上下文（NODE_ENV=prod）", () => {
  const cex = result.counterexamples.find((c) => c.vars.includes("read:extra.conf"));
  assert.ok(cex);
  assert.equal(cex.a["env:NODE_ENV"], "prod");
  assert.equal(cex.b["env:NODE_ENV"], "prod");
});

test("最小性：不存在互为严格子集的两个反例差异集", () => {
  const sets = result.counterexamples.map((c) => c.vars);
  for (const s of sets) {
    for (const t of sets) {
      if (s === t) continue;
      const strictSubset =
        t.length < s.length && t.every((v) => s.includes(v));
      assert.equal(strictSubset, false, `${t} 是 ${s} 的严格子集`);
    }
  }
});

test("键编码有损的反例：FLAG_A/FLAG_B 联合翻转保持键不变", () => {
  const cex = result.counterexamples.find(
    (c) => c.vars.includes("env:FLAG_A") && c.vars.includes("env:FLAG_B"),
  );
  assert.ok(cex);
  assert.notEqual(cex.a["env:FLAG_A"], cex.b["env:FLAG_A"]);
  assert.notEqual(cex.a["env:FLAG_B"], cex.b["env:FLAG_B"]);
  assert.equal(cex.key, flawedDemoBuild.key(cex.a));
});

test("健全的键：枚举范围内无任何反例", () => {
  const clean = audit(soundDemoBuild);
  assert.equal(clean.counterexamples.length, 0);
  assert.equal(clean.undeclaredButUsed.length, 0);
});

test("log-only 与 artifact 的分类边界", () => {
  const spec: BuildSpec = {
    name: "synthetic",
    inputs: [
      { name: "env:NOISY", kind: "env", domain: ["x", "y"] },
      { name: "env:REAL", kind: "env", domain: ["1", "2"] },
    ],
    key: () => "constant",
    run: (a) => ({
      artifact: `out-${a["env:REAL"]}`,
      logs: [`noise=${a["env:NOISY"]}`],
    }),
  };
  const r = audit(spec);
  const byVar = new Map(r.counterexamples.map((c) => [c.vars[0], c.kind]));
  assert.equal(byVar.get("env:NOISY"), "log-only");
  assert.equal(byVar.get("env:REAL"), "artifact");
});

test("maxDistance 限制搜索深度", () => {
  const shallow = audit(flawedDemoBuild, { maxDistance: 1 });
  assert.ok(
    shallow.counterexamples.every((c) => c.vars.length === 1),
    "距离 1 时不应出现多变量的键碰撞反例",
  );
  assert.ok(
    !shallow.counterexamples.some((c) => c.vars.includes("env:FLAG_A")),
  );
});
