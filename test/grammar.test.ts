import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  Decimal,
  compareDecimal,
  isValidJsonNumberLexeme,
  toDecimal,
} from '../src/decimal.ts';

/**
 * Exhaustive sweep of short strings over a number-ish alphabet. The oracle
 * is twofold:
 *   1. the JSON number grammar (independent regex), and
 *   2. JSON.parse itself, which accepts exactly the same number grammar.
 * For accepted strings the parsed decimal must equal the double JSON.parse
 * produced (short strings round-trip exactly through String()).
 */
const GRAMMAR = /^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$/;

function* strings(alphabet: string[], maxLength: number): Generator<string> {
  if (maxLength === 0) return;
  for (const ch of alphabet) yield ch;
  for (const prefix of strings(alphabet, maxLength - 1)) {
    for (const ch of alphabet) yield prefix + ch;
  }
}

function jsonParseAccepts(text: string): boolean {
  try {
    JSON.parse(text);
    return true;
  } catch {
    return false;
  }
}

test('exhaustive short lexemes match grammar and JSON.parse oracles', () => {
  const alphabet = ['0', '1', '9', '.', 'e', 'E', '+', '-'];
  let checked = 0;
  let accepted = 0;
  for (const text of strings(alphabet, 4)) {
    checked++;
    const ours = isValidJsonNumberLexeme(text);
    assert.equal(ours, GRAMMAR.test(text), `grammar oracle for ${JSON.stringify(text)}`);
    assert.equal(ours, jsonParseAccepts(text), `JSON.parse oracle for ${JSON.stringify(text)}`);
    if (ours) {
      accepted++;
      const parsed = Decimal.fromLexeme(text);
      assert.equal(compareDecimal(parsed, toDecimal(JSON.parse(text))), 0,
        `value of ${JSON.stringify(text)} matches JSON.parse`);
    }
  }
  assert.equal(checked, 8 + 64 + 512 + 4096);
  assert.ok(accepted > 0);
});

test('exhaustive length-5 lexemes over a reduced alphabet', () => {
  const alphabet = ['0', '1', '.', 'e', '-'];
  let checked = 0;
  for (const text of strings(alphabet, 5)) {
    if (text.length < 5) continue;
    checked++;
    const ours = isValidJsonNumberLexeme(text);
    assert.equal(ours, GRAMMAR.test(text), `grammar oracle for ${JSON.stringify(text)}`);
    assert.equal(ours, jsonParseAccepts(text), `JSON.parse oracle for ${JSON.stringify(text)}`);
  }
  assert.equal(checked, 5 ** 5);
});

test('exponent boundary sweep around the default budget', () => {
  for (const magnitude of [99998n, 99999n, 100000n]) {
    Decimal.fromLexeme(`1e${magnitude}`);
    Decimal.fromLexeme(`1e-${magnitude}`);
    Decimal.fromLexeme(`9.99e${magnitude}`);
  }
  for (const magnitude of [100001n, 100002n, 1000000n]) {
    assert.throws(() => Decimal.fromLexeme(`1e${magnitude}`), /exceeds precision budget/);
    assert.throws(() => Decimal.fromLexeme(`1e-${magnitude}`), /exceeds precision budget/);
  }
  // Exponent zero padding does not count against the budget.
  assert.equal(compareDecimal(Decimal.fromLexeme('1e00000000000000000000000001'),
    Decimal.fromLexeme('10')), 0);
});

test('exhaustive two-digit exponents with every sign style', () => {
  for (let exp = 0; exp < 100; exp++) {
    const digits = String(exp).padStart(2, '0');
    for (const sign of ['', '+', '-']) {
      const text = `1e${sign}${digits}`;
      assert.equal(isValidJsonNumberLexeme(text), true, text);
      const value = Decimal.fromLexeme(text);
      const expected = sign === '-' ? -BigInt(exp) : BigInt(exp);
      assert.equal(value.exponent, expected, text);
    }
  }
});
