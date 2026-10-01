import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compare, resumeCompare, startCompare } from '../src/compare.ts';
import { rational } from '../src/rational.ts';
import { decimalExpansion, exact, interval, withErrors } from '../src/value.ts';
import { parseValue } from '../src/parse.ts';

test('精确值：相等只在确切相等时成立', () => {
  assert.equal(compare(exact(rational(1n, 3n)), exact(rational(2n, 6n))).verdict, 'eq');
  assert.equal(compare(exact(rational(1n, 3n)), exact(rational(1n, 4n))).verdict, 'gt');
  assert.equal(compare(exact(0n), exact(rational(0n, 9n))).verdict, 'eq');
});

test('大整数相差 1：lt/gt 可区分', () => {
  const a = exact(2n ** 90n);
  const b = exact(2n ** 90n + 1n);
  assert.equal(compare(a, b).verdict, 'lt');
  assert.equal(compare(b, a).verdict, 'gt');
});

test('无 epsilon：相差 10^-30 仍是 lt，不是 eq', () => {
  const one = exact(rational(1n));
  const almost = exact(rational(10n ** 30n + 1n, 10n ** 30n));
  const s = compare(one, almost);
  assert.equal(s.verdict, 'lt');
  assert.equal(s.status, 'done');
});

test('静态区间：分离则确定，重叠则未决且恢复后依然未决', () => {
  assert.equal(compare(parseValue('1±0.1'), parseValue('2±0.1')).verdict, 'lt');
  const s = compare(parseValue('1±0.5'), parseValue('1.2±0.5'), 10);
  assert.equal(s.status, 'pending');
  assert.equal(s.verdict, 'undecided');
  assert.equal(s.stepsUsed, 0); // 非可细化值不消耗预算
  const resumed = resumeCompare(s, 100);
  assert.equal(resumed.status, 'pending');
  assert.equal(resumed.verdict, 'undecided');
});

test('共享来源的误差相消，不得当作独立误差', () => {
  // 同一把尺子（source 相同）测得 10±1 与 11±1：相关误差相消，差值恰为 -1。
  const x = withErrors(rational(10n), [{ source: 'ruler', radius: rational(1n) }]);
  const y = withErrors(rational(11n), [{ source: 'ruler', radius: rational(1n) }]);
  assert.equal(compare(x, y).verdict, 'lt');
  // 若来源不同（独立误差），半径相加为 2，区间 [-3, 1] 重叠 -> 未决。
  const x2 = withErrors(rational(10n), [{ source: 'ruler-A', radius: rational(1n) }]);
  const y2 = withErrors(rational(11n), [{ source: 'ruler-B', radius: rational(1n) }]);
  assert.equal(compare(x2, y2).verdict, 'undecided');
});

test('部分共享来源：共享部分相消、独立部分相加', () => {
  const x = withErrors(rational(10n), [
    { source: 'shared', radius: rational(1n) },
    { source: 'x-noise', radius: rational(1n, 2n) },
  ]);
  const y = withErrors(rational(11n), [
    { source: 'shared', radius: rational(1n, 2n) },
    { source: 'y-noise', radius: rational(1n, 2n) },
  ]);
  // 差值中心 -1；shared 净余 1/2，两个独立噪声各 1/2，总半径 3/2 -> [-5/2, 1/2] 重叠。
  assert.equal(compare(x, y).verdict, 'undecided');
});

test('可恢复细化：预算不足时 pending，追加预算后分离', () => {
  const third = decimalExpansion(1n, 3n); // 0.3, 0.33, 0.333, ...
  const target = exact(rational(334n, 1000n));
  const s1 = compare(third, target, 2); // 只到 0.33±0.01，与 0.334 重叠
  assert.equal(s1.status, 'pending');
  assert.equal(s1.verdict, 'undecided');
  assert.equal(s1.stepsUsed, 2);
  const s2 = resumeCompare(s1, 10); // 恢复后继续细化到分离
  assert.equal(s2.status, 'done');
  assert.equal(s2.verdict, 'lt');
  assert.ok(s2.stepsUsed <= 12);
  assert.ok(s2.trace.length > s1.trace.length);
});

test('区间重叠不蕴含相等：无限逼近 1/3 也永不判 eq', () => {
  const s = compare(decimalExpansion(1n, 3n), exact(rational(1n, 3n)), 12);
  assert.equal(s.status, 'pending');
  assert.equal(s.verdict, 'undecided');
});

test('done 状态幂等：恢复已完成比较返回原判定', () => {
  const s = compare(exact(1n), exact(2n));
  assert.equal(s.status, 'done');
  assert.equal(resumeCompare(s, 10).verdict, 'lt');
});

test('startCompare 不消耗预算', () => {
  const s = startCompare(decimalExpansion(1n, 3n), exact(rational(1n, 3n)));
  assert.equal(s.stepsUsed, 0);
  assert.equal(s.status, 'pending');
});

test('interval 半径为 0 时退化为精确值', () => {
  const v = interval(rational(5n), rational(0n));
  assert.equal(compare(v, exact(5n)).verdict, 'eq');
});
