import { test } from 'node:test';
import assert from 'node:assert/strict';
import { IncrementalEngine } from '../src/incremental.ts';
import type { Step } from '../src/plan.ts';
import { required, tNumber, tObject, type Type } from '../src/types.ts';

const input: Type = tObject({ seed: required(tNumber) });

const lit = (v: number) => ({ op: 'lit', value: v }) as const;
const addOne = (path: string) =>
  ({ op: 'add', left: { op: 'field', path }, right: { op: 'lit', value: 1 } }) as const;

function makePlan(cVal: number, s1Target = 'a'): Step[] {
  return [
    { id: 's1', op: 'compute', target: s1Target, expr: addOne('$.seed') },
    { id: 's2', op: 'compute', target: 'b', expr: addOne('$.a') },
    { id: 's3', op: 'compute', target: 'c', expr: lit(cVal) },
    { id: 's4', op: 'compute', target: 'd', expr: addOne('$.c') },
  ];
}

test('增量：首次全量推导，二次全部命中缓存', () => {
  const eng = new IncrementalEngine();
  const plan = makePlan(2);
  const a1 = eng.infer(plan, input);
  assert.deepEqual(eng.lastRecomputed, ['s1', 's2', 's3', 's4']);
  const a2 = eng.infer(plan, input);
  assert.deepEqual(eng.lastRecomputed, []);
  assert.deepEqual(eng.lastReused, ['s1', 's2', 's3', 's4']);
  assert.ok(IncrementalEngine.matchesFull(plan, input, a2));
  assert.deepEqual(a1.schema, a2.schema);
});

test('增量：修改一个步骤只失效相关推导', () => {
  const eng = new IncrementalEngine();
  eng.infer(makePlan(2), input);
  // 修改 s3 的字面量：只有 s3 自身与读取 c 的 s4 失效
  const changed = makePlan(99);
  const a = eng.infer(changed, input);
  assert.deepEqual(eng.lastRecomputed, ['s3', 's4']);
  assert.deepEqual(eng.lastReused, ['s1', 's2']);
  assert.ok(IncrementalEngine.matchesFull(changed, input, a));
});

test('增量：改写字段名只波及依赖该字段的步骤', () => {
  const eng = new IncrementalEngine();
  eng.infer(makePlan(2), input);
  // s1 改名 a→a2：s2 读 a 失效（并报错），s3/s4 与 a 无关，保持缓存
  const changed = makePlan(2, 'a2');
  const a = eng.infer(changed, input);
  assert.deepEqual(eng.lastRecomputed, ['s1', 's2']);
  assert.deepEqual(eng.lastReused, ['s3', 's4']);
  assert.equal(a.ok, false);
  assert.ok(a.diagnostics.some((d) => d.stepId === 's2' && d.path === '$.a'));
  assert.ok(IncrementalEngine.matchesFull(changed, input, a));
});
