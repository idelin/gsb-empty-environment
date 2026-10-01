// 有限精确数参考核验：用确定性伪随机生成的精确有理数作为参考，
// 核验比较协议的全序性、反对称性、传递性，以及未决（pending）状态的行为。

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compare, compareExact, resumeCompare } from '../src/compare.ts';
import { add, cmp, rational, sub, toString } from '../src/rational.ts';
import type { Rational } from '../src/rational.ts';
import { exact, interval } from '../src/value.ts';

function lcg(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0;
    return s / 2 ** 32;
  };
}

function sampleRationals(count: number, seed: number): Rational[] {
  const rand = lcg(seed);
  const seen = new Map<string, Rational>();
  while (seen.size < count) {
    const num = BigInt(Math.floor(rand() * 121) - 60);
    const den = BigInt(1 + Math.floor(rand() * 25));
    const r = rational(num, den);
    seen.set(toString(r), r);
  }
  return [...seen.values()];
}

const VALUES = sampleRationals(120, 20261001);

test('全序性：精确值比较永不未决，且与参考 cmp 一致', () => {
  for (const a of VALUES) {
    for (const b of VALUES) {
      const s = compare(exact(a), exact(b));
      assert.equal(s.status, 'done');
      assert.notEqual(s.verdict, 'undecided');
      assert.equal(s.verdict, compareExact(a, b), `compare(${toString(a)}, ${toString(b)})`);
    }
  }
});

test('反对称性：compare(a,b) 与 compare(b,a) 互反', () => {
  const flip: Record<string, string> = { lt: 'gt', gt: 'lt', eq: 'eq' };
  for (const a of VALUES) {
    for (const b of VALUES) {
      const ab = compare(exact(a), exact(b)).verdict;
      const ba = compare(exact(b), exact(a)).verdict;
      assert.equal(ba, flip[ab]);
    }
  }
});

test('传递性：a<b 且 b<c 蕴含 a<c（抽样三元组）', () => {
  const rand = lcg(42);
  const verdict = (a: Rational, b: Rational) => compare(exact(a), exact(b)).verdict;
  for (let i = 0; i < 5000; i++) {
    const a = VALUES[Math.floor(rand() * VALUES.length)];
    const b = VALUES[Math.floor(rand() * VALUES.length)];
    const c = VALUES[Math.floor(rand() * VALUES.length)];
    if (verdict(a, b) === 'lt' && verdict(b, c) === 'lt') {
      assert.equal(verdict(a, c), 'lt', `transitivity violated at ${toString(a)}, ${toString(b)}, ${toString(c)}`);
    }
    if (verdict(a, b) === 'eq' && verdict(b, c) === 'eq') {
      assert.equal(verdict(a, c), 'eq');
    }
  }
});

test('未决状态：区间分离则确定，重叠则 pending 且恢复不改变结论', () => {
  const rand = lcg(7);
  for (let i = 0; i < 300; i++) {
    const c1 = rational(BigInt(Math.floor(rand() * 200) - 100));
    const c2 = rational(BigInt(Math.floor(rand() * 200) - 100));
    const r1 = rational(BigInt(Math.floor(rand() * 10)));
    const r2 = rational(BigInt(Math.floor(rand() * 10)));
    const v1 = interval(c1, r1, 's1');
    const v2 = interval(c2, r2, 's2');
    const s = compare(v1, v2, 50);
    const separated = cmp(add(c1, r1), sub(c2, r2)) < 0 || cmp(add(c2, r2), sub(c1, r1)) < 0;
    if (separated) {
      assert.equal(s.status, 'done');
      assert.notEqual(s.verdict, 'undecided');
      // 与“中心序”一致：分离区间的方向由中心决定
      const centerOrder = cmp(c1, c2);
      assert.equal(s.verdict, centerOrder < 0 ? 'lt' : 'gt');
    } else if (cmp(r1, rational(0n)) === 0 && cmp(r2, rational(0n)) === 0) {
      // 两个都是点：必然可判定
      assert.equal(s.status, 'done');
    } else {
      assert.equal(s.status, 'pending');
      assert.equal(s.verdict, 'undecided');
      const resumed = resumeCompare(s, 100);
      assert.equal(resumed.status, 'pending'); // 非可细化区间：追加预算也不会误判
      assert.equal(resumed.verdict, 'undecided');
    }
  }
});
