import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { checkExportPlan } from "../src/checker.ts";
import { Sensitivity } from "../src/model.ts";
import type { ExportPlan, PlanNode } from "../src/model.ts";

const sources: PlanNode[] = [
  { kind: "source", id: "name", field: "name", level: Sensitivity.Confidential, population: 10000 },
  { kind: "source", id: "phone", field: "phone", level: Sensitivity.Secret, population: 10000 },
  { kind: "source", id: "income", field: "income", level: Sensitivity.Confidential, population: 10000 },
  { kind: "source", id: "city", field: "city", level: Sensitivity.Internal, population: 10000 },
];

function planWith(nodes: PlanNode[], exports: ExportPlan["exports"]): ExportPlan {
  return { name: "test", nodes: [...sources, ...nodes], exports };
}

describe("敏感级别沿计算依赖传播", () => {
  it("派生字段继承所有输入的最高级别", () => {
    const plan = planWith(
      [{ kind: "derive", id: "d1", field: "d1", inputs: ["city", "phone"], expression: "f(city, phone)" }],
      [{ nodeId: "d1", allowedLevel: Sensitivity.Secret }],
    );
    const report = checkExportPlan(plan);
    assert.equal(report.evaluations.get("d1")!.effectiveLevel, Sensitivity.Secret);
    assert.ok(report.ok);
  });

  it("低级别输入不抬高派生字段级别", () => {
    const plan = planWith(
      [{ kind: "derive", id: "d1", field: "d1", inputs: ["city"], expression: "upper(city)" }],
      [{ nodeId: "d1", allowedLevel: Sensitivity.Internal }],
    );
    const report = checkExportPlan(plan);
    assert.equal(report.evaluations.get("d1")!.effectiveLevel, Sensitivity.Internal);
    assert.ok(report.ok);
  });
});

describe("隐藏原字段不能消除派生字段泄露", () => {
  it("只导出派生字段仍违规，来源链回溯到被隐藏的原始字段", () => {
    const plan = planWith(
      [
        { kind: "derive", id: "pair", field: "pair", inputs: ["name", "phone"], expression: "concat(name, phone)" },
        { kind: "derive", id: "token", field: "token", inputs: ["pair"], expression: "sha256(pair)" },
      ],
      // 导出计划中不包含 name / phone，仅导出派生字段 token
      [{ nodeId: "token", allowedLevel: Sensitivity.Internal }],
    );
    const report = checkExportPlan(plan);
    assert.equal(report.violations.length, 1);
    const v = report.violations[0];
    assert.equal(v.exportField, "token");
    assert.equal(v.effectiveLevel, Sensitivity.Secret);
    // 来源链：token -> pair -> phone（Secret 级别的真正来源）
    const chain = v.chains[0];
    assert.deepEqual(chain.map((s) => s.field), ["token", "pair", "phone"]);
    assert.equal(chain.at(-1)!.kind, "source");
  });

  it("多输入时只为承载最高级别的输入生成来源链", () => {
    const plan = planWith(
      [{ kind: "derive", id: "d1", field: "d1", inputs: ["name", "phone", "city"], expression: "f(...)" }],
      [{ nodeId: "d1", allowedLevel: Sensitivity.Internal }],
    );
    const report = checkExportPlan(plan);
    const chains = report.violations[0].chains;
    assert.equal(chains.length, 1);
    assert.deepEqual(chains[0].map((s) => s.field), ["d1", "phone"]);
  });
});

describe("聚合降级与最小分组人数", () => {
  const agg = (id: string, input: string, groupSizes: number[]): PlanNode => ({
    kind: "aggregate", id, field: id, inputs: [input], groupBy: ["city"],
    groupSizes, minGroupSize: 100, downgradeTo: Sensitivity.Internal,
  });

  it("所有分组达到最小分组人数时允许降级", () => {
    const plan = planWith(
      [agg("a1", "income", [4200, 3100, 2700])],
      [{ nodeId: "a1", allowedLevel: Sensitivity.Internal }],
    );
    const report = checkExportPlan(plan);
    const ev = report.evaluations.get("a1")!;
    assert.equal(ev.downgrade!.applied, true);
    assert.equal(ev.effectiveLevel, Sensitivity.Internal);
    assert.ok(report.ok);
  });

  it("任一分组不足最小人数时降级被拒绝", () => {
    const plan = planWith(
      [agg("a1", "income", [4200, 3100, 99])],
      [{ nodeId: "a1", allowedLevel: Sensitivity.Internal }],
    );
    const report = checkExportPlan(plan);
    const ev = report.evaluations.get("a1")!;
    assert.equal(ev.downgrade!.applied, false);
    assert.equal(ev.effectiveLevel, Sensitivity.Confidential);
    assert.equal(report.violations.length, 1);
    assert.match(report.violations[0].chains[0][0].note!, /降级未生效/);
  });
});

