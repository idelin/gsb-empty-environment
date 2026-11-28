import { describe, test } from 'node:test';
import assert from 'node:assert/strict';

import { compressIndexRanges, expandIndexRanges } from './ranges.js';

describe('compressIndexRanges', () => {
  const cases: Array<{ name: string; input: number[]; expected: string }> = [
    { name: 'empty set', input: [], expected: '' },
    { name: 'single index', input: [5], expected: '5' },
    { name: 'two consecutive indexes', input: [3, 4], expected: '3-4' },
    { name: 'mixed runs and singletons', input: [0, 1, 2, 3, 5, 8, 9, 10], expected: '0-3,5,8-10' },
    { name: 'unsorted input is normalized', input: [10, 0, 9, 2, 1, 3, 8, 5], expected: '0-3,5,8-10' },
    { name: 'duplicates are deduplicated', input: [1, 1, 2, 2, 4], expected: '1-2,4' },
    { name: 'fully consecutive run', input: [0, 1, 2, 3, 4], expected: '0-4' },
    { name: 'all isolated indexes', input: [1, 3, 5], expected: '1,3,5' },
  ];

  for (const { name, input, expected } of cases) {
    test(name, () => {
      assert.equal(compressIndexRanges(input), expected);
    });
  }

  test('accepts any iterable, including a Set', () => {
    assert.equal(compressIndexRanges(new Set([2, 3, 7])), '2-3,7');
  });
});

describe('expandIndexRanges', () => {
  test('round-trips canonical strings', () => {
    const indexes = [0, 1, 2, 3, 5, 8, 9, 10];
    const ranges = compressIndexRanges(indexes);
    assert.deepEqual(expandIndexRanges(ranges), indexes);
  });

  test('parses singleton and empty forms', () => {
    assert.deepEqual(expandIndexRanges('7'), [7]);
    assert.deepEqual(expandIndexRanges('   '), []);
  });
});
