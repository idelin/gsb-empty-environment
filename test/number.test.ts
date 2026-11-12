import assert from 'node:assert/strict';
import { test } from 'node:test';
import type { NumberFormatRule } from '../src/types.ts';
import { normalizeNumberFormat, parsePriceText, scanAmountText } from '../src/number.ts';

function scan(text: string, rule: NumberFormatRule) {
  const { format } = normalizeNumberFormat(rule);
  return scanAmountText(text, format);
}

test('dot-decimal with comma grouping', () => {
  const result = scan('$1,234.50', { decimal: '.', grouping: ',' });
  assert.equal(result.amounts.length, 1);
  assert.equal(result.amounts[0].value, 1234.5);
  assert.equal(result.amounts[0].start, 1);
  assert.equal(result.amounts[0].end, 9);
  assert.equal(result.amounts[0].usedDecimal, true);
  assert.equal(result.amounts[0].usedGrouping, true);
});

test('comma-decimal with dot grouping', () => {
  const result = scan('€1.234,56', { decimal: ',', grouping: '.' });
  assert.equal(result.amounts.length, 1);
  assert.equal(result.amounts[0].value, 1234.56);
  assert.equal(result.amounts[0].start, 1);
  assert.equal(result.amounts[0].end, 9);
});

test('space grouping is accepted only when configured', () => {
  const result = scan('1 234,56', { decimal: ',', grouping: ' ' });
  assert.equal(result.amounts.length, 1);
  assert.equal(result.amounts[0].value, 1234.56);

  const unconfigured = scan('1 234,56', { decimal: ',', grouping: null });
  // A space that is not declared as grouping is a token boundary.  The parser
  // returns both readings as candidates instead of silently merging them.
  assert.equal(unconfigured.amounts.length, 2);
  assert.equal(unconfigured.amounts[0].value, 1);
  assert.equal(unconfigured.amounts[1].value, 234.56);
});

test('an extra or malformed separator is never guessed away', () => {
  const wrongGroup = scan('1,23', { decimal: '.', grouping: ',' });
  assert.equal(wrongGroup.amounts.length, 0);
  assert.equal(wrongGroup.invalid[0]?.issue.code, 'invalid-group-size');

  const twoDecimals = scan('1.234.567', { decimal: '.', grouping: ',' });
  assert.equal(twoDecimals.amounts.length, 0);
  assert.equal(twoDecimals.invalid[0]?.issue.code, 'multiple-decimal-separators');

  const noGroupingConfig = scan('1,234', { decimal: '.', grouping: null });
  assert.equal(noGroupingConfig.amounts.length, 0);
  assert.equal(noGroupingConfig.invalid[0]?.issue.code, 'unconfigured-separator');
});

test('multiple amounts in one text are returned as candidates without choosing', () => {
  const parsed = parsePriceText('$10.00 / $20.00', { decimal: '.', grouping: ',' });
  assert.equal(parsed.amounts.length, 2);
  assert.equal(parsed.ambiguous, true);
  assert.deepEqual(parsed.amounts.map((amount) => amount.value), [10, 20]);
});

test('negative amounts are not accepted by default', () => {
  const result = scan('-1.00', { decimal: '.', grouping: ',' });
  assert.equal(result.amounts.length, 0);
  assert.equal(result.invalid[0]?.issue.code, 'negative-amount');
});

test('a leading decimal separator is parsed against the configured decimal point', () => {
  const result = scan('.99', { decimal: '.', grouping: ',' });
  assert.equal(result.amounts.length, 1);
  assert.equal(result.amounts[0].value, 0.99);
  assert.equal(result.amounts[0].start, 0);
  assert.equal(result.amounts[0].end, 3);
});

test('missing decimal config is reported instead of inferred from text', () => {
  const { format, issues } = normalizeNumberFormat({ grouping: ',' });
  assert.equal(format.decimal, null);
  assert.equal(issues.some((issue) => issue.code === 'config-missing-decimal-separator'), true);
  const result = scanAmountText('1,234.56', format);
  assert.equal(result.amounts.length, 0);
});
