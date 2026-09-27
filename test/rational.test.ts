import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  Rational,
  fromDecimalParts,
  fractionPage,
  formatDecimal,
  gcd,
} from '../src/rational.ts';

const range = (lo: number, hi: number): bigint[] => {
  const out: bigint[] = [];
  for (let i = lo; i <= hi; i++) out.push(BigInt(i));
  return out;
};

test('穷举规范化：分母恒正、互素、0 统一 0/1、符号在分子', () => {
  for (const n of range(-12, 12)) {
    for (const d of range(-12, 12)) {
      if (d === 0n) continue;
      const r = new Rational(n, d);
      assert.ok(r.den > 0n, `${n}/${d}: den must be positive`);
      if (n === 0n) {
        assert.equal(r.num, 0n);
        assert.equal(r.den, 1n, '0 must normalize to 0/1');
      } else {
        assert.equal(gcd(r.num, r.den), 1n, `${n}/${d}: not reduced`);
        assert.equal(r.num < 0n, n * d < 0n, `${n}/${d}: sign must live in numerator`);
      }
      // 值保持：r.num * d === r.den * n
      assert.equal(r.num * d, r.den * n, `${n}/${d}: value changed`);
    }
  }
  assert.equal(new Rational(6n, -8n).toString(), '-3/4');
  assert.equal(new Rational(-6n, -8n).toString(), '3/4');
});

test('穷举四则：加/减/乘/除与交叉乘恒等式', () => {
  const rs: Rational[] = [];
  for (const n of range(-6, 6)) {
    for (const d of range(-6, 6)) {
      if (d !== 0n) rs.push(new Rational(n, d));
    }
  }
  for (const a of rs) {
    for (const b of rs) {
      // (a+b)-b === a，(a*b)/b === a (b≠0)
      assert.ok(a.add(b).sub(b).equals(a), `(${a}+${b})-${b} != ${a}`);
      if (!b.isZero()) {
        assert.ok(a.mul(b).div(b).equals(a), `(${a}*${b})/${b} != ${a}`);
        assert.ok(a.div(b).mul(b).equals(a), `(${a}/${b})*${b} != ${a}`);
      }
      // 和的交叉乘恒等：(a+b) == (a.num*b.den + b.num*a.den) / (a.den*b.den)
      const s = a.add(b);
      const sn = a.num * b.den + b.num * a.den;
      const sd = a.den * b.den;
      assert.equal(s.num * sd, s.den * sn, `add cross-check failed for ${a}, ${b}`);
      // 差 = 加相反数
      assert.ok(a.sub(b).equals(a.add(b.neg())), `sub != add(neg) for ${a}, ${b}`);
    }
  }
  assert.throws(() => new Rational(1n, 0n), RangeError);
  assert.throws(() => Rational.of(1n).div(Rational.of(0n)), RangeError);
});

test('穷举比较：cmp 与直接交叉乘符号一致', () => {
  const rs: Rational[] = [];
  for (const n of range(-9, 9)) {
    for (const d of range(1, 9)) rs.push(new Rational(n, d));
  }
  const sgn = (x: bigint): -1 | 0 | 1 => (x < 0n ? -1 : x > 0n ? 1 : 0);
  for (const a of rs) {
    for (const b of rs) {
      const expect = sgn(a.num * b.den - b.num * a.den);
      assert.equal(a.cmp(b), expect, `cmp(${a}, ${b})`);
      assert.equal(b.cmp(a), sgn(b.num * a.den - a.num * b.den), 'antisymmetry');
      assert.equal(a.equals(b), expect === 0);
    }
  }
});

test('十进制展开：已知循环节形态', () => {
  assert.equal(Rational.of(1n, 6n).toDecimalString(), '0.1(6)');
  assert.equal(Rational.of(1n, 3n).toDecimalString(), '0.(3)');
  assert.equal(Rational.of(1n, 7n).toDecimalString(), '0.(142857)');
  assert.equal(Rational.of(-22n, 7n).toDecimalString(), '-3.(142857)');
  assert.equal(Rational.of(1n, 8n).toDecimalString(), '0.125');
  assert.equal(Rational.of(0n, 7n).toDecimalString(), '0');
  assert.equal(Rational.of(5n, 2n).toDecimalString(), '2.5');
  assert.equal(Rational.of(-1n, 12n).toDecimalString(), '-0.08(3)');
});

