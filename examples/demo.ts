import {
  DecimalParser,
  parseDecimal,
  toFixed,
  toScientific,
  normalize,
  compare,
  quantize,
  inRange,
  assertInRange,
  isNegativeZero,
  DecimalParseError,
} from '../src/index.ts';

function show(title: string, fn: () => void) {
  console.log(`\n=== ${title} ===`);
  fn();
}

show('strict parse + metadata', () => {
  for (const s of ['1.2300', '1.23', '12300', '-0.00', '-2.5e+3']) {
    const d = parseDecimal(s);
    console.log(
      `${s.padEnd(8)} coeff=${d.coefficient} exp=${d.exponent} ` +
      `sigDigits=${d.significantDigits} negative=${d.negative} raw=${JSON.stringify(d.raw)}`,
    );
  }
});

show('1.2300 vs 1.23: equal value, different significant digits', () => {
  const a = parseDecimal('1.2300');
  const b = parseDecimal('1.23');
  console.log(`compare(1.2300, 1.23) = ${compare(a, b)} (numerically equal)`);
  console.log(`significantDigits: ${a.significantDigits} vs ${b.significantDigits}`);
});

show('normalized output: fixed and scientific', () => {
  for (const s of ['1.2300', '0.00123', '-2.5e+3', '9e999999']) {
    const d = parseDecimal(s);
    const fixed = s === '9e999999' ? '(budget refused)' : toFixed(d);
    console.log(`${s.padEnd(10)} fixed=${fixed.padEnd(10)} sci=${toScientific(d)}`);
  }
  console.log(`normalize('1.2300', scientific) = ${normalize(parseDecimal('1.2300'), { format: 'scientific' })}`);
});

show('huge exponents: compare without expansion, budget first', () => {
  const a = parseDecimal('1e999999');
  const b = parseDecimal('2e999999');
  console.log(`compare(1e999999, 2e999999) = ${compare(a, b)} (no 10^999999 materialized)`);
  try {
    parseDecimal('1e1000000');
  } catch (err) {
    const e = err as DecimalParseError;
    console.log(`parse('1e1000000') -> ${e.code} at position ${e.position}`);
  }
  try {
    toFixed(a);
  } catch (err) {
    console.log(`toFixed(1e999999) -> ${(err as Error).name}: ${(err as Error).message}`);
  }
});

show('quantize with explicit rounding + evidence', () => {
  for (const [s, mode] of [['1.235', 'halfEven'], ['1.245', 'halfEven'], ['2.71828', 'up'], ['-1.231', 'floor']] as const) {
    const { result, evidence } = quantize(parseDecimal(s), mode === 'up' ? -3 : -2, mode);
    console.log(
      `quantize(${s}, 1e${mode === 'up' ? -3 : -2}, ${mode}) = ${toFixed(result)} ` +
      `evidence=${JSON.stringify(evidence)}`,
    );
  }
  const nz = quantize(parseDecimal('-0.001'), -2, 'down');
  console.log(`quantize(-0.001, 0.01, down) = ${toFixed(nz.result)} isNegativeZero=${isNegativeZero(nz.result)}`);
});

show('interval constraints', () => {
  console.log(`inRange('5', [1,10]) = ${inRange('5', { min: '1', max: '10' })}`);
  console.log(`inRange('1e999999', [0, 2e999999]) = ${inRange('1e999999', { min: '0', max: '2e999999' })}`);
  try {
    assertInRange('11', { min: '1', max: '10' });
  } catch (err) {
    console.log(`assertInRange('11', [1,10]) -> ${(err as Error).message}`);
  }
});

show('incremental chunked parsing == whole-string parsing', () => {
  const p = new DecimalParser();
  for (const chunk of ['-1.', '23', '00e', '+4']) p.write(chunk);
  const d = p.finish();
  console.log(`chunks ['-1.','23','00e','+4'] -> raw=${d.raw} coeff=${d.coefficient} exp=${d.exponent} sig=${d.significantDigits}`);
  const whole = parseDecimal('-1.2300e+4');
  console.log(`deep-equal to one-shot parse: ${JSON.stringify({ ...d, coefficient: String(d.coefficient) }) === JSON.stringify({ ...whole, coefficient: String(whole.coefficient) })}`);
});

show('error positions: illegal exponent, budget, truncation', () => {
  for (const s of ['1,000', '2.5e1.5', '1e+', '1e1000000']) {
    try {
      parseDecimal(s);
    } catch (err) {
      const e = err as DecimalParseError;
      console.log(`parse(${JSON.stringify(s)}) -> ${e.code} at position ${e.position} (state=${e.state})`);
    }
  }
});
