/**
 * Runnable tour of the library:  node samples/demo.ts
 */
import { Decimal } from '../src/decimal.ts';
import {
  EMPTY, UNKNOWN, ZeroCrossingError, add, containsPoint, div, interval, mul, show, sub,
} from '../src/interval.ts';
import type { IntervalOpCert } from '../src/interval.ts';
import { IntervalUnion } from '../src/union.ts';
import { AffineForm } from '../src/affine.ts';
import { verifyAll } from '../src/verify.ts';
import { sampledTruthCheck, searchIntervalOp } from '../src/search.ts';
import type { Arith } from '../src/search.ts';

const d = (s: string) => Decimal.parse(s);
const iv = (lo: string, hi: string) => interval(d(lo), d(hi));
const line = (t: string) => console.log(`\n=== ${t} ===`);

line('1. Exact decimal, outward rounding to scale 6 (no Number intermediates)');
{
  const big = Decimal.parse('123456789012345678901234567890.123456789');
  console.log('parsed exactly :', big.toString());
  console.log('1/3 floor/ceil :', Decimal.ONE.div(d('3'), 6, 'floor').toString(), '/',
    Decimal.ONE.div(d('3'), 6, 'ceil').toString());
  console.log('-1/3 floor/ceil:', Decimal.ONE.neg().div(d('3'), 6, 'floor').toString(), '/',
    Decimal.ONE.neg().div(d('3'), 6, 'ceil').toString());
  const certs: IntervalOpCert[] = [];
  console.log('[1,1]/[3,3]   :', show(div(iv('1', '1'), iv('3', '3'), 6, 'reject', certs)[0]!));
  console.log('certificate ok :', verifyAll(certs).ok);
}

line('2. Empty vs unknown are different kinds');
{
  console.log('EMPTY contains 0?   ', containsPoint(EMPTY, d('0')));
  console.log('UNKNOWN contains 0? ', containsPoint(UNKNOWN, d('0')));
  console.log('UNKNOWN + [1,2]     :', show(add(UNKNOWN, iv('1', '2'), 6)));
  console.log('EMPTY + [1,2]       :', show(add(EMPTY, iv('1', '2'), 6)));
}

line('3. Division across zero: explicit split or reject');
{
  const u = IntervalUnion.div(iv('1', '2'), iv('-1', '3'), 6, 'split');
  console.log('[1,2]/[-1,3] split :', u.toString());
  try {
    div(iv('1', '2'), iv('-1', '3'), 6, 'reject');
  } catch (e) {
    console.log('[1,2]/[-1,3] reject:', (e as ZeroCrossingError).message);
  }
}

line('4. Segment limit exceeded -> conservative hull + precision-loss note');
{
  const segs = [iv('0', '1'), iv('3', '4'), iv('6', '7'), iv('9', '10')];
  const u = IntervalUnion.fromSegments(segs, 2);
  console.log('4 segments, limit 2:', u.toString());
}

line('5. Correlation: same uncertain input keeps its noise symbol');
{
  const x = AffineForm.fromInterval(iv('0.9', '1.1'));
  console.log('affine x            :', x.toString());
  console.log('x - x (affine)      :', show(x.sub(x).toInterval(10)));
  console.log('x - x (naive interval):', show(sub(iv('0.9', '1.1'), iv('0.9', '1.1'), 10)));
  const x2 = x.mul(x, 10);
  console.log('x*x                 :', x2.form.toString(), '->', show(x2.form.toInterval(10)));
  console.log('mul certificate ok  :', verifyAll([x2.cert]).ok);
}

line('6. Nonlinear step with conservative remainder certificate');
{
  const y = AffineForm.fromInterval(iv('2', '4'));
  const r = y.recip(6);
  console.log('1/[2,4] affine      :', r.form.toString());
  console.log('recip range         :', show(r.form.toInterval(6)));
  console.log('certificate ok      :', verifyAll([r.cert]).ok);
}

line('7. Negative coefficient flips bounds');
{
  const x = AffineForm.fromInterval(iv('-2', '4'));
  console.log('x range             :', show(x.toInterval(6)));
  console.log('(-1)*x range        :', show(x.scaleBy(d('-1')).toInterval(6)));
  console.log('[1,2]*[-1,-1]       :', show(mul(iv('1', '2'), iv('-1', '-1'), 6)));
}

line('8. Counterexample search (exact rationals) + sampled truth');
{
  const certs: IntervalOpCert[] = [];
  mul(iv('-1.5', '2'), iv('0.25', '0.5'), 6, certs);
  console.log('honest cert, counterexample:', searchIntervalOp(certs[0]!) ?? 'none found');
  const f = <T>(ops: Arith<T>, [x]: T[]): T =>
    ops.div(ops.mul(x, x), ops.add(x, ops.constant('1')));
  const r = sampledTruthCheck(f, [iv('0.5', '1.5')], 8);
  console.log(`sampled truth: ${r.checked} exact samples checked, ${r.violations.length} violations`);
  console.log('output interval:', show(r.output));
  console.log('chain certificates ok:', verifyAll([...r.certs]).ok);
}
