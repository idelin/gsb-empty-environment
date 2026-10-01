// 运行样例：node examples/demo.ts（Node >= 23.6 直接运行 TypeScript）

import { compare, resumeCompare } from '../src/compare.ts';
import type { CompareState } from '../src/compare.ts';
import { addDecimal, decimal, decimalToString, divideDecimal } from '../src/decimal.ts';
import { parseValue } from '../src/parse.ts';
import { rational, toString as ratStr } from '../src/rational.ts';
import type { Rational } from '../src/rational.ts';
import { decimalExpansion, exact, withErrors } from '../src/value.ts';

function fmt(r: Rational): string {
  const approx = divideDecimal(r.num, r.den, 6, 'truncate');
  return `${ratStr(r)} (≈${decimalToString(approx)})`;
}

function show(label: string, s: CompareState): void {
  console.log(`  ${label}: verdict=${s.verdict} status=${s.status} stepsUsed=${s.stepsUsed}`);
  for (const t of s.trace) {
    console.log(`    step ${t.step}: a-b ∈ [${fmt(t.lower)}, ${fmt(t.upper)}]${t.exact ? ' (exact)' : ''}`);
  }
}

console.log('1) 大整数相差 1（2^90 vs 2^90+1），不经过 Number：');
show('2^90 vs 2^90+1', compare(exact(2n ** 90n), exact(2n ** 90n + 1n)));

console.log('\n2) 0.1 + 0.2 === 0.3（精确十进制，无浮点误差）：');
const sum = addDecimal(decimal(1n, 1), decimal(2n, 1));
console.log(`  0.1 + 0.2 = ${decimalToString(sum)}`);
show('0.1+0.2 vs 3/10', compare(exact(rational(sum.coeff, 10n ** BigInt(sum.scale))), exact(rational(3n, 10n))));

console.log('\n3) 误差区间：重叠不蕴含相等；共享来源的误差相消：');
show('1±0.5 vs 1.2±0.5 (独立来源)', compare(parseValue('1±0.5'), parseValue('1.2±0.5')));
const shared = compare(
  withErrors(rational(10n), [{ source: 'ruler', radius: rational(1n) }]),
  withErrors(rational(11n), [{ source: 'ruler', radius: rational(1n) }]),
);
show('10±1 vs 11±1 (同一把尺子)', shared);

console.log('\n4) 可恢复细化协议：1/3 的十进制展开 vs 0.334，预算 2 不够，恢复后分离：');
const s1 = compare(decimalExpansion(1n, 3n), parseValue('0.334'), 2);
show('budget=2', s1);
const s2 = resumeCompare(s1, 10);
show('resume(+10)', s2);

console.log('\n5) 无限逼近也不误判相等：1/3 展开 vs 精确 1/3，预算 12 仍 undecided：');
show('expansion(1/3) vs 1/3', compare(decimalExpansion(1n, 3n), exact(rational(1n, 3n)), 12));

console.log('\n6) NaN 字面量按不支持拒绝：');
try {
  parseValue('NaN');
} catch (e) {
  console.log(`  parseValue('NaN') -> ${(e as Error).name}: ${(e as Error).message}`);
}
