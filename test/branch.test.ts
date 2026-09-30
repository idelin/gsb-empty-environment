import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compilePlan } from '../src/infer.ts';
import type { Step } from '../src/plan.ts';
import {
  describe,
  required,
  tLiteral,
  tNumber,
  tObject,
  tString,
  unionOf,
  type Type,
} from '../src/types.ts';

const input: Type = tObject({
  kind: required(unionOf([tLiteral('a'), tLiteral('b')])),
  x: required(tNumber),
  name: required(tString),
});

test('分支合流：两侧类型取联合', () => {
  const plan: Step[] = [
    {
      id: 'b1',
      op: 'branch',
      cond: { kind: 'eq', path: '$.kind', value: 'a' },
      then: [
        { id: 't1', op: 'compute', target: 'z', expr: { op: 'add', left: { op: 'field', path: '$.x' }, right: { op: 'lit', value: 1 } } },
      ],
      else: [
        { id: 'e1', op: 'compute', target: 'z', expr: { op: 'call', fn: 'upper', args: [{ op: 'field', path: '$.name' }] } },
      ],
    },
  ];
  const a = compilePlan(plan, input);
  assert.ok(a.ok, JSON.stringify(a.diagnostics));
  const z = (a.schema as Extract<Type, { kind: 'object' }>).fields['z']!;
  assert.equal(describe(z.type), 'number | string');
  assert.equal(z.optional, false);
});

test('分支只一侧产生字段：下游标记为可缺失，读取产生警告', () => {
  const plan: Step[] = [
    {
      id: 'b1',
      op: 'branch',
      cond: { kind: 'eq', path: '$.kind', value: 'a' },
      then: [
        { id: 't1', op: 'compute', target: 'z', expr: { op: 'add', left: { op: 'field', path: '$.x' }, right: { op: 'lit', value: 1 } } },
      ],
      else: [],
    },
    { id: 's2', op: 'compute', target: 'w', expr: { op: 'add', left: { op: 'field', path: '$.z' }, right: { op: 'lit', value: 1 } } },
  ];
  const a = compilePlan(plan, input);
  assert.ok(a.ok, '可缺失读取是警告而非错误');
  const z = (a.schema as Extract<Type, { kind: 'object' }>).fields['z']!;
  assert.equal(z.optional, true, '合流后 z 应可缺失');
  const warn = a.diagnostics.find((d) => d.stepId === 's2' && d.severity === 'warning');
  assert.ok(warn);
  assert.equal(warn.path, '$.z');
  assert.match(warn.message, /可缺失/);
});

test('不可达分支：条件收缩为空时提前报警', () => {
  const plan: Step[] = [
    {
      id: 'b1',
      op: 'branch',
      cond: { kind: 'eq', path: '$.kind', value: 'c' },
      then: [
        { id: 't1', op: 'compute', target: 'z', expr: { op: 'lit', value: 1 } },
      ],
      else: [],
    },
  ];
  const a = compilePlan(plan, input);
  const warn = a.diagnostics.find((d) => d.message.includes('不可达'));
  assert.ok(warn);
  assert.equal(warn.stepId, 'b1');
  // 不可达侧不产生字段：z 不进入合流结果
  assert.equal((a.schema as Extract<Type, { kind: 'object' }>).fields['z'], undefined);
});

test('路径敏感收缩：then 侧 kind 收窄为字面量，else 侧排除该字面量', () => {
  const plan: Step[] = [
    {
      id: 'b1',
      op: 'branch',
      cond: { kind: 'eq', path: '$.kind', value: 'a' },
      then: [
        { id: 't1', op: 'compute', target: 'p', expr: { op: 'lit', value: 0 } },
      ],
      else: [
        { id: 'e1', op: 'compute', target: 'q', expr: { op: 'lit', value: 0 } },
      ],
    },
  ];
  const a = compilePlan(plan, input);
  const tIn = a.derivations.find((d) => d.stepId === 't1')!.input;
  const eIn = a.derivations.find((d) => d.stepId === 'e1')!.input;
  const tKind = (tIn as Extract<Type, { kind: 'object' }>).fields['kind']!;
  const eKind = (eIn as Extract<Type, { kind: 'object' }>).fields['kind']!;
  assert.equal(describe(tKind.type), '"a"');
  assert.equal(describe(eKind.type), '"b"');
});

test('has 条件：真侧去掉可缺失性，假侧移除字段', () => {
  const optInput: Type = tObject({
    x: required(tNumber),
    z: { type: tString, optional: true },
  });
  const plan: Step[] = [
    {
      id: 'b1',
      op: 'branch',
      cond: { kind: 'has', path: '$.z' },
      then: [
        { id: 't1', op: 'compute', target: 'n', expr: { op: 'call', fn: 'strlen', args: [{ op: 'field', path: '$.z' }] } },
      ],
      else: [],
    },
  ];
  const a = compilePlan(plan, optInput);
  // then 侧 z 已收窄为必需，读取无警告
  assert.ok(!a.diagnostics.some((d) => d.stepId === 't1' && d.severity === 'warning'));
  assert.ok(a.ok, JSON.stringify(a.diagnostics));
});
