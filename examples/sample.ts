/**
 * 端到端运行样例：
 *   node examples/sample.ts
 *
 * 覆盖：声明规则（空白/日期/枚举）、显式顺序、逐字段变更记录、
 * 错误定位与原值保留、字段身份、逆补丁、两遍幂等、
 * 依赖分析（读写路径/触发边）、别名冲突/触发环/顺序违例拦截、
 * 幂等反例搜索（proved/refuted/undecided）、
 * 版本化发布（非法发布不破坏上一版）与局部规则增量重算。
 */
import type { JsonValue, RuleSet } from "../src/types.ts";
import {
  analyzeRuleset,
  applyInversePatch,
  normalize,
  searchIdempotency,
  NormalizationSession,
  RuleRegistry,
} from "../src/index.ts";

const hr = (title: string): void => {
  console.log("\n" + "=".repeat(72));
  console.log(title);
  console.log("=".repeat(72));
};

/* ------------------------- 1. 声明规则与规范化 ------------------------- */
hr("1. 声明规则（顺序即数组顺序）与规范化");

const ruleset: RuleSet = {
  name: "people-v1",
  rules: [
    { id: "ws-name", type: "whitespace", path: "/people/*/name", mode: "collapse" },
    {
      id: "date-birth",
      type: "date",
      path: "/people/*/birth",
      formats: ["YYYY-MM-DD"],
      outputFormat: "DD/MM/YYYY",
    },
    {
      id: "enum-sex",
      type: "enum",
      path: "/people/*/sex",
      map: { M: "male", F: "female" },
    },
  ],
};

const input: JsonValue = {
  people: [
    { name: "  Ada   Lovelace ", birth: "1990-03-07", sex: "F", note: null, age: 30 },
    { name: "Bob", birth: "31/12/2021", sex: "Z" }, // 日期无法解析 + 枚举未知
  ],
};

const result = normalize(input, ruleset);
console.log("规范化输出:", JSON.stringify(result.output, null, 2));

hr("2. 逐字段变更记录");
for (const path of ["/people/0/name", "/people/0/birth", "/people/0/sex", "/people/1/birth"]) {
  const rec = result.records[path];
  console.log(`\n字段 ${path}`);
  console.log("  before:", JSON.stringify(rec.before.value), `(${rec.before.kind}, fp=${rec.before.fingerprint})`);
  console.log("  after :", JSON.stringify(rec.after.value), `(fp=${rec.after.fingerprint})`);
  for (const ch of rec.changes) {
    console.log(`  变更  : [${ch.ruleId}] ${JSON.stringify(ch.from)} -> ${JSON.stringify(ch.to)}`);
  }
  if (rec.error) {
    console.log(`  错误  : [${rec.error.ruleId}] ${rec.error.code} - ${rec.error.message}`);
    console.log(`          保留原值: ${JSON.stringify(rec.error.retained)} @ ${rec.error.path}`);
  }
}

hr("3. 错误与来源可定位到具体路径");
for (const e of result.errors) {
  console.log(`${e.path}  <- 规则 ${e.ruleId}(${e.ruleType})  ${e.code}: ${e.message}`);
}

hr("4. 逆补丁（应用后恢复规范化前的树）");
console.log(JSON.stringify(result.inversePatch, null, 2));
const restored = applyInversePatch(result.output, result.inversePatch);
console.log("逆补丁还原 == 原始输入:", JSON.stringify(restored) === JSON.stringify(input));

hr("5. 规范化两次不继续改变");
const second = normalize(result.output, ruleset);
console.log("两遍输出一致:", JSON.stringify(second.output) === JSON.stringify(result.output));
console.log("第二遍新增变更数:", Object.values(second.records).reduce((n, r) => n + r.changes.length, 0));

/* ------------------------- 6. 依赖分析 ------------------------- */
hr("6. 规则组合读写路径依赖");
const analysis = analyzeRuleset(ruleset);
for (const d of analysis.dependencies) {
  console.log(`#${d.order} ${d.ruleId}(${d.ruleType}) reads=${JSON.stringify(d.reads)} writes=${JSON.stringify(d.writes)} 声明域大小=${d.declaredDomain.length}`);
}
console.log("路径相交对:", analysis.overlapPairs);
console.log("触发边:", analysis.triggerEdges);
console.log("静态分析通过:", analysis.ok);

