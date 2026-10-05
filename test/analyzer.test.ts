import { test } from 'node:test';
import assert from 'node:assert/strict';
import { RuleCoverageAnalyzer, analyzeRules } from '../src/analyzer.ts';
import { crossCheck } from '../src/exhaustive.ts';
import { enumEq, enumIn, intBetween, intEq, intRange } from '../src/model.ts';
import type { Attr, Condition, Rule, Schema } from '../src/model.ts';

const schemaX: Schema = { attrs: [{ kind: 'int', name: 'x', min: 0, max: 9 }] };

const schemaXL: Schema = {
  attrs: [
    { kind: 'int', name: 'x', min: 0, max: 9 },
    { kind: 'enum', name: 'level', values: ['low', 'mid', 'high'] },
  ],
};

function statuses(report: ReturnType<typeof analyzeRules>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const r of report.rules) out[r.ruleId] = r.status;
  return out;
}

test('相邻半开端点：[0,5) 与 [5,10) 无缝无重叠，完全覆盖', () => {
  const rules: Rule[] = [
    { id: 'r1', action: 'low', conditions: [intRange('x', 0, 5)] },
    { id: 'r2', action: 'high', conditions: [intRange('x', 5, 10)] },
  ];
  const report = analyzeRules(schemaX, rules);
  assert.deepEqual(statuses(report), { r1: 'reachable', r2: 'reachable' });
  assert.equal(report.fullyCovered, true);
  assert.equal(report.uncoveredWitness, null);
  assert.deepEqual(report.rules[1].witness, { x: 5 }); // 邻接端点 5 归属后一区间
  assert.equal(crossCheck(schemaX, rules, report).length, 0);
});

test('空区间 lo >= hi 判定为 empty', () => {
  const rules: Rule[] = [
    { id: 'empty1', action: 'a', conditions: [intRange('x', 3, 3)] },
    { id: 'empty2', action: 'b', conditions: [intRange('x', 7, 2)] },
    { id: 'ok', action: 'c', conditions: [intRange('x', 0, 10)] },
  ];
  const report = analyzeRules(schemaX, rules);
  assert.deepEqual(statuses(report), { empty1: 'empty', empty2: 'empty', ok: 'reachable' });
  assert.equal(report.rules[0].witness, null);
  assert.equal(crossCheck(schemaX, rules, report).length, 0);
});

test('互斥条件：同一属性两个不相交区间合取为 empty', () => {
  const rules: Rule[] = [
    { id: 'contra', action: 'a', conditions: [intRange('x', 0, 5), intRange('x', 7, 10)] },
    { id: 'fallback', action: 'b', conditions: [intRange('x', 0, 10)] },
  ];
  const report = analyzeRules(schemaX, rules);
  assert.equal(report.rules[0].status, 'empty');
  assert.equal(crossCheck(schemaX, rules, report).length, 0);
});

test('互斥枚举条件：level 同时等于两个不同值为 empty', () => {
  const rules: Rule[] = [
    { id: 'contra', action: 'a', conditions: [enumEq('level', 'low'), enumEq('level', 'high')] },
    { id: 'fallback', action: 'b', conditions: [] },
  ];
  const report = analyzeRules(schemaXL, rules);
  assert.equal(report.rules[0].status, 'empty');
  assert.equal(report.rules[1].status, 'reachable');
  assert.equal(crossCheck(schemaXL, rules, report).length, 0);
});

test('完全遮蔽：前序宽规则使后序规则不可达', () => {
  const rules: Rule[] = [
    { id: 'wide', action: 'allow', conditions: [intRange('x', 0, 10)] },
    { id: 'narrow', action: 'deny', conditions: [intRange('x', 2, 4)] },
  ];
  const report = analyzeRules(schemaX, rules);
  assert.equal(report.rules[1].status, 'unreachable');
  assert.deepEqual(report.rules[1].shadowedBy, ['wide']);
  assert.equal(report.rules[1].witness, null);
  assert.equal(crossCheck(schemaX, rules, report).length, 0);
});

