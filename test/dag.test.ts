import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rational } from '../src/rational.ts';
import { add, sub, mul, div, lit, evaluate, compareExprs, type Expr } from '../src/dag.ts';

test('DAG 求值：四则组合结果正确', () => {
  // (1/2 + 1/3) * 3 = 5/2
  const e = mul(add(lit(1n, 2n), lit(1n, 3n)), lit(3n));
  const r = evaluate(e);
  assert.equal(r.status, 'ok');
  if (r.status === 'ok') assert.equal(r.value.toString(), '5/2');
  // (2/3 - 5/6) / (1/4) = -2/3
  const e2 = div(sub(lit(2n, 3n), lit(5n, 6n)), lit(1n, 4n));
  const r2 = evaluate(e2);
  assert.equal(r2.status, 'ok');
  if (r2.status === 'ok') assert.equal(r2.value.toString(), '-2/3');
});

test('DAG 共享子表达式只算一次（同一对象记忆化）', () => {
  const shared = add(lit(1n, 7n), lit(1n, 13n));
  const e = mul(shared, shared);
  const r = evaluate(e);
  assert.equal(r.status, 'ok');
  if (r.status === 'ok') {
    const expect = Rational.of(1n, 7n).add(Rational.of(1n, 13n));
    assert.ok(r.value.equals(expect.mul(expect)));
  }
});

test('延迟约分：超预算先约分，可救回则 ok', () => {
  // (2^40 * 3) / (2^40) 中间位长 ~82，约分后回到 3/1
  const big = 1n << 40n;
  const e = div(lit(big * 3n), lit(big));
  const tight = evaluate(e, { maxBits: 50 });
  assert.equal(tight.status, 'ok');
  if (tight.status === 'ok') assert.equal(tight.value.toString(), '3');
});

test('位长预算：连乘爆炸 -> incomplete，不抛错不展开', () => {
  // ((2/3)*(2/3)*...) 分子位长线性增长
  let e: Expr = lit(2n, 3n);
  for (let i = 0; i < 200; i++) e = mul(e, lit(2n, 3n));
  const r = evaluate(e, { maxBits: 64 });
  assert.equal(r.status, 'incomplete');
  if (r.status === 'incomplete') assert.match(r.reason, /budget/);
  // 预算充足则可完成
  const ok = evaluate(e, { maxBits: 4096 });
  assert.equal(ok.status, 'ok');
});

test('除零子表达式 -> incomplete', () => {
  const r = evaluate(add(lit(1n), div(lit(1n), lit(0n))));
  assert.equal(r.status, 'incomplete');
  if (r.status === 'incomplete') assert.match(r.reason, /zero/);
});

test('表达式比较：交叉乘判定，无需十进制展开', () => {
  // 1/3 + 1/6 == 1/2
  const a = add(lit(1n, 3n), lit(1n, 6n));
  const b = lit(1n, 2n);
  assert.deepEqual(compareExprs(a, b), { status: 'ok', order: 0 });
  // 355/113 vs 22/7（π 的两个逼近）
  const c = compareExprs(lit(355n, 113n), lit(22n, 7n));
  assert.deepEqual(c, { status: 'ok', order: -1 });
  // 预算不足时比较同样 incomplete
  let big: Expr = lit(2n, 3n);
  for (let i = 0; i < 200; i++) big = mul(big, lit(2n, 3n));
  assert.equal(compareExprs(big, lit(1n), { maxBits: 64 }).status, 'incomplete');
});
