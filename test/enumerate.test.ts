import { test } from 'node:test';
import assert from 'node:assert/strict';
import { enumerateInputs, findCounterexamples } from '../src/enumerate.ts';
import type { Step } from '../src/plan.ts';
import { required, tNumber, tObject, tString, type Type } from '../src/types.ts';

const input: Type = tObject({ x: required(tNumber), name: required(tString) });

const soundPlan: Step[] = [
  { id: 's1', op: 'compute', target: 'z', expr: { op: 'add', left: { op: 'field', path: '$.x' }, right: { op: 'lit', value: 1 } } },
  { id: 's2', op: 'rename', from: 'name', to: 'label' },
  { id: 's3', op: 'delete', field: 'x' },
];

test('有限枚举：输入集合有界且确定', () => {
  const a = enumerateInputs(input);
  const b = enumerateInputs(input);
  assert.ok(a.length > 0 && a.length <= 128);
  assert.deepEqual(a, b);
});

test('静态安全的计划：枚举检查无反例', () => {
  const r = findCounterexamples(soundPlan, input);
  assert.ok(r.staticallyClean);
  assert.ok(r.checkedInputs > 0);
  assert.deepEqual(r.counterexamples, []);
});

test('“静态声称安全但运行缺字段”：注入错误声称后能抓到反例并定位路径', () => {
  // 模拟被篡改/有缺陷的静态声称：声称 s3 之后 x 仍必需存在（实际已被删除）。
  const bogus: Type = tObject({
    x: required(tNumber),
    label: required(tString),
    z: required(tNumber),
  });
  const r = findCounterexamples(soundPlan, input, {
    claimsOverride: new Map([['s3', bogus]]),
  });
  assert.ok(r.counterexamples.length > 0);
  const cex = r.counterexamples[0]!;
  assert.equal(cex.stepId, 's3');
  assert.equal(cex.path, '$.x');
  assert.match(cex.actual, /缺失/);
});

test('静态已有错误的计划不属于“声称安全”', () => {
  const bad: Step[] = [
    { id: 's1', op: 'delete', field: 'x' },
    { id: 's2', op: 'compute', target: 'w', expr: { op: 'add', left: { op: 'field', path: '$.x' }, right: { op: 'lit', value: 1 } } },
  ];
  const r = findCounterexamples(bad, input);
  assert.equal(r.staticallyClean, false);
  assert.deepEqual(r.counterexamples, []);
});