test('部分遮蔽：给出落在未遮蔽部分的最小反例', () => {
  const rules: Rule[] = [
    { id: 'r1', action: 'a', conditions: [intRange('x', 0, 5)] },
    { id: 'r2', action: 'b', conditions: [intRange('x', 3, 8)] },
  ];
  const report = analyzeRules(schemaX, rules);
  const r2 = report.rules[1];
  assert.equal(r2.status, 'partially-shadowed');
  assert.deepEqual(r2.witness, { x: 5 }); // [3,5) 被 r1 遮蔽，[5,8) 可达
  assert.deepEqual(r2.shadowedBy, ['r1']);
  // 未覆盖 [8,10) 的最小反例
  assert.deepEqual(report.uncoveredWitness, { x: 8 });
  assert.equal(crossCheck(schemaX, rules, report).length, 0);
});

test('未覆盖输入的最小反例遵循属性声明顺序的字典序', () => {
  const rules: Rule[] = [
    { id: 'r1', action: 'a', conditions: [intRange('x', 0, 5), enumEq('level', 'low')] },
  ];
  const report = analyzeRules(schemaXL, rules);
  // x=0 时 level=low 被覆盖，level=mid 未覆盖 → 最小未覆盖输入
  assert.deepEqual(report.uncoveredWitness, { x: 0, level: 'mid' });
  assert.equal(report.fullyCovered, false);
  assert.equal(crossCheck(schemaXL, rules, report).length, 0);
});

test('first-match-wins：相同条件的后序规则不可达', () => {
  const rules: Rule[] = [
    { id: 'first', action: 'A', conditions: [intBetween('x', 0, 4)] },
    { id: 'second', action: 'B', conditions: [intBetween('x', 0, 4)] },
  ];
  const report = analyzeRules(schemaX, rules);
  assert.deepEqual(statuses(report), { first: 'reachable', second: 'unreachable' });
  assert.equal(crossCheck(schemaX, rules, report).length, 0);
});

test('组合条件：intEq / enumIn / 多属性部分遮蔽', () => {
  const rules: Rule[] = [
    { id: 'vip', action: 'vip', conditions: [intEq('x', 0), enumIn('level', ['mid', 'high'])] },
    { id: 'all-mid', action: 'mid', conditions: [enumEq('level', 'mid')] },
    { id: 'zero', action: 'zero', conditions: [intEq('x', 0)] },
  ];
  const report = analyzeRules(schemaXL, rules);
  assert.equal(report.rules[0].status, 'reachable');
  // all-mid 在 x=0 处被 vip 遮蔽
  assert.equal(report.rules[1].status, 'partially-shadowed');
  assert.deepEqual(report.rules[1].witness, { x: 1, level: 'mid' });
  // zero 的 level=mid/high 部分被前两条遮蔽，level=low 可达
  assert.equal(report.rules[2].status, 'partially-shadowed');
  assert.deepEqual(report.rules[2].witness, { x: 0, level: 'low' });
  assert.equal(crossCheck(schemaXL, rules, report).length, 0);
});

test('增删规则后分析随之更新', () => {
  const analyzer = new RuleCoverageAnalyzer(schemaX, [
    { id: 'r1', action: 'a', conditions: [intRange('x', 0, 5)] },
    { id: 'r2', action: 'b', conditions: [intRange('x', 3, 8)] },
  ]);
  let report = analyzer.analyze();
  assert.equal(report.rules[1].status, 'partially-shadowed');
  assert.deepEqual(report.rules[1].witness, { x: 5 });

  // 删除遮蔽源：r2 变为完全可达
  assert.equal(analyzer.removeRule('r1'), true);
  report = analyzer.analyze();
  assert.equal(report.rules[0].status, 'reachable');
  assert.deepEqual(report.rules[0].witness, { x: 3 });
  assert.deepEqual(report.uncoveredWitness, { x: 0 });

  // 在头部插入更宽的规则：r2 变为不可达
  analyzer.addRule({ id: 'r0', action: 'z', conditions: [intRange('x', 0, 8)] }, 0);
  report = analyzer.analyze();
  assert.equal(report.rules[1].status, 'unreachable');
  assert.deepEqual(report.rules[1].shadowedBy, ['r0']);
  assert.equal(report.fullyCovered, false); // [8,10) 仍未覆盖
  assert.equal(crossCheck(schemaX, analyzer.getRules(), report).length, 0);

  // 补上空洞后完全覆盖
  analyzer.addRule({ id: 'tail', action: 't', conditions: [intRange('x', 8, 10)] });
  report = analyzer.analyze();
  assert.equal(report.fullyCovered, true);
  assert.equal(crossCheck(schemaX, analyzer.getRules(), report).length, 0);
});

