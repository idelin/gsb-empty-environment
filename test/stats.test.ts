import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ExactWindowStats } from '../src/stats.ts';
import { parseDecimal, toDecimal, toFraction, eq, makeRational } from '../src/rational.ts';
import type { Rational } from '../src/rational.ts';

function frac(r: Rational | null): string | null {
  return r === null ? null : toFraction(r);
}

test('parseDecimal：整数、小数、科学计数法、负数、bigint', () => {
  assert.equal(toFraction(parseDecimal('42')), '42');
  assert.equal(toFraction(parseDecimal('-0.001')), '-1/1000');
  assert.equal(toFraction(parseDecimal('1.25e3')), '1250');
  assert.equal(toDecimal(parseDecimal('6.02e-23'), 25, 'down'), `0.${'0'.repeat(22)}602`);
  assert.equal(toFraction(parseDecimal('0.10')), '1/10'); // 约分
  assert.equal(toFraction(parseDecimal(123456789012345678901234567890n)), '123456789012345678901234567890');
  assert.throws(() => parseDecimal('1.2.3'));
  assert.throws(() => parseDecimal('abc'));
});

test('sum/mean：0.1+0.2+0.3 精确等于 3/5，无浮点误差', () => {
  const w = new ExactWindowStats();
  w.add('0.1'); w.add('0.2'); w.add('0.3');
  assert.equal(toFraction(w.sum()), '3/5');
  assert.equal(toFraction(w.mean()), '1/5');
  assert.equal(toDecimal(w.sum(), 1, 'down'), '0.6');
});

test('样本/总体方差区分：[1,2,3] 样本=1，总体=2/3', () => {
  const w = new ExactWindowStats();
  w.add('1'); w.add('2'); w.add('3');
  assert.equal(toFraction(w.variance('sample')), '1');
  assert.equal(toFraction(w.variance('population')), '2/3');
  assert.equal(toFraction(w.variance()), '1'); // 默认样本
});

test('n=1 时样本方差未定义（null），总体方差为 0；n=0 时均值/方差均为 null', () => {
  const w = new ExactWindowStats();
  assert.equal(w.mean(), null);
  assert.equal(w.variance('sample'), null);
  assert.equal(w.variance('population'), null);
  assert.equal(toFraction(w.sum()), '0');
  w.add('7.5');
  assert.equal(toFraction(w.mean()), '15/2');
  assert.equal(w.variance('sample'), null);
  assert.equal(toFraction(w.variance('population')), '0');
});

test('大偏移小方差：1e30 附近相差 1，样本方差精确为 1（无灾难消减）', () => {
  const w = new ExactWindowStats();
  w.add('1000000000000000000000000000000');
  w.add('1000000000000000000000000000001');
  w.add('1000000000000000000000000000002');
  assert.equal(toFraction(w.mean()), '1000000000000000000000000000001');
  assert.equal(toFraction(w.variance('sample')), '1');
  assert.equal(toFraction(w.variance('population')), '2/3');
});

test('大偏移 + 小数微差：1e30 附近相差 0.5，样本方差精确为 1/8', () => {
  const w = new ExactWindowStats();
  w.add('1000000000000000000000000000000.25');
  w.add('1000000000000000000000000000000.75');
  assert.equal(toFraction(w.variance('sample')), '1/8');
  assert.equal(toDecimal(w.variance('sample'), 4, 'down'), '0.1250');
});

test('大数不经过 Number：超出 MAX_SAFE_INTEGER 仍精确', () => {
  const w = new ExactWindowStats();
  w.add('9007199254740993'); // 2^53 + 1
  w.add('9007199254740993');
  assert.equal(toFraction(w.sum()), '18014398509481986');
  assert.equal(toFraction(w.variance('sample')), '0');
});

test('重复值：独立 id，删除其中一个不影响另一个', () => {
  const w = new ExactWindowStats();
  const a = w.add('5');
  w.add('5');
  const c = w.add('5');
  assert.equal(w.count, 3);
  assert.equal(toFraction(w.variance('sample')), '0');
  w.remove(a);
  assert.equal(w.count, 2);
  assert.equal(toFraction(w.sum()), '10');
  w.remove(c);
  assert.equal(toFraction(w.sum()), '5');
  assert.throws(() => w.remove(a)); // 已删除
  assert.throws(() => w.remove(999)); // 不存在
});

test('窗口删除：remove 精确回退 Σx 与 Σx²', () => {
  const w = new ExactWindowStats();
  const ids = ['1.5', '2.5', '3.5', '4.5'].map((v) => w.add(v));
  w.remove(ids[1]);
  assert.equal(toFraction(w.sum()), '19/2');
  assert.equal(toFraction(w.mean()), '19/6');
  assert.equal(toFraction(w.variance('sample')), '7/3');
});

