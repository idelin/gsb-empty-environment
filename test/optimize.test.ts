import { test } from 'node:test';
import assert from 'node:assert/strict';
import { enumerateInputs } from '../src/enumerate.ts';
import { optimizePlan, verifyErrorOrderPreserved, firstRuntimeError } from '../src/optimize.ts';
import type { Step } from '../src/plan.ts';
import { required, tNumber, tObject, tString, type Type } from '../src/types.ts';

const input: Type = tObject({ x: required(tNumber), name: required(tString) });

test('删除冗余步骤：无操作 delete/project/自改名/死计算', () => {
  const plan: Step[] = [
    { id: 's1', op: 'delete', field: 'ghost' },           // 无操作
    { id: 's2', op: 'project', fields: ['x', 'name'] },   // 保留全部字段，无操作
    { id: 's3', op: 'rename', from: 'x', to: 'x' },       // 自改名
    { id: 's4', op: 'compute', target: 't', expr: { op: 'lit', value: 1 } }, // 死代码：s5 覆盖前无人读
    { id: 's5', op: 'compute', target: 't', expr: { op: 'add', left: { op: 'field', path: '$.x' }, right: { op: 'lit', value: 1 } } },
  ];
  const r = optimizePlan(plan, input);
  assert.deepEqual(r.removed.map((x) => x.stepId), ['s1', 's2', 's3', 's4']);
  assert.deepEqual(r.steps.map((s) => s.id), ['s5']);
});

test('错误触发顺序保持：可能报错的步骤绝不删除', () => {
  const plan: Step[] = [
    { id: 's1', op: 'delete', field: 'ghost' },  // 无操作，可删
    { id: 's2', op: 'compute', target: 'w', expr: { op: 'add', left: { op: 'field', path: '$.missing' }, right: { op: 'lit', value: 1 } } }, // 读取缺失字段，必须保留
    { id: 's3', op: 'delete', field: 'alsoGhost' }, // 无操作，可删
  ];
  const r = optimizePlan(plan, input);
  assert.deepEqual(r.removed.map((x) => x.stepId), ['s1', 's3']);
  assert.deepEqual(r.steps.map((s) => s.id), ['s2']);
});

test('错误触发顺序保持：有限输入枚举下首错一致', () => {
  const plan: Step[] = [
    { id: 's1', op: 'delete', field: 'ghost' },
    { id: 's2', op: 'compute', target: 't', expr: { op: 'lit', value: 0 } },
    { id: 's3', op: 'compute', target: 't', expr: { op: 'add', left: { op: 'field', path: '$.x' }, right: { op: 'lit', value: 1 } } },
    { id: 's4', op: 'compute', target: 'bad', expr: { op: 'add', left: { op: 'field', path: '$.nope' }, right: { op: 'lit', value: 1 } } },
  ];
  const r = optimizePlan(plan, input);
  // s4 静态报错被保留；s1/s2 被删
  assert.deepEqual(r.steps.map((s) => s.id), ['s3', 's4']);
  const inputs = enumerateInputs(input);
  assert.ok(inputs.length > 0);
  const check = verifyErrorOrderPreserved(plan, r.steps, inputs);
  assert.ok(check.ok, JSON.stringify(check.mismatches));
});

test('首错定位：运行期错误携带步骤与路径', () => {
  const plan: Step[] = [
    { id: 's1', op: 'delete', field: 'x' },
    { id: 's2', op: 'compute', target: 'w', expr: { op: 'add', left: { op: 'field', path: '$.x' }, right: { op: 'lit', value: 1 } } },
  ];
  const err = firstRuntimeError(plan, { x: 1, name: 'a' });
  assert.ok(err);
  assert.equal(err.stepId, 's2');
  assert.equal(err.path, '$.x');
});
