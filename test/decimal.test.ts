import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { Decimal, d } from '../src/decimal.ts';
import { Context, Flag } from '../src/context.ts';
import { Rounding } from '../src/rounding.ts';

describe('exact construction and parsing', () => {
  test('0.1 + 0.2 === 0.3 exactly', () => {
    const sum = d('0.1').add('0.2');
    assert.equal(sum.toString(), '0.3');
    assert.ok(sum.eq('0.3'));
  });

  test('parses plain, fractional, and scientific literals', () => {
    assert.equal(d('1.23e4').toString(), '12300');
    assert.equal(d('1.23e-4').toString(), '0.000123');
    assert.equal(d('.5').toString(), '0.5');
    assert.equal(d('-0.007').toString(), '-0.007');
  });

  test('rejects invalid literals', () => {
    assert.throws(() => d('abc'), TypeError);
    assert.throws(() => d('1.2.3'), TypeError);
    assert.throws(() => d('1e10000000'), RangeError);
  });

  test('bigint input keeps full precision, no Number detour', () => {
    const big = 123456789012345678901234567890n;
    assert.equal(d(big).toString(), '123456789012345678901234567890');
  });
});

describe('big-number arithmetic beyond Number.MAX_SAFE_INTEGER', () => {
  test('integer addition past 2^53 stays exact', () => {
    assert.equal(d('9007199254740993').add('1').toString(), '9007199254740994');
  });

  test('huge multiplication is exact', () => {
    const ctx = new Context({ precision: 50 });
    const product = d('123456789123456789').mul('987654321987654321', ctx);
    assert.equal(product.toString(), '121932631356500531347203169112635269');
    assert.equal(ctx.flags.size, 0);
  });

  test('2^64 exact via repeated multiply', () => {
    const ctx = new Context({ precision: 50 });
    let v = d(1n);
    for (let i = 0; i < 64; i++) v = v.mul(2n, ctx);
    assert.equal(v.toString(), '18446744073709551616');
    assert.equal(ctx.flags.size, 0);
  });

  test('subtraction signs', () => {
    assert.equal(d('5').sub('7').toString(), '-2');
    assert.equal(d('-5').add('5').toString(), '0');
    assert.equal(d('-2.5').mul('4').toString(), '-10.0');
  });
});

describe('rounding modes: symmetric midpoints per mode', () => {
  const ctxOf = (rounding: (typeof Rounding)[keyof typeof Rounding]) =>
    new Context({ precision: 50, rounding });
  const q = (s: string, mode: keyof typeof Rounding, exp = 0) =>
    d(s).quantize(exp, ctxOf(Rounding[mode])).toString();

  const cases: Array<[string, keyof typeof Rounding, string]> = [
    // HALF_EVEN: ties to even, symmetric around zero
    ['2.5', 'HALF_EVEN', '2'],
    ['-2.5', 'HALF_EVEN', '-2'],
    ['3.5', 'HALF_EVEN', '4'],
    ['-3.5', 'HALF_EVEN', '-4'],
    ['2.5001', 'HALF_EVEN', '3'],
    ['-2.5001', 'HALF_EVEN', '-3'],
    // HALF_UP: ties away from zero, symmetric
    ['2.5', 'HALF_UP', '3'],
    ['-2.5', 'HALF_UP', '-3'],
    ['2.4', 'HALF_UP', '2'],
    ['-2.4', 'HALF_UP', '-2'],
    // HALF_DOWN: ties toward zero, symmetric
    ['2.5', 'HALF_DOWN', '2'],
    ['-2.5', 'HALF_DOWN', '-2'],
    ['2.51', 'HALF_DOWN', '3'],
    ['-2.51', 'HALF_DOWN', '-3'],
    // DOWN: truncate toward zero
    ['2.9', 'DOWN', '2'],
    ['-2.9', 'DOWN', '-2'],
    // UP: away from zero
    ['2.1', 'UP', '3'],
    ['-2.1', 'UP', '-3'],
    // CEILING: toward +inf (intentionally asymmetric)
    ['2.1', 'CEILING', '3'],
    ['-2.1', 'CEILING', '-2'],
    ['-2.9', 'CEILING', '-2'],
    // FLOOR: toward -inf (intentionally asymmetric)
    ['2.9', 'FLOOR', '2'],
    ['-2.1', 'FLOOR', '-3'],
    ['2.1', 'FLOOR', '2'],
  ];

  for (const [input, mode, expected] of cases) {
    test(`${input} ${mode} -> ${expected}`, () => {
      assert.equal(q(input, mode), expected);
    });
  }

  test('05UP rounds away only when kept digit is 0 or 5', () => {
    const ctx = new Context({ precision: 50, rounding: Rounding.ROUND_05UP });
    assert.equal(d('1.05').quantize(-1, ctx).toString(), '1.1');
    assert.equal(d('1.15').quantize(-1, ctx).toString(), '1.1');
    assert.equal(d('1.55').quantize(-1, ctx).toString(), '1.6');
    assert.equal(d('-1.05').quantize(-1, ctx).toString(), '-1.1');
    assert.equal(d('-1.15').quantize(-1, ctx).toString(), '-1.1');
  });
});