describe("过滤后人数减少使聚合降级失效", () => {
  it("过滤缩减人数，最小组人数跌破阈值，导出转为违规", () => {
    const filter: PlanNode = {
      kind: "filter", id: "hv", field: "hv", inputs: ["income"],
      predicate: "lifetime_value > 100000", remainingPopulation: 150,
    };
    const aggAfter: PlanNode = {
      kind: "aggregate", id: "a2", field: "a2", inputs: ["hv"], groupBy: ["city"],
      groupSizes: [80, 62, 8], minGroupSize: 100, downgradeTo: Sensitivity.Internal,
    };
    const plan = planWith([filter, aggAfter], [{ nodeId: "a2", allowedLevel: Sensitivity.Internal }]);
    const report = checkExportPlan(plan);

    // 验证过滤后人数减少
    assert.equal(report.evaluations.get("hv")!.population, 150);
    assert.ok(report.evaluations.get("hv")!.population < report.evaluations.get("income")!.population);

    // 降级失效 -> 违规，来源链包含过滤与聚合两步注解
    assert.equal(report.violations.length, 1);
    const chain = report.violations[0].chains[0];
    assert.deepEqual(chain.map((s) => s.kind), ["aggregate", "filter", "source"]);
    assert.match(chain[0].note!, /最小组人数 8 < 100/);
    assert.match(chain[1].note!, /人数 10000 -> 150/);
  });

  it("过滤节点剩余人数超过输入人数时拒绝该计划", () => {
    const bad: PlanNode = {
      kind: "filter", id: "f", field: "f", inputs: ["income"],
      predicate: "x > 0", remainingPopulation: 99999,
    };
    assert.throws(() => checkExportPlan(planWith([bad], [{ nodeId: "f", allowedLevel: Sensitivity.Secret }])), /超过输入人数/);
  });
});

describe("共享计算节点", () => {
  const sharedPlan = planWith(
    [
      { kind: "derive", id: "pair", field: "pair", inputs: ["name", "phone"], expression: "concat(name, phone)" },
      { kind: "derive", id: "t1", field: "t1", inputs: ["pair"], expression: "sha256(pair)" },
      { kind: "derive", id: "t2", field: "t2", inputs: ["pair"], expression: "hmac(pair, salt)" },
    ],
    [
      { nodeId: "t1", allowedLevel: Sensitivity.Internal },
      { nodeId: "t2", allowedLevel: Sensitivity.Internal },
    ],
  );

  it("共享节点被识别且只求值一次", () => {
    const report = checkExportPlan(sharedPlan);
    assert.ok(report.sharedNodes.includes("pair"));
    assert.equal(report.evaluationCounts.get("pair"), 1);
  });

  it("共享节点同时出现在两条违规来源链中", () => {
    const report = checkExportPlan(sharedPlan);
    assert.equal(report.violations.length, 2);
    for (const v of report.violations) {
      assert.ok(v.chains[0].some((s) => s.nodeId === "pair"));
    }
  });
});

describe("计算图完整性", () => {
  it("检测循环依赖", () => {
    const cyclic: PlanNode[] = [
      { kind: "derive", id: "x", field: "x", inputs: ["y"], expression: "f(y)" },
      { kind: "derive", id: "y", field: "y", inputs: ["x"], expression: "g(x)" },
    ];
    assert.throws(
      () => checkExportPlan({ name: "c", nodes: cyclic, exports: [{ nodeId: "x", allowedLevel: Sensitivity.Public }] }),
      /存在环/,
    );
  });

  it("全部合规时报告 ok", () => {
    const plan = planWith([], [{ nodeId: "city", allowedLevel: Sensitivity.Internal }]);
    const report = checkExportPlan(plan);
    assert.ok(report.ok);
    assert.equal(report.violations.length, 0);
  });
});
