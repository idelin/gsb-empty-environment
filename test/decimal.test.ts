import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  Decimal,
  Context,
  Flag,
  RoundingMode,
  DivisionByZeroError,
  InvalidOperationError,
} from '../src/index.ts';

const d = (s: string) => Decimal.of(s);

describe('exact addition/subtraction (no Number intermediate)', () => {
  test('0.1 + 0.2 === 0.3 exactly', () => {
    const sum = d('0.1').add(d('0.2'));
    assert.equal(sum.toString(), '0.3');
    assert.ok(sum.equals(d('0.3')));
  });

  test('0.3 - 0.1 === 0.2 exactly', () => {
    assert.equal(d('0.3').subtract(d('0.1')).toString(), '0.2');
  });

  test('integers far beyond 2^53 stay exact', () => {
    const big = d('900719925474099312345678901234567890');
    assert.equal(big.add(1).toString(), '900719925474099312345678901234567891');
    assert.equal(big.subtract(big).toString(), '0');
    assert.equal(
      d('12345678901234567890').multiply(d('98765432109876543210')).toString(),
      '1219326311370217952237463801111263526900',
    );
  });

  test('negative results and zero normalization', () => {
    assert.equal(d('1').subtract(d('2')).toString(), '-1');
    assert.equal(d('-0.00').sign, 1);
    assert.equal(d('5').add(d('-5')).toString(), '0');
  });
});

describe('exact multiplication', () => {
  test('1.1 * 1.1 = 1.21', () => {
    assert.equal(d('1.1').multiply(d('1.1')).toString(), '1.21');
  });

  test('signs multiply correctly', () => {
    assert.equal(d('-2.5').multiply(d('4')).toString(), '-10.0');
    assert.equal(d('-2.5').multiply(d('-4')).toString(), '10.0');
  });
});

describe('division: exact vs inexact vs division by zero', () => {
  test('terminating division is exact and sets no flags', () => {
    const ctx = new Context({ precision: 10 });
    assert.equal(d('1').divide(d('2'), ctx).toString(), '0.5');
    assert.equal(d('10').divide(d('4'), ctx).toString(), '2.5');
    assert.equal(ctx.flags.size, 0);
  });

  test('non-terminating division rounds to context precision and flags INEXACT', () => {
    const ctx = new Context({ precision: 10 });
    const third = d('1').divide(d('3'), ctx);
    assert.equal(third.toString(), '0.3333333333');
    assert.ok(ctx.hasFlag(Flag.INEXACT));
    assert.ok(ctx.hasFlag(Flag.ROUNDED));
    assert.ok(!ctx.hasFlag(Flag.DIVISION_BY_ZERO));
  });

  test('division by zero throws and flags DIVISION_BY_ZERO, never INEXACT', () => {
    const ctx = new Context();
    assert.throws(() => d('1').divide(d('0'), ctx), DivisionByZeroError);
    assert.ok(ctx.hasFlag(Flag.DIVISION_BY_ZERO));
    assert.ok(!ctx.hasFlag(Flag.INEXACT));
    assert.ok(!ctx.hasFlag(Flag.ROUNDED));
  });

  test('0 / x is exact zero, no flags', () => {
    const ctx = new Context();
    assert.equal(d('0').divide(d('7'), ctx).toString(), '0');
    assert.equal(ctx.flags.size, 0);
  });

  test('division respects rounding mode', () => {
    const down = new Context({ precision: 5, rounding: RoundingMode.DOWN });
    const up = new Context({ precision: 5, rounding: RoundingMode.UP });
    assert.equal(d('1').divide(d('3'), down).toString(), '0.33333');
    assert.equal(d('1').divide(d('3'), up).toString(), '0.33334');
  });
});

describe('context precision is independent per context', () => {
  test('two contexts yield different precisions and separate flags', () => {
    const coarse = new Context({ precision: 3 });
    const fine = new Context({ precision: 12 });
    assert.equal(d('1').divide(d('7'), coarse).toString(), '0.143');
    assert.equal(d('1').divide(d('7'), fine).toString(), '0.142857142857');
    assert.ok(coarse.hasFlag(Flag.INEXACT));
    assert.ok(fine.hasFlag(Flag.INEXACT));
    // Mutating one context does not touch the other.
    coarse.clearFlags();
    assert.equal(coarse.flags.size, 0);
    assert.ok(fine.hasFlag(Flag.INEXACT));
  });

  test('addition under a context rounds to significant digits', () => {
    const ctx = new Context({ precision: 4 });
    const sum = d('1.23456789').add(d('0'), ctx);
    assert.equal(sum.toString(), '1.235');
    assert.ok(ctx.hasFlag(Flag.INEXACT));
  });

  test('rounding carry across precision boundary (9.99 -> 10)', () => {
    const ctx = new Context({ precision: 2, rounding: RoundingMode.HALF_UP });
    assert.equal(d('9.99').add(d('0'), ctx).toString(), '10');
  });

  test('flags are sticky until clearFlags()', () => {
    const ctx = new Context({ precision: 3 });
    d('1').divide(d('3'), ctx);
    assert.ok(ctx.hasFlag(Flag.INEXACT));
    d('1').divide(d('2'), ctx); // exact op does not clear old flags
    assert.ok(ctx.hasFlag(Flag.INEXACT));
    ctx.clearFlags();
    assert.equal(ctx.flags.size, 0);
  });
});

