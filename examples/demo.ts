/**
 * Demo: exact comparison of algebraic numbers.
 * Every step uses exact rational (BigInt) arithmetic — no floating point.
 */
import { Alg, isolateRoots, tightenInterval, type Interval } from '../src/index.ts';

const r = (n: number) => Alg.rational(n);
const sq = (n: number) => Alg.sqrt(r(n));

const fmtIv = (iv: Interval) => `[${iv.lo.toDecimal(8)}, ${iv.hi.toDecimal(8)}]`;
const verdict = (c: number) => (c === 0 ? '==' : c < 0 ? '<' : '>');

function showComparison(nameA: string, a: Alg, nameB: string, b: Alg): void {
  console.log(`比较 ${nameA} 与 ${nameB}`);
  const ma = a.minimalPolynomial();
  const mb = b.minimalPolynomial();
  console.log(`  极小多项式(${nameA}) = ${ma}`);
  console.log(`  极小多项式(${nameB}) = ${mb}`);
  if (ma.equals(mb)) {
    console.log('  极小多项式相同 → 用实根隔离区间判定是否为同一个实根:');
    const ivs = isolateRoots(ma).map((iv) => tightenInterval(ma, iv, 24));
    ivs.forEach((iv, i) => console.log(`    实根 #${i + 1} ∈ ${fmtIv(iv)}`));
  } else {
    console.log('  极小多项式不同 → 两数必不相等，加细区间直至分离');
  }
  const c = a.compare(b);
  console.log(`  区间证据: ${nameA} ≈ ${a.approxDecimal(12)}, ${nameB} ≈ ${b.approxDecimal(12)}`);
  console.log(`  结论: ${nameA} ${verdict(c)} ${nameB}\n`);
}

console.log('=== 代数数精确比较演示（全程 BigInt 有理数运算，无浮点近似）===\n');

// 验收样例 1：sqrt(2) + sqrt(3) ?= sqrt(5 + 2*sqrt(6))
const a1 = sq(2).add(sq(3));
const b1 = Alg.sqrt(r(5).add(r(2).mul(sq(6))));
showComparison('sqrt(2)+sqrt(3)', a1, 'sqrt(5+2*sqrt(6))', b1);

// 验收样例 2：sqrt(2) + sqrt(3) ?= sqrt(10)
const a2 = sq(2).add(sq(3));
showComparison('sqrt(2)+sqrt(3)', a2, 'sqrt(10)', sq(10));

// 更多样例
showComparison('sqrt(7+2*sqrt(10))', Alg.sqrt(r(7).add(r(2).mul(sq(10)))), 'sqrt(2)+sqrt(5)', sq(2).add(sq(5)));
showComparison('sqrt(2)*sqrt(2)', sq(2).mul(sq(2)), '2', r(2));
showComparison('2+sqrt(3)', r(2).add(sq(3)), '4', r(4));