test('maxSize 滑窗：超容量自动驱逐最旧值', () => {
  const w = new ExactWindowStats({ maxSize: 3 });
  w.add('1'); w.add('2'); w.add('3');
  w.add('4'); // 驱逐 1
  assert.equal(w.count, 3);
  assert.equal(toFraction(w.sum()), '9');
  assert.equal(toFraction(w.mean()), '3');
  w.add('5'); // 驱逐 2
  assert.equal(toFraction(w.sum()), '12');
  assert.equal(toFraction(w.variance('sample')), '1');
});

test('undo：撤销 add 恢复原统计', () => {
  const w = new ExactWindowStats();
  w.add('1'); w.add('2'); w.add('3');
  const before = w.snapshot();
  w.add('100');
  assert.equal(toFraction(w.sum()), '106');
  assert.ok(w.undo());
  const after = w.snapshot();
  assert.equal(after.count, before.count);
  assert.ok(eq(after.sum, before.sum));
  assert.ok(eq(after.mean!, before.mean!));
  assert.ok(eq(after.sampleVariance!, before.sampleVariance!));
  assert.ok(eq(after.populationVariance!, before.populationVariance!));
});

test('undo：撤销 remove 恢复原统计与原驱逐顺序', () => {
  const w = new ExactWindowStats({ maxSize: 3 });
  const id1 = w.add('10');
  w.add('20');
  w.add('30');
  const before = w.snapshot();
  w.remove(id1);
  assert.equal(toFraction(w.sum()), '50');
  assert.ok(w.undo());
  assert.ok(eq(w.snapshot().sum, before.sum));
  assert.equal(w.count, 3);
  // 驱逐顺序恢复：再 add 应驱逐 10 而非其他
  w.add('40');
  assert.equal(toFraction(w.sum()), '90'); // 20+30+40
});

test('undo：撤销触发驱逐的 add，被驱逐值一并恢复', () => {
  const w = new ExactWindowStats({ maxSize: 2 });
  w.add('1'); w.add('2');
  const before = w.snapshot();
  w.add('3'); // 驱逐 1
  assert.equal(toFraction(w.sum()), '5');
  assert.ok(w.undo());
  assert.equal(w.count, before.count);
  assert.ok(eq(w.snapshot().sum, before.sum));
  assert.ok(eq(w.snapshot().sampleVariance!, before.sampleVariance!));
  assert.equal(w.undo(), true);  // 撤销 add('2')
  assert.equal(w.undo(), true);  // 撤销 add('1')
  assert.equal(w.undo(), false); // 日志已空
  assert.equal(w.count, 0);
});

test('舍入规则显式：down/up/floor/ceil/half-up/half-even', () => {
  const third = makeRational(1n, 3n);
  const negThird = makeRational(-1n, 3n);
  assert.equal(toDecimal(third, 2, 'down'), '0.33');
  assert.equal(toDecimal(third, 2, 'up'), '0.34');
  assert.equal(toDecimal(third, 2, 'floor'), '0.33');
  assert.equal(toDecimal(third, 2, 'ceil'), '0.34');
  assert.equal(toDecimal(negThird, 2, 'down'), '-0.33');
  assert.equal(toDecimal(negThird, 2, 'up'), '-0.34');
  assert.equal(toDecimal(negThird, 2, 'floor'), '-0.34');
  assert.equal(toDecimal(negThird, 2, 'ceil'), '-0.33');
  // 恰好一半：half-up 远离零，half-even 取偶
  assert.equal(toDecimal(makeRational(1n, 8n), 2, 'half-up'), '0.13');   // 0.125
  assert.equal(toDecimal(makeRational(1n, 8n), 2, 'half-even'), '0.12'); // 0.125 → 偶
  assert.equal(toDecimal(makeRational(3n, 8n), 2, 'half-even'), '0.38'); // 0.375 → 偶
  assert.equal(toDecimal(makeRational(-1n, 8n), 2, 'half-up'), '-0.13');
  // 整数与 0 位小数
  assert.equal(toDecimal(makeRational(7n, 2n), 0, 'half-up'), '4');
  assert.equal(toDecimal(makeRational(5n, 1n), 2, 'down'), '5.00');
});

test('混合大整数与小数、负数窗口', () => {
  const w = new ExactWindowStats();
  w.add('-100000000000000000000.0005');
  w.add('100000000000000000000.0005');
  assert.equal(toFraction(w.sum()), '0');
  assert.equal(toFraction(w.mean()), '0');
  // 方差 = x^2（关于 0 对称）：(2·(1e20.0005)²)/1 - 0 … 样本 = 2·x²
  const x2 = parseDecimal('100000000000000000000.0005');
  const expected = toFraction(makeRational(
    2n * x2.num * x2.num, x2.den * x2.den));
  assert.equal(toFraction(w.variance('sample')), expected);
});
