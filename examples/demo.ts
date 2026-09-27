import { Rational, fractionPage, formatDecimal } from '../src/rational.ts';
import { add, mul, div, lit, evaluate, compareExprs, type Expr } from '../src/dag.ts';

const show = (label: string, v: unknown) => console.log(`${label.padEnd(34)} ${v}`);

console.log('== 规范化 ==');
show('6/-8 ->', new Rational(6n, -8n).toString());
show('-6/-8 ->', new Rational(-6n, -8n).toString());
show('0/-7 ->', new Rational(0n, -7n).toString());

console.log('\n== 四则与交叉乘比较 ==');
const a = Rational.of(1n, 3n), b = Rational.of(1n, 6n);
show('1/3 + 1/6 =', a.add(b).toString());
show('1/3 - 1/6 =', a.sub(b).toString());
show('1/3 * 1/6 =', a.mul(b).toString());
show('1/3 / 1/6 =', a.div(b).toString());
show('cmp(355/113, 22/7) =', Rational.of(355n, 113n).cmp(Rational.of(22n, 7n)));

console.log('\n== 循环节十进制 ==');
for (const [n, d] of [[1n, 6n], [1n, 7n], [-22n, 7n], [1n, 12n], [1n, 8n]] as const) {
  show(`${n}/${d} =`, Rational.of(n, d).toDecimalString());
}
show('1/97 (预算 10) =', Rational.of(1n, 97n).toDecimalString({ maxDigits: 10 }));
show('1/97 (预算 200) 周期长度 =', Rational.of(1n, 97n).toDecimal({ maxDigits: 200 }).period.length);

console.log('\n== 分页输出（1/7，每页 10 位） ==');
const e7 = Rational.of(1n, 7n).toDecimal();
for (let p = 0; p < 3; p++) {
  const page = fractionPage(e7, p, 10);
  show(`page ${page.page}`, `${page.digits} (hasMore=${page.hasMore})`);
}

console.log('\n== 显式舍入（1/8 = 0.125，保留 2 位） ==');
for (const m of ['trunc', 'floor', 'ceil', 'half-up', 'half-even'] as const) {
  show(m, Rational.of(1n, 8n).toFixed(2, m));
}

const showEval = (label: string, r: ReturnType<typeof evaluate>) =>
  show(label, r.status === 'ok' ? r.value.toString() : `incomplete: ${r.reason}`);
const showCmp = (label: string, r: ReturnType<typeof compareExprs>) =>
  show(label, r.status === 'ok' ? String(r.order) : `incomplete: ${r.reason}`);

console.log('\n== 表达式 DAG：延迟约分 + 位长预算 ==');
const expr = mul(add(lit(1n, 2n), lit(1n, 3n)), lit(3n));
showEval('(1/2 + 1/3) * 3 =', evaluate(expr));
let chain: Expr = lit(2n, 3n);
for (let i = 0; i < 200; i++) chain = mul(chain, lit(2n, 3n));
showEval('(2/3)^201 @ maxBits=64 =', evaluate(chain, { maxBits: 64 }));
show('(2/3)^201 @ maxBits=4096 =', evaluate(chain, { maxBits: 4096 }).status);
showCmp('1/3+1/6 vs 1/2 =', compareExprs(add(lit(1n, 3n), lit(1n, 6n)), lit(1n, 2n)));
showCmp('355/113 vs 22/7 =', compareExprs(lit(355n, 113n), lit(22n, 7n)));
const huge = div(lit((1n << 40n) * 3n), lit(1n << 40n));
showEval('(2^40*3)/(2^40) @ maxBits=50 =', evaluate(huge, { maxBits: 50 }));
