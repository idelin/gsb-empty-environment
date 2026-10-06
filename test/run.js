import assert from 'node:assert/strict';
import { NEVER, literal, object, union, typeEquals, typeToString } from '../src/types.js';
import { typeOfValue } from '../src/values.js';
import { getField, narrowEq, join, analyzeIf } from '../src/analyzer.js';
import { evalCondition, checkPrecision } from '../src/runtime.js';

let passed = 0;
let failed = 0;
function test(name, fn) {
  try {
    fn();
    passed++;
    console.log(`ok   ${name}`);
  } catch (err) {
    failed++;
    console.error(`FAIL ${name}\n${err.stack}`);
  }
}

// ---------- 类型系统基础 ----------

test('联合规范化：拍平、去重、never 坍缩', () => {
  const t = union([literal('a'), union([literal('a'), literal('b')]), NEVER]);
  assert.equal(t.kind, 'union');
  assert.equal(t.members.length, 2);
  assert.equal(union([NEVER, NEVER]), NEVER);
  assert.equal(union([literal('x')]).kind, 'literal');
});

test('typeEquals：对象字段顺序无关、联合成员顺序无关', () => {
  assert.ok(typeEquals(
    object({ a: literal(1), b: literal(2) }),
    object({ b: literal(2), a: literal(1) }),
  ));
  assert.ok(typeEquals(
    union([literal('x'), literal('y')]),
    union([literal('y'), literal('x')]),
  ));
  assert.ok(!typeEquals(literal('x'), literal('y')));
});

// ---------- 字段访问 ----------

const shape = union([
  object({ tag: literal('circle'), radius: literal(1) }),
  object({ tag: literal('rect'), w: literal(2), h: literal(3) }),
]);

test('字段访问：已知字段返回字段类型联合', () => {
  const diags = [];
  const t = getField(shape, 'tag', diags);
  assert.equal(diags.length, 0);
  assert.ok(typeEquals(t, union([literal('circle'), literal('rect')])));
});

test('字段访问：未知字段报错', () => {
  const diags = [];
  const t = getField(shape, 'area', diags);
  assert.equal(t, NEVER);
  assert.equal(diags.length, 2); // 两个成员都缺该字段
  assert.ok(diags.every((d) => d.code === 'unknown-field' && d.field === 'area'));
});

test('字段访问：部分成员缺字段同样报错', () => {
  const diags = [];
  getField(shape, 'radius', diags);
  assert.equal(diags.length, 1);
  assert.match(diags[0].message, /rect/);
});

// ---------- tag 等值收窄 ----------

test('收窄：then 侧保留匹配成员，else 侧保留其余', () => {
  const { then, else: els } = narrowEq(shape, 'tag', 'circle');
  assert.ok(typeEquals(then, object({ tag: literal('circle'), radius: literal(1) })));
  assert.ok(typeEquals(els, object({ tag: literal('rect'), w: literal(2), h: literal(3) })));
});

test('收窄：字段类型为字面值联合时对象在两侧分裂', () => {
  const flex = object({ tag: union([literal('a'), literal('b')]), v: literal(0) });
  const { then, else: els } = narrowEq(flex, 'tag', 'a');
  assert.ok(typeEquals(then, object({ tag: literal('a'), v: literal(0) })));
  assert.ok(typeEquals(els, object({ tag: literal('b'), v: literal(0) })));
});

test('收窄：缺失 tag 的成员落入 else、then 排除', () => {
  const withMissing = union([
    object({ tag: literal('circle'), radius: literal(1) }),
    object({ name: literal('anonymous') }), // 无 tag 字段
  ]);
  const { then, else: els } = narrowEq(withMissing, 'tag', 'circle');
  assert.ok(typeEquals(then, object({ tag: literal('circle'), radius: literal(1) })));
  assert.ok(typeEquals(els, object({ name: literal('anonymous') })));
});

// ---------- 合流与不可达 ----------

test('合流：分支合流取联合，且无损还原输入类型', () => {
  const { then, else: els } = narrowEq(shape, 'tag', 'circle');
  const merged = join(then, els);
  assert.equal(merged.kind, 'union');
  assert.ok(typeEquals(merged, shape));
});

test('不可达：then 分支无匹配成员时单独标记为不可达', () => {
  const r = analyzeIf(shape, { field: 'tag', equals: 'triangle' });
  assert.equal(r.then.reachable, false);
  assert.equal(r.then.type, NEVER);
  assert.equal(r.else.reachable, true);
  assert.ok(typeEquals(r.else.type, shape));
});

test('不可达：全部成员匹配时 else 分支不可达', () => {
  const only = object({ tag: literal('a') });
  const r = analyzeIf(only, { field: 'tag', equals: 'a' });
  assert.equal(r.then.reachable, true);
  assert.equal(r.else.reachable, false);
  assert.equal(r.else.type, NEVER);
});

test('诊断：所有成员都不含该字段时报 unknown-field', () => {
  const r = analyzeIf(shape, { field: 'area', equals: 1 });
  assert.equal(r.diagnostics.length, 1);
  assert.equal(r.diagnostics[0].code, 'unknown-field');
  assert.equal(r.then.reachable, false);
});

// ---------- 有限值集上的精度核对 ----------

const values = [
  { tag: 'circle', radius: 1 },
  { tag: 'circle', radius: 2 },
  { tag: 'rect', w: 2, h: 3 },
  { tag: 'rect', w: 4, h: 5 },
  { name: 'anonymous' }, // 缺失 tag
];

test('运行执行：缺失 tag 的值落入 else', () => {
  const { then, else: els } = evalCondition(values, 'tag', 'circle');
  assert.equal(then.length, 2);
  assert.equal(els.length, 3);
  assert.ok(els.some((v) => v.name === 'anonymous'));
});

test('精度核对：tag === "circle" 双侧静态剩余类型均精确（含缺失 tag）', () => {
  const r = checkPrecision(values, 'tag', 'circle');
  assert.ok(r.precise, r.report);
  // then 侧精确到 radius 的字面值联合
  assert.ok(typeEquals(
    r.checks[0].staticType,
    union([
      object({ tag: literal('circle'), radius: literal(1) }),
      object({ tag: literal('circle'), radius: literal(2) }),
    ]),
  ));
  // else 侧包含缺失 tag 的对象
  assert.ok(r.checks[1].staticType.kind === 'union');
  assert.ok(r.checks[1].staticType.members.some(
    (m) => m.kind === 'object' && !('tag' in m.fields),
  ));
});

test('精度核对：无匹配字面值时 then 侧精确为 never（不可达）', () => {
  const r = checkPrecision(values, 'tag', 'triangle');
  assert.ok(r.precise, r.report);
  assert.equal(r.checks[0].staticType, NEVER);
  assert.equal(r.checks[0].runtimeType, NEVER);
});

test('精度核对：能识别不精确（静态与运行不一致时判 false）', () => {
  const inputType = union(values.map(typeOfValue));
  const { then } = narrowEq(inputType, 'tag', 'circle');
  const wrong = union([then, object({ tag: literal('circle'), radius: literal(99) })]);
  assert.ok(!typeEquals(wrong, then));
  assert.ok(!typeEquals(typeOfValue({ tag: 'circle', radius: 1 }), wrong));
});

test('typeOfValue：原始值与对象的精确类型', () => {
  assert.ok(typeEquals(typeOfValue('x'), literal('x')));
  assert.ok(typeEquals(
    typeOfValue({ a: 1, b: 's' }),
    object({ a: literal(1), b: literal('s') }),
  ));
});

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
