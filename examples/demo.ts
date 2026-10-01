import { d } from '../src/decimal.ts';
import { Context, Flag } from '../src/context.ts';
import { Rounding } from '../src/rounding.ts';

function show(label: string, value: unknown): void {
  console.log(`${label.padEnd(46)} ${value}`);
}

console.log('=== exact decimal arithmetic (BigInt based, no Number detour) ===\n');

show('0.1 + 0.2', d('0.1').add('0.2').toString());
show('0.1 + 0.2 === 0.3 ?', d('0.1').add('0.2').eq('0.3'));
show('9007199254740993 + 1 (past 2^53)', d('9007199254740993').add('1').toString());
const wide = new Context({ precision: 50 });
show(
  '123456789123456789 * 987654321987654321',
  d('123456789123456789').mul('987654321987654321', wide).toString(),
);

console.log('\n=== independent contexts: precision + rounding per operation ===\n');

const coarse = new Context({ precision: 5 });
const fine = new Context({ precision: 30 });
show('1 / 3 @ precision 5', d('1').div('3', coarse).toString());
show('1 / 3 @ precision 30', d('1').div('3', fine).toString());
show('coarse flags', [...coarse.flags].join(', '));
show('fine flags', [...fine.flags].join(', '));

console.log('\n=== rounding modes on +/- midpoints (quantize to integer) ===\n');

for (const mode of Object.values(Rounding)) {
  const ctx = new Context({ precision: 50, rounding: mode });
  const pos = d('2.5').quantize(0, ctx).toString();
  const neg = d('-2.5').quantize(0, ctx).toString();
  show(`${mode}: 2.5 / -2.5`, `${pos} / ${neg}`);
}

console.log('\n=== error flags: division by zero vs inexact result ===\n');

const ctx = new Context({ precision: 10 });
show('1 / 0', d('1').div('0', ctx).toString());
show('flags after 1/0', [...ctx.flags].join(', '));
ctx.clearFlags();
show('1 / 3 (precision 10)', d('1').div('3', ctx).toString());
show('flags after 1/3', [...ctx.flags].join(', '));
ctx.clearFlags();
show('1 / 4 (exact)', d('1').div('4', ctx).toString());
show('flags after 1/4', ctx.flags.size === 0 ? '(none)' : [...ctx.flags].join(', '));
show('0 / 0', d('0').div('0', ctx).toString());
show('flags after 0/0', [...ctx.flags].join(', '));

console.log('\n=== quantize to cents ===\n');

const money = new Context({ precision: 28, rounding: Rounding.HALF_UP });
show('2.005 -> cents (HALF_UP)', d('2.005').quantize(-2, money).toString());
show('-2.005 -> cents (HALF_UP)', d('-2.005').quantize(-2, money).toString());
const bankers = new Context({ precision: 28, rounding: Rounding.HALF_EVEN });
show('2.005 -> cents (HALF_EVEN)', d('2.005').quantize(-2, bankers).toString());
show('has Inexact flag?', money.hasFlag(Flag.Inexact));
