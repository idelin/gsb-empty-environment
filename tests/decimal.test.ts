import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  parseDecimal,
  canonicalDecimal,
  serializeDecimal,
  compareDecimal,
  decimalEquals,
  decimalIdentical,
  isIntegerDecimal,
  multipleOfDecimal,
  DecimalError,
  DEFAULT_MAX_ABS_EXPONENT,
} from '../src/decimal.ts';

const p = (s: string) => parseDecimal(s);

test('exhaustive short lexemes: validity matches JSON.parse, as-is round-trips, canonical idempotent', () => {
  const alphabet = ['-', '+', '0', '1', '9', '.', 'e', 'E'];
  let accepted = 0;
  let rejected = 0;
  const check = (s: string): void => {
    let oracle = true;
    try { JSON.parse(s); } catch { oracle = false; }
    let ours = null;
    try { ours = parseDecimal(s); } catch (e) { assert.ok(e instanceof DecimalError, s); }
    assert.equal(ours !== null, oracle, `validity mismatch for ${JSON.stringify(s)}`);
    if (ours !== null) {
      accepted++;
      assert.equal(serializeDecimal(ours, 'asis'), s, `as-is must replay lexeme ${s}`);
      const c1 = canonicalDecimal(ours);
      const c2 = canonicalDecimal(parseDecimal(c1));
      assert.equal(c2, c1, `canonical form must be a fixed point for ${s}`);
      assert.ok(decimalEquals(ours, parseDecimal(c1)), `canonical form must preserve value for ${s}`);
    } else {
      rejected++;
    }
  };
  const walk = (prefix: string, depth: number): void => {
    if (depth === 0) { check(prefix); return; }
    for (const c of alphabet) walk(prefix + c, depth - 1);
  };
  for (let len = 1; len <= 4; len++) walk('', len);
  assert.equal(accepted + rejected, 8 + 64 + 512 + 4096);
  assert.ok(accepted > 100 && rejected > 1000, `accepted=${accepted} rejected=${rejected}`);
});

test('rejects illegal leading zeros and non-finite literals', () => {
  for (const bad of ['01', '-01', '00', '00.5', '007', '+1', '.5', '-.5', '1.', '1e', '1e+', '1e-', '-', '', 'NaN', 'Infinity', '-Infinity', '0x10', '1_000', '1,5']) {
    assert.throws(() => parseDecimal(bad), DecimalError, bad);
  }
  assert.equal(canonicalDecimal(p('0.5')), '0.5');
  assert.equal(canonicalDecimal(p('0')), '0');
});

test('integers beyond 53 bits keep full precision', () => {
  const a = p('9007199254740993'); // 2^53 + 1, not representable as Number
  const b = p('9007199254740992');
  assert.ok(compareDecimal(a, b) > 0, 'Number() would tie these');
  assert.equal(canonicalDecimal(a), '9007199254740993');
  const big = p('123456789012345678901234567890123456789');
  // 39 digits: canonical switches to scientific above 1e21, digits stay exact
  assert.equal(canonicalDecimal(big), '1.23456789012345678901234567890123456789e+38');
  assert.equal(big.digits, '123456789012345678901234567890123456789');
  assert.ok(isIntegerDecimal(big));
  assert.ok(!isIntegerDecimal(p('1.5')));
  assert.ok(isIntegerDecimal(p('1e2')));
  assert.ok(!isIntegerDecimal(p('1e-2')));
});

test('negative zero policy: value-equal to 0, canonical collapses, as-is preserved', () => {
  const nz = p('-0');
  const z = p('0');
  assert.ok(decimalEquals(nz, z));
  assert.ok(!decimalIdentical(nz, z));
  assert.equal(nz.neg, true);
  assert.equal(canonicalDecimal(nz), '0');
  assert.equal(serializeDecimal(nz, 'asis'), '-0');
  assert.equal(serializeDecimal(p('-0.0e5'), 'asis'), '-0.0e5');
  assert.equal(canonicalDecimal(p('-0.0e5')), '0');
  assert.ok(compareDecimal(p('-0'), p('0.0')) === 0);
});

