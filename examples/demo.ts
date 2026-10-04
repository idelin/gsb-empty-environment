/**
 * Runnable tour of the library: node examples/demo.ts
 */
import { Dec } from '../src/decimal.ts';
import { Rat } from '../src/rational.ts';
import * as Iv from '../src/interval.ts';
import { Affine, AffineCtx } from '../src/affine.ts';
import { Domain } from '../src/multi.ts';
import { checkBinaryOp, checkCertificate, containsRat } from '../src/certificate.ts';

const d = (s: string) => Dec.fromString(s);
const show = (label: string, value: unknown) => console.log(`${label.padEnd(58)} ${value}`);

console.log('=== 1. Exact decimals, explicit directed rounding (prec = 10) ===');
show('1/3  floor:', Dec.div(Dec.one, d('3'), 10, 'floor').toString());
show('1/3  ceil :', Dec.div(Dec.one, d('3'), 10, 'ceil').toString());
show('-1/3 floor:', Dec.div(d('-1'), d('3'), 10, 'floor').toString());
show('-1/3 ceil :', Dec.div(d('-1'), d('3'), 10, 'ceil').toString());
show('40-digit mantissa + 1 (exact, no Number):', Dec.add(Dec.fromString('9'.repeat(40)), Dec.one).toString());

console.log('\n=== 2. Interval arithmetic with outward rounding ===');
const q = Iv.div(Iv.point(d('1')), Iv.point(d('3')), 3);
show('[1,1] / [3,3]  (prec 3):', Iv.toString(q));
show('contains exact 1/3?', containsRat(q, Rat.of(1n, 3n)));
show('[-2,3] * [4,5]:', Iv.toString(Iv.mul(Iv.range(d('-2'), d('3')), Iv.range(d('4'), d('5')), 10)));
show('[2,3] scaled by -1 (bounds flip):', Iv.toString(Iv.scale(Iv.range(d('2'), d('3')), d('-1'), 10)));
show('empty vs unknown:', `${Iv.toString(Iv.EMPTY)}  vs  ${Iv.toString(Iv.UNKNOWN)}`);

console.log('\n=== 3. Affine forms: correlated symbols, no false widening ===');
const ctx = new AffineCtx(10);
const x = ctx.variable(d('1'), d('2'));
show('x - x (same symbol):', Iv.toString(Affine.sub(x, x).toInterval(10)));
show('(x + 1) - x:', Iv.toString(Affine.sub(Affine.add(x, ctx.constant(d('1'))), x).toInterval(10)));
const naive = Iv.sub(Iv.add(Iv.range(d('1'), d('2')), Iv.point(d('1')), 10), Iv.range(d('1'), d('2')), 10);
show('same expression with plain intervals:', Iv.toString(naive));

console.log('\n=== 4. Nonlinear steps emit conservative remainder certificates ===');
const ctx2 = new AffineCtx(12);
const t = ctx2.variable(d('1'), d('2'));
const expr = Affine.sub(Affine.mul(ctx2, t, t), t); // t^2 - t on [1, 2]
show('t^2 - t  affine bounds:', Iv.toString(expr.toInterval(12)));
for (const c of ctx2.certificates) {
  const chk = checkCertificate(c);
  show(`certificate ${c.op} (sym ${c.sym}), remainder ${c.remainder.toString()}:`, chk.ok ? `VERIFIED — ${chk.detail}` : `REJECTED — ${chk.detail}`);
}
let allInside = true;
const iv = expr.toInterval(12);
for (let i = 0; i <= 20; i++) {
  const tv = Rat.of(20n + BigInt(i), 20n);
  if (!containsRat(iv, Rat.sub(Rat.mul(tv, tv), tv))) allInside = false;
}
show('21 exact rational samples of t^2-t inside bounds?', allInside);

console.log('\n=== 5. Division across zero: split or reject ===');
const num = Domain.of(Iv.point(d('1')));
const den = Domain.of(Iv.range(d('-2'), d('3')));
const split = num.div(den, 10, { mode: 'split', maxParts: 8 });
show('1 / [-2,3] split into parts:', split.parts.map(Iv.toString).join('  ∪  '));
show('precisionLoss?', split.precisionLoss);
try {
  num.div(den, 10, { mode: 'reject', maxParts: 8 });
} catch (e) {
  show('reject mode:', `${(e as Error).name}: ${(e as Error).message}`);
}
const capped = num.div(den, 10, { mode: 'split', maxParts: 1 });
show('cap=1 forces hull:', capped.parts.map(Iv.toString).join('  ∪  '));
show('precisionLoss note:', capped.note);

console.log('\n=== 6. Independent checker: outward direction + counterexample search ===');
const a = Iv.range(d('1'), d('2'));
const b = Iv.range(d('1'), d('2'));
const good = Iv.mul(a, b, 6);
show('checkBinaryOp(mul, [1,2], [1,2], correct):', JSON.stringify(checkBinaryOp('mul', a, b, good, 6).ok));
const bad = Iv.range(d('1.5'), d('4'));
const chk = checkBinaryOp('mul', a, b, bad, 6);
show('checkBinaryOp(..., shrunk [1.5,4]):', `ok=${chk.ok}, loOutward=${chk.loOutward}`);
show('counterexample found:', chk.counterexample ? `point ${chk.counterexample.point} -> ${chk.counterexample.value}` : 'none');
