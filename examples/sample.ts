import { CalibrationTable, CalibrationConflictError } from '../src/index.ts';

const table = new CalibrationTable([
  { x: '0', y: '0' },
  { x: '10', y: '10' },
  { x: '15', y: '10' }, // plateau at y = 10
  { x: '25', y: '0' }, // falling slope -> non-monotonic table
]);

const show = (label: string, v: unknown) => console.log(label.padEnd(34), v);

console.log('=== forward conversion (scale=4, half-up) ===');
for (const x of ['5', '12', '20']) {
  const r = table.forward(x, { scale: 4, rounding: 'half-up', outOfRange: 'error' })!;
  show(`x=${x} -> y`, `${r.value}  (exact ${r.exact}, |err| <= ${r.errorBound})`);
}

console.log('\n=== non-terminating exact value keeps its error bound ===');
const t3 = new CalibrationTable([
  { x: '0', y: '0' },
  { x: '3', y: '1' },
]);
const r3 = t3.forward('1', { scale: 2, rounding: 'half-up', outOfRange: 'error' })!;
show('x=1 on (0,0)-(3,1)', `value ${r3.value}, exact ${r3.exact}, exactError ${r3.exactError}, bound ${r3.errorBound}`);

console.log('\n=== inverse: rise-then-fall gives two solutions ===');
const inv5 = table.inverse('5', { scale: 4, rounding: 'half-up', outOfRange: 'error' });
show('y=5 -> x points', inv5.points.map((p) => p.value).join(', '));

console.log('\n=== inverse: plateau value gives an interval ===');
const inv10 = table.inverse('10', { scale: 4, rounding: 'half-up', outOfRange: 'error' });
for (const iv of inv10.intervals) {
  show('y=10 -> x in', `[${iv.from.value}, ${iv.to.value}]`);
}

console.log('\n=== explicit out-of-range policies ===');
try {
  table.forward('99', { scale: 4, rounding: 'half-up', outOfRange: 'error' });
} catch (e) {
  show("forward(99, 'error')", `${(e as Error).name}: ${(e as Error).message}`);
}
show("forward(99, 'clamp')", table.forward('99', { scale: 4, rounding: 'half-up', outOfRange: 'clamp' })!.value + ' (clamped)');
show("forward(99, 'null')", String(table.forward('99', { scale: 4, rounding: 'half-up', outOfRange: 'null' })));
show("inverse(42, 'empty')", JSON.stringify(table.inverse('42', { scale: 4, rounding: 'half-up', outOfRange: 'empty' })));

console.log('\n=== big integers never pass through Number ===');
const big = new CalibrationTable([
  { x: '0', y: '0' },
  { x: '18014398509481984', y: '1' }, // 2^54
]);
const rb = big.forward('9007199254740993', { scale: 20, rounding: 'half-up', outOfRange: 'error' })!;
show('forward(2^53+1)', rb.value);
show('exact', rb.exact);
show('error bound', rb.errorBound);

console.log('\n=== conflicting duplicate inputs are rejected ===');
try {
  new CalibrationTable([
    { x: '1', y: '10' },
    { x: '1.0', y: '11' },
  ]);
} catch (e) {
  if (e instanceof CalibrationConflictError) show('duplicate x=1', e.message);
}