test('1e+02 canonicalizes to an equivalent value; re-canonicalization is a fixed point', () => {
  const sci = p('1e+02');
  const plain = p('100');
  assert.ok(decimalEquals(sci, plain));
  assert.ok(!decimalIdentical(sci, plain), 'same value, different lexeme must stay distinguishable');
  assert.equal(canonicalDecimal(sci), '100');
  assert.equal(canonicalDecimal(parseDecimal(canonicalDecimal(sci))), '100');
  assert.equal(serializeDecimal(sci, 'asis'), '1e+02');
  assert.equal(sci.raw, '1e+02');
  assert.equal(plain.raw, '100');
});

test('comparison uses normalized form and never expands huge exponents', () => {
  assert.ok(compareDecimal(p('1e100000'), p('2e100000')) < 0);
  assert.ok(compareDecimal(p('2e100000'), p('1e100001')) < 0);
  assert.ok(compareDecimal(p('9.99e100000'), p('1e100001')) < 0);
  assert.ok(compareDecimal(p('-2e100000'), p('-1e100000')) < 0);
  assert.ok(compareDecimal(p('1e-100000'), p('2e-100000')) < 0);
  assert.ok(compareDecimal(p('-1e100000'), p('1e-100000')) < 0);
  const c = canonicalDecimal(p('1e100000'));
  assert.equal(c, '1e+100000');
  assert.ok(c.length < 20, 'canonical form stays compact: exponent not expanded');
  assert.ok(decimalEquals(p('1e100000'), p('10e99999')));
  assert.ok(decimalEquals(p('1.0'), p('1')));
  assert.ok(decimalEquals(p('10e-1'), p('1.00')));
  assert.ok(decimalEquals(p('0.1'), p('1e-1')));
  assert.ok(compareDecimal(p('0.5'), p('0.51')) < 0);
  assert.ok(compareDecimal(p('0.49'), p('0.5')) < 0);
});

test('exponent and precision budgets enforced at exact boundaries', () => {
  // adjusted exponent of "1e999999" is 1_000_000 == default budget: accepted
  assert.equal(p('1e999999').exp, DEFAULT_MAX_ABS_EXPONENT);
  assert.throws(() => p('1e1000000'), DecimalError); // adjusted 1_000_001
  assert.throws(() => p('1e999999999999999999999999'), DecimalError, 'exponent literal beyond Number range');
  assert.equal(p('0e999999999999999999999999').digits, '0', 'zero ignores its exponent');
  // budget applies to the *adjusted* exponent, not the literal
  assert.equal(p('0.0001e1000002').exp, 999999);
  assert.throws(() => p('0.0001e1000004'), DecimalError);
  // custom budgets
  assert.throws(() => parseDecimal('12345', { maxSignificantDigits: 4 }), DecimalError);
  assert.equal(parseDecimal('12345', { maxSignificantDigits: 5 }).digits, '12345');
  assert.throws(() => parseDecimal('1e10', { maxAbsExponent: 5 }), DecimalError);
  assert.equal(parseDecimal('1e10', { maxAbsExponent: 11 }).exp, 11);
});

test('exact multipleOf on decimals (cases floats get wrong)', () => {
  assert.ok(multipleOfDecimal(p('0.3'), p('0.1')));
  assert.ok(!multipleOfDecimal(p('0.3'), p('0.2')));
  assert.ok(multipleOfDecimal(p('9007199254740993'), p('1')));
  assert.ok(multipleOfDecimal(p('-4.5'), p('1.5')));
  assert.ok(multipleOfDecimal(p('0'), p('0.1')));
  assert.ok(multipleOfDecimal(p('1e100000'), p('1e99999')));
  assert.throws(() => multipleOfDecimal(p('1'), p('0')), DecimalError);
});
