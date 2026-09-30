/**
 * 运行样例：覆盖需求清单的各个环节。
 * 运行：node examples/run-sample.ts  （需要 Node ≥ 23.6，原生类型擦除）
 */
import { compilePlan } from '../src/infer.ts';
import { issueCertificate, replayCertificate } from '../src/certificate.ts';
import { optimizePlan, verifyErrorOrderPreserved } from '../src/optimize.ts';
import { enumerateInputs, findCounterexamples } from '../src/enumerate.ts';
import { IncrementalEngine } from '../src/incremental.ts';
import { describe, required, tLiteral, tNumber, tObject, tString, unionOf, type Type } from '../src/types.ts';
import type { Step } from '../src/plan.ts';

const show = (title: string, v: unknown) =>
  console.log(`\n=== ${title} ===\n` + (typeof v === 'string' ? v : JSON.stringify(v, null, 2)));

// ---------- 样例 A：良好计划的完整流水线 ----------
const inputA: Type = tObject({
  kind: required(unionOf([tLiteral('a'), tLiteral('b')])),
  x: required(tNumber),
  name: required(tString),
});
const planA: Step[] = [
  { id: 's1', op: 'compute', target: 'y', expr: { op: 'add', left: { op: 'field', path: '$.x' }, right: { op: 'lit', value: 1 } } },
  {
    id: 'b1', op: 'branch', cond: { kind: 'eq', path: '$.kind', value: 'a' },
    then: [{ id: 't1', op: 'compute', target: 'z', expr: { op: 'call', fn: 'strlen', args: [{ op: 'field', path: '$.name' }] } }],
    else: [],
  },
  { id: 's1b', op: 'compute', target: 'w', expr: { op: 'add', left: { op: 'field', path: '$.z' }, right: { op: 'lit', value: 1 } } },
  { id: 's2', op: 'delete', field: 'x' },
  { id: 's3', op: 'project', fields: ['kind', 'y', 'z'] },
];
const aA = compilePlan(planA, inputA);
show('A1 最终模式（z 仅单侧产生 ⇒ 可缺失）', describe(aA.schema));
show('A2 诊断（下游读取可缺失的 z ⇒ 警告）', aA.diagnostics);
show('A3 步骤依赖图', aA.depGraph.edges);
const { certificate } = issueCertificate(planA, inputA);
show('A4 证书（首步推导记录）', certificate.steps[0]);
show('A5 证书重放', replayCertificate(planA, inputA, certificate));
const enumA = findCounterexamples(planA, inputA);
show('A6 有限枚举反例检查', { checkedInputs: enumA.checkedInputs, counterexamples: enumA.counterexamples });

// ---------- 样例 B：先删 x 后计算 x+1 ⇒ 编译失败 ----------
const planB: Step[] = [
  { id: 's1', op: 'delete', field: 'x' },
  { id: 's2', op: 'compute', target: 'w', expr: { op: 'add', left: { op: 'field', path: '$.x' }, right: { op: 'lit', value: 1 } } },
];
const aB = compilePlan(planB, tObject({ x: required(tNumber) }));
show('B 先删 x 后计算 x+1 ⇒ 编译失败', { ok: aB.ok, diagnostics: aB.diagnostics });

// ---------- 样例 C：不可达分支 + 未知函数 ----------
const planC: Step[] = [
  {
    id: 'b1', op: 'branch', cond: { kind: 'eq', path: '$.kind', value: 'c' },
    then: [{ id: 't1', op: 'compute', target: 'z', expr: { op: 'lit', value: 1 } }],
    else: [],
  },
  { id: 's1', op: 'compute', target: 'r', expr: { op: 'call', fn: 'magic', args: [] } },
];
const aC = compilePlan(planC, inputA);
show('C 不可达分支 + 未知函数（不推断为 any）', aC.diagnostics);

// ---------- 样例 D：动态字段读取保守建模 ----------
const planD: Step[] = [
  { id: 's1', op: 'readDynamic', target: 'picked', key: { op: 'field', path: '$.name' } },
];
const aD = compilePlan(planD, inputA);
show('D 动态读取：结果类型与诊断', {
  picked: describe((aD.schema as Extract<Type, { kind: 'object' }>).fields['picked']!.type),
  diagnostics: aD.diagnostics,
});

// ---------- 样例 E：冗余消除且保持错误触发顺序 ----------
const planE: Step[] = [
  { id: 's1', op: 'delete', field: 'ghost' },
  { id: 's2', op: 'compute', target: 't', expr: { op: 'lit', value: 0 } },
  { id: 's3', op: 'compute', target: 't', expr: { op: 'add', left: { op: 'field', path: '$.x' }, right: { op: 'lit', value: 1 } } },
  { id: 's4', op: 'compute', target: 'bad', expr: { op: 'add', left: { op: 'field', path: '$.nope' }, right: { op: 'lit', value: 1 } } },
];
const inputE = tObject({ x: required(tNumber) });
const opt = optimizePlan(planE, inputE);
show('E1 删除的冗余步骤', opt.removed);
show('E2 保留步骤（含必错的 s4）', opt.steps.map((s) => s.id));
const order = verifyErrorOrderPreserved(planE, opt.steps, enumerateInputs(inputE));
show('E3 错误触发顺序保持（有限输入验证）', order);

// ---------- 样例 F：增量失效 ----------
const eng = new IncrementalEngine();
const mkPlan = (c: number): Step[] => [
  { id: 's1', op: 'compute', target: 'a', expr: { op: 'add', left: { op: 'field', path: '$.x' }, right: { op: 'lit', value: 1 } } },
  { id: 's2', op: 'compute', target: 'b', expr: { op: 'add', left: { op: 'field', path: '$.a' }, right: { op: 'lit', value: 1 } } },
  { id: 's3', op: 'compute', target: 'c', expr: { op: 'lit', value: c } },
  { id: 's4', op: 'compute', target: 'd', expr: { op: 'add', left: { op: 'field', path: '$.c' }, right: { op: 'lit', value: 1 } } },
];
eng.infer(mkPlan(2), inputE);
eng.infer(mkPlan(99), inputE);
show('F 修改 s3 后：重算步骤 / 缓存命中', {
  recomputed: eng.lastRecomputed,
  reused: eng.lastReused,
});
