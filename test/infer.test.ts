import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compilePlan } from '../src/infer.ts';
import type { Step } from '../src/plan.ts';
import {
  describe,
  required,
  tNumber,
  tObject,
  tString,
  type Type,
} from '../src/types.ts';

const input: Type = tObject({
  x: required(tNumber),
  y: required(tNumber),
  name: required(tString),
});

test('投影/改名/计算/删除：推导字段存在性与类型', () => {
  const plan: Step[] = [
    { id: 's1', op: 'compute', target: 'z', expr: { op: 'add', left: { op: 'field', path: '$.x' }, right: { op: 'field', path: '$.y' } } },
    { id: 's2', op: 'rename', from: 'name', to: 'label' },
    { id: 's3', op: 'delete', field: 'y' },
    { id: 's4', op: 'project', fields: ['z', 'label'] },
  ];
  const a = compilePlan(plan, input);
  assert.ok(a.ok, JSON.stringify(a.diagnostics));
  assert.equal(describe(a.schema), '{ label: string, z: number }');
});

test('先删 x 后计算 x+1：编译失败并定位到 $.x', () => {
  const plan: Step[] = [
    { id: 's1', op: 'delete', field: 'x' },
    { id: 's2', op: 'compute', target: 'w', expr: { op: 'add', left: { op: 'field', path: '$.x' }, right: { op: 'lit', value: 1 } } },
  ];
  const a = compilePlan(plan, input);
  assert.equal(a.ok, false);
  const err = a.diagnostics.find((d) => d.severity === 'error');
  assert.ok(err);
  assert.equal(err.stepId, 's2');
  assert.equal(err.path, '$.x');
  assert.match(err.message, /不存在或已删除/);
});

test('改名来源不存在：错误定位到路径', () => {
  const plan: Step[] = [{ id: 's1', op: 'rename', from: 'ghost', to: 'g2' }];
  const a = compilePlan(plan, input);
  assert.equal(a.ok, false);
  assert.equal(a.diagnostics[0]!.path, '$.ghost');
});

test('未知函数不推断为 any：报错且结果为 unknown', () => {
  const plan: Step[] = [
    { id: 's1', op: 'compute', target: 'r', expr: { op: 'call', fn: 'magic', args: [{ op: 'field', path: '$.x' }] } },
    { id: 's2', op: 'compute', target: 'q', expr: { op: 'add', left: { op: 'field', path: '$.r' }, right: { op: 'lit', value: 1 } } },
  ];
  const a = compilePlan(plan, input);
  assert.equal(a.ok, false);
  const unknownFn = a.diagnostics.find((d) => d.message.includes('未知函数'));
  assert.ok(unknownFn);
  assert.match(unknownFn.message, /不推断为 any/);
  // unknown 继续传播：下游算术只产生警告而非静默通过
  const warn = a.diagnostics.find((d) => d.stepId === 's2' && d.severity === 'warning');
  assert.ok(warn);
  assert.match(warn.message, /unknown/);
  const s1 = a.derivations.find((d) => d.stepId === 's1')!;
  assert.equal(describe((s1.output as Extract<Type, { kind: 'object' }>).fields['r']!.type), 'unknown');
});

test('动态字段读取：保守建模为全部字段的联合并依赖所有字段', () => {
  const plan: Step[] = [
    { id: 's1', op: 'readDynamic', target: 'picked', key: { op: 'field', path: '$.name' } },
  ];
  const a = compilePlan(plan, input);
  assert.ok(a.ok);
  const d = a.derivations.find((x) => x.stepId === 's1')!;
  assert.deepEqual(d.reads, ['$.name', '$.x', '$.y']);
  const picked = (d.output as Extract<Type, { kind: 'object' }>).fields['picked']!;
  assert.equal(describe(picked.type), 'number | string');
  assert.ok(d.diagnostics.some((x) => x.severity === 'info' && x.message.includes('保守建模')));
});

test('步骤依赖图：def-use 边与 $input 源', () => {
  const plan: Step[] = [
    { id: 's1', op: 'compute', target: 'a', expr: { op: 'add', left: { op: 'field', path: '$.x' }, right: { op: 'lit', value: 1 } } },
    { id: 's2', op: 'compute', target: 'b', expr: { op: 'add', left: { op: 'field', path: '$.a' }, right: { op: 'lit', value: 1 } } },
    { id: 's3', op: 'delete', field: 'a' },
    { id: 's4', op: 'compute', target: 'c', expr: { op: 'add', left: { op: 'field', path: '$.b' }, right: { op: 'lit', value: 1 } } },
  ];
  const a = compilePlan(plan, input);
  const edges = a.depGraph.edges.map((e) => `${e.from}->${e.to}:${e.field}`);
  assert.ok(edges.includes('$input->s1:x'));
  assert.ok(edges.includes('s1->s2:a'));
  assert.ok(edges.includes('s2->s4:b'));
  assert.ok(a.depGraph.nodes.includes('s3'));
});