describe('context precision is independent per context', () => {
  test('same division, different precisions, independent flags', () => {
    const coarse = new Context({ precision: 5 });
    const fine = new Context({ precision: 30 });
    assert.equal(d('1').div('3', coarse).toString(), '0.33333');
    assert.equal(
      d('1').div('3', fine).toString(),
      '0.333333333333333333333333333333',
    );
    assert.ok(coarse.hasFlag(Flag.Inexact));
    assert.ok(fine.hasFlag(Flag.Inexact));
    coarse.clearFlags();
    assert.equal(coarse.flags.size, 0);
    assert.ok(fine.hasFlag(Flag.Inexact), 'other context flags untouched');
  });

  test('precision applies to add/multiply results', () => {
    const ctx = new Context({ precision: 3 });
    assert.equal(d('12345').add('0', ctx).toString(), '12300');
    assert.ok(ctx.hasFlag(Flag.Inexact));
    assert.ok(ctx.hasFlag(Flag.Rounded));
  });

  test('carry past precision is renormalized (999.9 -> 1000)', () => {
    const ctx = new Context({ precision: 3, rounding: Rounding.HALF_UP });
    assert.equal(d('999.9').add('0', ctx).toString(), '1000');
  });
});

describe('error flags: division by zero vs insufficient precision', () => {
  test('x / 0 sets DivisionByZero and yields signed Infinity', () => {
    const ctx = new Context();
    assert.equal(d('1').div('0', ctx).toString(), 'Infinity');
    assert.equal(d('-2').div('0', ctx).toString(), '-Infinity');
    assert.ok(ctx.hasFlag(Flag.DivisionByZero));
    assert.ok(!ctx.hasFlag(Flag.Inexact));
  });

  test('0 / 0 is InvalidOperation NaN, not DivisionByZero', () => {
    const ctx = new Context();
    assert.equal(d('0').div('0', ctx).toString(), 'NaN');
    assert.ok(ctx.hasFlag(Flag.InvalidOperation));
    assert.ok(!ctx.hasFlag(Flag.DivisionByZero));
  });

  test('inexact division sets Inexact, not DivisionByZero', () => {
    const ctx = new Context({ precision: 10 });
    assert.equal(d('1').div('3', ctx).toString(), '0.3333333333');
    assert.ok(ctx.hasFlag(Flag.Inexact));
    assert.ok(ctx.hasFlag(Flag.Rounded));
    assert.ok(!ctx.hasFlag(Flag.DivisionByZero));
  });

  test('exact division sets no flags', () => {
    const ctx = new Context();
    assert.equal(d('1').div('4', ctx).toString(), '0.25');
    assert.equal(d('22').div('7', new Context({ precision: 6 })).toString(), '3.14286');
    assert.equal(ctx.flags.size, 0);
  });

  test('flags persist until explicitly cleared', () => {
    const ctx = new Context({ precision: 4 });
    d('1').div('3', ctx);
    d('1').div('0', ctx);
    assert.ok(ctx.hasFlag(Flag.Inexact));
    assert.ok(ctx.hasFlag(Flag.DivisionByZero));
    ctx.clearFlags();
    assert.equal(ctx.flags.size, 0);
  });

  test('quantize beyond context precision is InvalidOperation', () => {
    const ctx = new Context({ precision: 2 });
    assert.equal(d('123').quantize(-1, ctx).toString(), 'NaN');
    assert.ok(ctx.hasFlag(Flag.InvalidOperation));
  });
});

describe('quantize', () => {
  test('rounds to cents with explicit mode', () => {
    const ctx = new Context({ precision: 28, rounding: Rounding.HALF_UP });
    assert.equal(d('3.14159').quantize(-2, ctx).toString(), '3.14');
    assert.equal(d('2.005').quantize(-2, ctx).toString(), '2.01');
    assert.equal(d('-2.005').quantize(-2, ctx).toString(), '-2.01');
  });

  test('widening exponent is exact and flag-free', () => {
    const ctx = new Context({ precision: 28 });
    assert.equal(d('1.5').quantize(-4, ctx).toString(), '1.5000');
    assert.equal(ctx.flags.size, 0);
  });
});

describe('comparison', () => {
  test('eq and compare across scales', () => {
    assert.ok(d('1.0').eq('1'));
    assert.ok(d('0.3').eq(d('0.1').add('0.2')));
    assert.equal(d('-1').compare('1'), -1);
    assert.equal(d('2').compare('1.999'), 1);
    assert.equal(d('5').compare('5.00'), 0);
  });
});

describe('special values propagate', () => {
  test('infinity arithmetic', () => {
    const ctx = new Context();
    const inf = d('Infinity');
    assert.equal(inf.add('1').toString(), 'Infinity');
    assert.equal(inf.add(inf.negate(), ctx).toString(), 'NaN');
    assert.ok(ctx.hasFlag(Flag.InvalidOperation));
    assert.equal(inf.mul('-2').toString(), '-Infinity');
    assert.equal(d('1').div(inf).toString(), '0');
  });

  test('NaN is sticky and never equal', () => {
    assert.equal(d('NaN').add('1').toString(), 'NaN');
    assert.ok(!d('NaN').eq('NaN'));
  });
});