/* ---------------- 7. 提前发现：别名冲突/环/顺序违例 ---------------- */
hr("7. 提前发现非法组合");

const conflict: RuleSet = {
  name: "conflict",
  rules: [
    { id: "e1", type: "enum", path: "/s", map: { M: "male" } },
    { id: "e2", type: "enum", path: "/s", map: { M: "man" } },
  ],
};
console.log("别名冲突:", analyzeRuleset(conflict).aliasConflicts.map((c) => c.message));

const cycle: RuleSet = {
  name: "cycle",
  rules: [{ id: "e", type: "enum", path: "/s", map: { A: "B", B: "A" } }],
};
console.log("相互触发环:", analyzeRuleset(cycle).cycles.map((c) => c.message));

const badOrder: RuleSet = {
  name: "order",
  rules: [
    { id: "consumer", type: "enum", path: "/s", map: { x: "z" } },
    { id: "producer", type: "enum", path: "/s", map: { A: "x" } },
  ],
};
console.log("顺序违例:", analyzeRuleset(badOrder).orderViolations.map((v) => v.message));

/* ------------------------- 8. 幂等反例搜索 ------------------------- */
hr("8. 支持的有限输入域上的幂等性反例搜索");
const goodIdem = searchIdempotency(ruleset);
console.log(`良好组合: ${goodIdem.status}（${goodIdem.evaluated}/${goodIdem.domainSize}）- ${goodIdem.note}`);

const badIdem = searchIdempotency(cycle);
console.log(`别名环  : ${badIdem.status}`);
for (const c of badIdem.counterexamples) {
  console.log(`  反例: ${JSON.stringify(c.input)} -> ${JSON.stringify(c.once)} -> ${JSON.stringify(c.twice)}`);
}

const undecided = searchIdempotency(ruleset, { budget: 10 });
console.log(`预算未决: ${undecided.status}（${undecided.evaluated}/${undecided.domainSize}）- ${undecided.note}`);

/* ------------------- 9. 版本化发布：非法发布不破坏旧版 ------------------- */
hr("9. 版本化发布门禁");
const registry = new RuleRegistry();
const pub1 = registry.publish("v1", ruleset);
console.log("发布 v1:", pub1.ok, "激活:", registry.activeVersion, "幂等:", pub1.idempotency?.status);

const v2Bad: RuleSet = {
  name: "people-v2-bad",
  rules: [
    ...ruleset.rules,
    { id: "broken", type: "enum", path: "/people/*/sex", map: { male: "F", female: "M" } },
  ],
};
const pubBad = registry.publish("v2-bad", v2Bad);
console.log("发布 v2-bad:", pubBad.ok, "| 拒绝原因数:", pubBad.reasons.length);
console.log("  首条原因:", pubBad.reasons[0]?.split("\n")[0]);
console.log("激活版本仍是:", registry.activeVersion, "（非法发布未破坏上一版结果）");

/* ------------------------- 10. 局部规则增量重算 ------------------------- */
hr("10. 局部规则更新只重算依赖字段");
const v2: RuleSet = {
  name: "people-v2",
  rules: [
    { id: "ws-name", type: "whitespace", path: "/people/*/name", mode: "trim" }, // collapse -> trim
    ruleset.rules[1],
    ruleset.rules[2],
  ],
};
const pub2 = registry.publish("v2", v2);
console.log("发布 v2:", pub2.ok);

const session = new NormalizationSession(registry, input, "v1");
console.log("v1 输出 name[0]:", JSON.stringify((session.result.output as any).people[0].name));
const inc = session.updateTo("v2");
console.log(`从 ${inc.fromVersion} 升级到 ${inc.toVersion}`);
console.log("重算字段:", inc.recomputedPaths);
console.log("复用字段:", inc.unchangedPaths);
console.log("v2 输出 name[0]:", JSON.stringify((inc.output as any).people[0].name));
console.log("v2 输出 sex[0] 保持:", JSON.stringify((inc.output as any).people[0].sex), "（未重算，沿用 v1 结果）");
