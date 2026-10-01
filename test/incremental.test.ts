import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DecimalParser, parseDecimal, DecimalParseError, type Decimal } from '../src/index.ts';

const SAMPLES = [
  '0',
  '-0',
  '-0.00',
  '1.2300',
  '12300',
  '.5',
  '+1.5',
  '-2.5e+3',
  '1E-10',
  '9e999999',
  '0.0012300e-4',
  '98765432109876543210987654321.0001',
];

function fields(d: Decimal) {
  return {
    negative: d.negative,
    coefficient: d.coefficient,
    exponent: d.exponent,
    significantDigits: d.significantDigits,
    raw: d.raw,
  };
}

function parseChunked(input: string, splits: number[]): Decimal {
  const p = new DecimalParser();
  let prev = 0;
  for (const i of splits) {
    p.write(input.slice(prev, i));
    prev = i;
  }
  p.write(input.slice(prev));
  return p.finish();
}

test('arbitrary chunking matches whole-string parsing', () => {
  for (const s of SAMPLES) {
    const whole = fields(parseDecimal(s));
    // every single split point
    for (let i = 0; i <= s.length; i++) {
      assert.deepEqual(fields(parseChunked(s, [i])), whole, `${s} split at ${i}`);
    }
    // every pair of split points (sampled for long strings)
    for (let i = 0; i <= s.length; i++) {
      for (let j = i; j <= s.length; j++) {
        if (s.length > 12 && (i * 31 + j) % 7 !== 0) continue;
        assert.deepEqual(fields(parseChunked(s, [i, j])), whole, `${s} splits at ${i},${j}`);
      }
    }
    // character-by-character
    assert.deepEqual(
      fields(parseChunked(s, Array.from({ length: s.length }, (_, i) => i))),
      whole,
      `${s} char-by-char`,
    );
  }
});

test('raw lexeme is preserved across chunks', () => {
  const p = new DecimalParser();
  p.write('-1.').write('23').write('00e').write('+4');
  const d = p.finish();
  assert.equal(d.raw, '-1.2300e+4');
  assert.equal(d.coefficient, 12300n);
  assert.equal(d.exponent, 0);
  assert.equal(d.significantDigits, 5);
});

test('error positions are absolute across chunk boundaries', () => {
  const p = new DecimalParser();
  p.write('12');
  assert.throws(
    () => p.write(',3'),
    (err: unknown) => {
      const e = err as DecimalParseError;
      assert.equal(e.code, 'INVALID_CHARACTER');
      assert.equal(e.position, 2);
      return true;
    },
  );
});

test('finish truncation positions are independent of chunking', () => {
  for (const chunks of [['1e'], ['1', 'e'], ['1e', '']]) {
    const p = new DecimalParser();
    for (const c of chunks) p.write(c);
    assert.throws(
      () => p.finish(),
      (err: unknown) => {
        const e = err as DecimalParseError;
        assert.equal(e.code, 'TRUNCATED_INPUT');
        assert.equal(e.position, 2);
        assert.equal(e.state, 'eMark');
        return true;
      },
    );
  }

  const p2 = new DecimalParser();
  p2.write('2.5e+');
  assert.throws(
    () => p2.finish(),
    (err: unknown) => {
      const e = err as DecimalParseError;
      assert.equal(e.code, 'TRUNCATED_INPUT');
      assert.equal(e.position, 5);
      assert.equal(e.state, 'eSign');
      return true;
    },
  );

  const p3 = new DecimalParser();
  p3.write('1.');
  assert.throws(
    () => p3.finish(),
    (err: unknown) => (err as DecimalParseError).code === 'TRUNCATED_INPUT'
      && (err as DecimalParseError).position === 2,
  );
});

test('budget exhaustion position is exact across chunks', () => {
  const p = new DecimalParser({ maxMantissaDigits: 4 });
  p.write('12.3');
  assert.throws(
    () => p.write('45'),
    (err: unknown) => {
      const e = err as DecimalParseError;
      assert.equal(e.code, 'BUDGET_EXCEEDED');
      assert.equal(e.position, 5);
      return true;
    },
  );

  const p2 = new DecimalParser({ maxExponent: 100 });
  p2.write('1e1');
  assert.throws(
    () => p2.write('50'),
    (err: unknown) => {
      const e = err as DecimalParseError;
      assert.equal(e.code, 'EXPONENT_OVERFLOW');
      assert.equal(e.position, 4);
      return true;
    },
  );
});

test('parser closes after error and after finish', () => {
  const p = new DecimalParser();
  assert.throws(() => p.write('1,2'));
  assert.throws(
    () => p.write('3'),
    (err: unknown) => (err as DecimalParseError).code === 'PARSER_CLOSED',
  );

  const p2 = new DecimalParser();
  p2.write('1');
  p2.finish();
  assert.throws(
    () => p2.finish(),
    (err: unknown) => (err as DecimalParseError).code === 'PARSER_CLOSED',
  );
  assert.throws(
    () => p2.write('2'),
    (err: unknown) => (err as DecimalParseError).code === 'PARSER_CLOSED',
  );
});
