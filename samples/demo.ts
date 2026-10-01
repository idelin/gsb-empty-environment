import { Rat } from '../src/rational.ts';
import { compileLadder, evaluate } from '../src/ladder.ts';
import type { Evaluation, LadderRule } from '../src/ladder.ts';
import { invert } from '../src/inverse.ts';
import type { Interval } from '../src/inverse.ts';
import { validateOnGrid } from '../src/validate.ts';

const r = (s: string) => Rat.parse(s);
const fmt = (v: Rat) => v.toDecimalString();
const fmtIv = (iv: Interval) =>
  `${iv.lowerClosed ? '[' : '('}${fmt(iv.lower)}, ${fmt(iv.upper)}${iv.upperClosed ? ']' : ')'}`;

// A composed piecewise function: a progressive tiered rule (marginal rates,
// rounded at the rule total) stacked with a flat fee ladder (whole amount
// falls into one bracket) carrying a discount plus min/max, and a final
// rounding on the grand total. Pure numeric function; no order/billing I/O.
const tiered: LadderRule = {
  name: 'tiered',
  mode: 'progressive',
  brackets: [
    { lower: r('0'), lowerClosed: true, upper: r('50'), upperClosed: true, rate: r('0.1') },
    { lower: r('50'), lowerClosed: false, upper: r('200'), upperClosed: true, rate: r('0.2') },
    { lower: r('200'), lowerClosed: false, upper: null, upperClosed: false, rate: r('0.3') },
  ],
  rounding: { quantum: r('0.1'), mode: 'half-up', at: 'rule-total' },
};

const fee: LadderRule = {
  name: 'fee',
  mode: 'flat',
  brackets: [
    { lower: r('-100'), lowerClosed: true, upper: r('0'), upperClosed: false, rate: r('0'), offset: r('1') },
    { lower: r('0'), lowerClosed: true, upper: r('100'), upperClosed: false, rate: r('0'), offset: r('5') },
    { lower: r('100'), lowerClosed: true, upper: r('500'), upperClosed: true, rate: r('0'), offset: r('3') },
    { lower: r('500'), lowerClosed: false, upper: null, upperClosed: false, rate: r('0'), offset: r('0') },
  ],
  discount: r('1.5'),
  min: r('1'),
  max: r('4'),
};

const ladder = compileLadder({
  rules: [tiered, fee],
  finalRounding: { quantum: r('1'), mode: 'floor' },
});

function showEvaluation(x: string) {
  const ev: Evaluation = evaluate(ladder, r(x));
  console.log(`\nx = ${x}`);
  for (const rule of ev.rules) {
    console.log(`  rule ${rule.name} (${rule.mode})`);
    for (const s of rule.segments) {
      console.log(
        `    bracket#${s.bracketIndex} portion=${fmt(s.inputPortion)} rate=${s.rate.toString()}` +
        ` offset=${fmt(s.offset)} raw=${fmt(s.raw)} rounded=${fmt(s.rounded)}`,
      );
    }
    console.log(
      `    subtotal=${fmt(rule.subtotalAfterRounding)} (raw ${fmt(rule.subtotalBeforeRounding)})` +
      ` x discount ${rule.discount.toString()} = ${fmt(rule.afterDiscount)}` +
      ` -> [min ${rule.min ?? '-inf'}, max ${rule.max ?? '+inf'}] => ${fmt(rule.contribution)}` +
      (rule.clamped ? ' (clamped)' : ''),
    );
  }
  console.log(
    `  total before final rounding: ${fmt(ev.totalBeforeFinalRounding)}` +
    `  =>  floor to 1 =>  ${fmt(ev.total)}`,
  );
}

console.log('=== per-segment contribution evidence ===');
for (const x of ['-50', '0', '75', '100', '250', '1000']) showEvaluation(x);

console.log('\n=== progressive vs flat across a tier (x = 150) ===');
const progOnly = compileLadder({ rules: [{ ...tiered, rounding: undefined }] });
const flatOnly = compileLadder({
  rules: [{ name: 'flat', mode: 'flat', brackets: tiered.brackets }],
});
console.log(`  progressive: ${fmt(evaluate(progOnly, r('150')).total)}`);
console.log(`  flat:        ${fmt(evaluate(flatOnly, r('150')).total)}`);

console.log('\n=== rounding anchor matters (same rates, different anchor) ===');
const mk = (at: 'segment' | 'rule-total') =>
  compileLadder({
    rules: [{
      name: at,
      mode: 'progressive',
      brackets: [
        { lower: r('0'), lowerClosed: true, upper: r('6'), upperClosed: true, rate: r('0.15') },
        { lower: r('6'), lowerClosed: false, upper: null, upperClosed: false, rate: r('0.15') },
      ],
      rounding: { quantum: r('1'), mode: 'floor', at },
    }],
  });
console.log(`  per-segment floor at x=12: ${fmt(evaluate(mk('segment'), r('12')).total)}`);
console.log(`  rule-total  floor at x=12: ${fmt(evaluate(mk('rule-total'), r('12')).total)}`);

console.log('\n=== inversion: all x with composed output = 12 ===');
for (const iv of invert(ladder, r('12'), { from: r('-100'), to: r('1000') })) {
  console.log(`  ${fmtIv(iv)}`);
}
console.log('=== inversion: output = 4 (clamp plateau of the fee rule) ===');
for (const iv of invert(ladder, r('4'), { from: r('-100'), to: r('1000') })) {
  console.log(`  ${fmtIv(iv)}`);
}

console.log('\n=== rational grid validation (step 2.5, includes negatives) ===');
const report = validateOnGrid(ladder, { from: r('-20'), to: r('120'), step: r('2.5') });
console.log(`  points checked: ${report.pointsChecked}`);
console.log(`  inverse mismatches: ${report.inverseMismatches.length}`);
for (const c of report.breakpointChecks) console.log(`  [${c.ok ? 'ok' : 'FAIL'}] ${c.rule}: ${c.detail}`);
console.log(`  overall: ${report.ok ? 'ok' : 'FAILED'}`);