test('穷举循环节回转：展开 -> 还原 == 原值（截断不算精确）', () => {
  for (const n of range(-30, 30)) {
    for (const d of range(1, 30)) {
      const r = new Rational(n, d);
      const e = r.toDecimal({ maxDigits: 5000 });
      assert.ok(e.complete, `${r}: expansion should complete within budget`);
      const back = fromDecimalParts(e.negative, e.integer, e.prefix, e.period);
      assert.ok(back.equals(r), `round-trip failed for ${r}: got ${back}`);
    }
  }
});

test('预算：超预算标记未完成，余数状态表有界不无限增长', () => {
  // 1/97 的循环节长度为 96，预算 10 必然截断
  const e = Rational.of(1n, 97n).toDecimal({ maxDigits: 10 });
  assert.equal(e.complete, false);
  assert.equal(e.digitsEmitted, 10);
  assert.match(formatDecimal(e), /\[incomplete\]$/);
  // 预算充足时完整，循环节长度恰为 96
  const full = Rational.of(1n, 97n).toDecimal({ maxDigits: 200 });
  assert.ok(full.complete);
  assert.equal(full.period.length, 96);
  assert.ok(fromDecimalParts(full.negative, full.integer, full.prefix, full.period).equals(Rational.of(1n, 97n)));
});

test('分页输出：任意页直接定位，无需展开整串', () => {
  const e = Rational.of(1n, 7n).toDecimal();
  assert.equal(fractionPage(e, 0, 3).digits, '142');
  assert.equal(fractionPage(e, 1, 3).digits, '857');
  assert.equal(fractionPage(e, 2, 3).digits, '142');
  assert.ok(fractionPage(e, 1000, 5).hasMore);
  // 1/6 = 0.1(6)：前缀 1 位后进入周期
  const s = Rational.of(1n, 6n).toDecimal();
  assert.equal(fractionPage(s, 0, 4).digits, '1666');
  assert.equal(fractionPage(s, 3, 4).digits, '6666');
  // 有限小数 1/8 = 0.125，超出后补 0 且 hasMore=false
  const t = Rational.of(1n, 8n).toDecimal();
  const p = fractionPage(t, 1, 3);
  assert.equal(p.digits, '000');
  assert.equal(p.hasMore, false);
});

test('显式舍入：五种模式，结果不冒充精确值', () => {
  const x = Rational.of(1n, 8n); // 0.125
  assert.equal(x.toFixed(2, 'trunc'), '0.12');
  assert.equal(x.toFixed(2, 'half-up'), '0.13');
  assert.equal(x.toFixed(2, 'half-even'), '0.12');
  assert.equal(x.toFixed(2, 'floor'), '0.12');
  assert.equal(x.toFixed(2, 'ceil'), '0.13');
  const nx = Rational.of(-1n, 8n); // -0.125
  assert.equal(nx.toFixed(2, 'trunc'), '-0.12');
  assert.equal(nx.toFixed(2, 'half-up'), '-0.13');
  assert.equal(nx.toFixed(2, 'half-even'), '-0.12');
  assert.equal(nx.toFixed(2, 'floor'), '-0.13');
  assert.equal(nx.toFixed(2, 'ceil'), '-0.12');
  assert.equal(Rational.of(5n, 2n).toFixed(0, 'half-even'), '2');
  assert.equal(Rational.of(3n, 2n).toFixed(0, 'half-even'), '2');
  assert.equal(Rational.of(7n, 2n).toFixed(0, 'half-up'), '4');
});

test('大数不经过 Number：远超 2^53 仍精确', () => {
  const big = (1n << 90n) + 1n;
  const a = new Rational(big, 3n);
  const b = a.mul(Rational.of(3n));
  assert.equal(b.num, big);
  assert.equal(b.den, 1n);
  // 交叉乘比较两个相邻巨分数，Number 无法区分
  const u = new Rational(1n << 80n, (1n << 40n) + 1n);
  const v = new Rational((1n << 80n) + 1n, (1n << 40n) + 2n);
  assert.equal(u.cmp(v), 1);
  // 大分母循环节回转
  const w = new Rational(12345678901234567890n, 99999999999999999999n);
  const e = w.toDecimal({ maxDigits: 1000 });
  assert.ok(e.complete);
  assert.ok(fromDecimalParts(e.negative, e.integer, e.prefix, e.period).equals(w));
});
