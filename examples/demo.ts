import { Decimal, Context, Flag, RoundingMode } from '../src/index.ts';

const d = (s: string) => Decimal.of(s);
const show = (label: string, value: unknown) => console.log(`${label.padEnd(46)} ${value}`);

console.log('=== Exact arithmetic (BigInt based, no Number intermediates) ===');
show('0.1 + 0.2', d('0.1').add(d('0.2')).toString());
show('0.3 - 0.1', d('0.3').subtract(d('0.1')).toString());
show('1.1 * 1.1', d('1.1').multiply(d('1.1')).toString());
show('12345678901234567890 * 98765432109876543210',
  d('12345678901234567890').multiply(d('98765432109876543210')).toString());
show('2^64 + 1 (beyond 2^53)', d('18446744073709551616').add(1).toString());

console.log('\n=== Division: exact vs inexact vs division by zero ===');
const ctx = new Context({ precision: 10 });
show('1 / 2 (precision 10)', d('1').divide(d('2'), ctx).toString());
show('flags after exact division', `[${[...ctx.flags].join(', ')}]`);
ctx.clearFlags();
show('1 / 3 (precision 10)', d('1').divide(d('3'), ctx).toString());
show('flags after inexact division', `[${[...ctx.flags].join(', ')}]`);
ctx.clearFlags();
try {
  d('1').divide(d('0'), ctx);
} catch (err) {
  show('1 / 0 throws', `${(err as Error).name}: ${(err as Error).message}`);
}
show('flags after division by zero', `[${[...ctx.flags].join(', ')}]`);
show('DIVISION_BY_ZERO distinct from INEXACT',
  String(ctx.hasFlag(Flag.DIVISION_BY_ZERO) && !ctx.hasFlag(Flag.INEXACT)));

console.log('\n=== Independent context precision ===');
const coarse = new Context({ precision: 3 });
const fine = new Context({ precision: 15 });
show('1 / 7 with precision 3', d('1').divide(d('7'), coarse).toString());
show('1 / 7 with precision 15', d('1').divide(d('7'), fine).toString());

console.log('\n=== Rounding modes at +/- midpoints (quantize to integer) ===');
const samples = ['2.5', '-2.5', '3.5', '-3.5', '2.1', '-2.1'];
const modes: RoundingMode[] = ['HALF_EVEN', 'HALF_UP', 'HALF_DOWN', 'UP', 'DOWN', 'CEILING', 'FLOOR'];
console.log('mode'.padEnd(12) + samples.map((s) => s.padStart(7)).join(''));
for (const mode of modes) {
  const row = samples.map((s) => d(s).quantize(0, mode).toString().padStart(7)).join('');
  console.log(mode.padEnd(12) + row);
}

console.log('\n=== Quantize to cents ===');
show('3.145 -> 2dp HALF_UP', d('3.145').quantize(-2, 'HALF_UP').toString());
show('-3.145 -> 2dp HALF_UP', d('-3.145').quantize(-2, 'HALF_UP').toString());
show('3.14159 -> 2dp DOWN', d('3.14159').quantize(-2, 'DOWN').toString());