describe('quantize with explicit rounding modes, symmetric at midpoints', () => {
  const q = (s: string, mode: RoundingMode) => d(s).quantize(0, mode).toString();

  test('HALF_EVEN: ties to even, symmetric', () => {
    assert.equal(q('2.5', 'HALF_EVEN'), '2');
    assert.equal(q('-2.5', 'HALF_EVEN'), '-2');
    assert.equal(q('3.5', 'HALF_EVEN'), '4');
    assert.equal(q('-3.5', 'HALF_EVEN'), '-4');
  });

  test('HALF_UP: ties away from zero, symmetric', () => {
    assert.equal(q('2.5', 'HALF_UP'), '3');
    assert.equal(q('-2.5', 'HALF_UP'), '-3');
    assert.equal(q('2.4', 'HALF_UP'), '2');
    assert.equal(q('-2.4', 'HALF_UP'), '-2');
  });

  test('HALF_DOWN: ties toward zero, symmetric', () => {
    assert.equal(q('2.5', 'HALF_DOWN'), '2');
    assert.equal(q('-2.5', 'HALF_DOWN'), '-2');
    assert.equal(q('2.51', 'HALF_DOWN'), '3');
    assert.equal(q('-2.51', 'HALF_DOWN'), '-3');
  });

  test('UP / DOWN: away from / toward zero, symmetric', () => {
    assert.equal(q('2.1', 'UP'), '3');
    assert.equal(q('-2.1', 'UP'), '-3');
    assert.equal(q('2.9', 'DOWN'), '2');
    assert.equal(q('-2.9', 'DOWN'), '-2');
  });

  test('CEILING / FLOOR: directed modes', () => {
    assert.equal(q('2.1', 'CEILING'), '3');
    assert.equal(q('-2.1', 'CEILING'), '-2');
    assert.equal(q('2.1', 'FLOOR'), '2');
    assert.equal(q('-2.1', 'FLOOR'), '-3');
  });

  test('quantize to cents and rescaling up', () => {
    assert.equal(d('3.14159').quantize(-2, 'DOWN').toString(), '3.14');
    assert.equal(d('3.145').quantize(-2, 'HALF_UP').toString(), '3.15');
    assert.equal(d('-3.145').quantize(-2, 'HALF_UP').toString(), '-3.15');
    assert.equal(d('3.1').quantize(-3, 'DOWN').toString(), '3.100');
  });

  test('quantize records flags on a context', () => {
    const ctx = new Context();
    d('1.234').quantize(-2, 'DOWN', ctx);
    assert.ok(ctx.hasFlag(Flag.ROUNDED));
    assert.ok(ctx.hasFlag(Flag.INEXACT));
    ctx.clearFlags();
    d('1.2').quantize(-2, 'DOWN', ctx); // exact rescale, no discarded digits
    assert.equal(ctx.flags.size, 0);
  });

  test('quantize without mode or context is an error', () => {
    assert.throws(() => d('1.5').quantize(0), InvalidOperationError);
  });
});

describe('parsing and formatting', () => {
  test('string forms round-trip', () => {
    assert.equal(d('0.001').toString(), '0.001');
    assert.equal(d('-12.340').toString(), '-12.340');
    assert.equal(d('1.5e3').toString(), '1500');
    assert.equal(d('1.5e-3').toString(), '0.0015');
    assert.equal(d('.5').toString(), '0.5');
    assert.equal(d('+7').toString(), '7');
  });

  test('bigint input is exact', () => {
    assert.equal(Decimal.of(123456789012345678901234567890n).toString(), '123456789012345678901234567890');
  });

  test('scientific notation output', () => {
    assert.equal(d('-123.45').toExponential(), '-1.2345e+2');
    assert.equal(d('0.001').toExponential(), '1e-3');
  });

  test('invalid literals rejected', () => {
    assert.throws(() => d('abc'), InvalidOperationError);
    assert.throws(() => d(''), InvalidOperationError);
    assert.throws(() => d('1.2.3'), InvalidOperationError);
  });

  test('comparison', () => {
    assert.ok(d('1.0').equals(d('1')));
    assert.ok(d('2').compare(d('10')) < 0);
    assert.ok(d('-5').compare(d('3')) < 0);
    assert.ok(d('0.1').compare(d('0.10')) === 0);
  });
});