test('非法定义抛错：未知属性 / 重复规则 id / 枚举值越界', () => {
  assert.throws(() => analyzeRules(schemaX, [{ id: 'a', action: 'x', conditions: [intRange('nope', 0, 1)] }]));
  assert.throws(
    () =>
      new RuleCoverageAnalyzer(schemaX, [
        { id: 'a', action: 'x', conditions: [] },
        { id: 'a', action: 'y', conditions: [] },
      ]),
  );
  assert.throws(() =>
    analyzeRules(schemaXL, [{ id: 'a', action: 'x', conditions: [enumEq('level', 'ultra')] }]),
  );
});

// ---- 随机小域穷举交叉核对 ----

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomSchema(rand: () => number): Schema {
  const attrs: Attr[] = [];
  const intCount = 1 + Math.floor(rand() * 3);
  for (let i = 0; i < intCount; i++) {
    const min = Math.floor(rand() * 5) - 2;
    const size = 1 + Math.floor(rand() * 5);
    attrs.push({ kind: 'int', name: `i${i}`, min, max: min + size - 1 });
  }
  const enumCount = Math.floor(rand() * 3);
  for (let i = 0; i < enumCount; i++) {
    const size = 1 + Math.floor(rand() * 3);
    attrs.push({ kind: 'enum', name: `e${i}`, values: ['a', 'b', 'c'].slice(0, size) });
  }
  // 打乱属性顺序，覆盖不同的字典序排列
  for (let i = attrs.length - 1; i > 0; i--) {
    const j = Math.floor(rand() * (i + 1));
    [attrs[i], attrs[j]] = [attrs[j], attrs[i]];
  }
  return { attrs };
}

function randomCondition(rand: () => number, attr: Attr): Condition {
  if (attr.kind === 'int') {
    // 边界故意越出域一点，制造空区间与相邻端点
    const lo = attr.min - 1 + Math.floor(rand() * (attr.max - attr.min + 4));
    const hi = attr.min - 1 + Math.floor(rand() * (attr.max - attr.min + 4));
    return intRange(attr.name, lo, hi);
  }
  const picked = attr.values.filter(() => rand() < 0.6);
  return enumIn(attr.name, picked); // 可能为空集
}

function randomRules(rand: () => number, schema: Schema, count: number, prefix: string): Rule[] {
  const rules: Rule[] = [];
  for (let i = 0; i < count; i++) {
    const condCount = Math.floor(rand() * 4);
    const conditions: Condition[] = [];
    for (let c = 0; c < condCount; c++) {
      const attr = schema.attrs[Math.floor(rand() * schema.attrs.length)];
      conditions.push(randomCondition(rand, attr));
    }
    rules.push({ id: `${prefix}${i}`, action: `act-${prefix}${i}`, conditions });
  }
  return rules;
}

test('随机小域：符号分析与穷举核对一致（含增删更新）', () => {
  const rand = mulberry32(20261005);
  const rounds = 300;
  for (let round = 0; round < rounds; round++) {
    const schema = randomSchema(rand);
    const rules = randomRules(rand, schema, Math.floor(rand() * 7), 'r');
    const analyzer = new RuleCoverageAnalyzer(schema, rules);

    // 随机增删后重新分析
    const mutations = Math.floor(rand() * 4);
    for (let m = 0; m < mutations; m++) {
      if (rand() < 0.5) {
        const extra = randomRules(rand, schema, 1, `m${round}-${m}-`);
        try {
          analyzer.addRule(extra[0], Math.floor(rand() * (analyzer.getRules().length + 1)));
        } catch {
          // 重复 id 等合法拒绝，忽略
        }
      } else {
        const current = analyzer.getRules();
        if (current.length > 0) {
          analyzer.removeRule(current[Math.floor(rand() * current.length)].id);
        }
      }
    }

    const finalRules = analyzer.getRules();
    const report = analyzer.analyze();

    // 增删后的分析必须与一次性全量分析完全一致
    const fresh = analyzeRules(schema, finalRules);
    assert.deepEqual(report, fresh, `round ${round}: incremental update diverges`);

    // 与暴力穷举交叉核对
    const problems = crossCheck(schema, finalRules, report);
    assert.deepEqual(problems, [], `round ${round} schema=${JSON.stringify(schema)} rules=${JSON.stringify(finalRules)}`);
  }
});
