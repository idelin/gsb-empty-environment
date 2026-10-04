import assert from 'node:assert/strict';
import test from 'node:test';

import {
  EditDistance,
  MOVE_ORDER,
  applyPath,
  editDistance,
  enumerateOptimalPaths,
  pathCost,
  unrankPath,
  validateCosts,
  type Operation,
  type OperationCosts,
} from '../src/index.ts';

const UNIT: OperationCosts = { insert: 1, delete: 1, substitute: 1 };

function serialize(path: readonly Operation[]): string {
  return JSON.stringify(path);
}

test('rejects zero-cost and negative-cost operations', () => {
  assert.throws(() => validateCosts({ insert: 0, delete: 1, substitute: 1 }), RangeError);
  assert.throws(() => validateCosts({ insert: 1, delete: 0, substitute: 1 }), RangeError);
  assert.throws(() => validateCosts({ insert: 1, delete: 1, substitute: 0 }), RangeError);
  assert.throws(() => validateCosts({ insert: -1, delete: 1, substitute: 1 }), RangeError);
  assert.throws(() => validateCosts({ insert: 1, delete: 1, substitute: -0.5 }), RangeError);
  assert.throws(() => editDistance('a', 'b', { insert: 0, delete: 1, substitute: 1 }), RangeError);
});

test('rejects non-finite costs', () => {
  assert.throws(() => validateCosts({ insert: NaN, delete: 1, substitute: 1 }), TypeError);
  assert.throws(() => validateCosts({ insert: 1, delete: Infinity, substitute: 1 }), TypeError);
  assert.throws(
    () => validateCosts({ insert: 1, delete: 1, substitute: '1' as unknown as number }),
    TypeError,
  );
});

test('accepts positive (including fractional) costs', () => {
  assert.doesNotThrow(() => validateCosts({ insert: 0.5, delete: 2, substitute: 1.25 }));
});

test('empty strings', () => {
  const both = editDistance('', '', UNIT);
  assert.equal(both.cost, 0);
  assert.equal(both.count, 1n);
  assert.deepEqual(unrankPath('', '', 0n, UNIT), []);

  const toEmpty = editDistance('ab', '', UNIT);
  assert.equal(toEmpty.cost, 2);
  assert.equal(toEmpty.count, 1n);
  assert.deepEqual(unrankPath('ab', '', 0, UNIT), [
    { type: 'delete', char: 'a' },
    { type: 'delete', char: 'b' },
  ]);

  const fromEmpty = editDistance('', 'ab', UNIT);
  assert.equal(fromEmpty.cost, 2);
  assert.equal(fromEmpty.count, 1n);
  assert.deepEqual(unrankPath('', 'ab', 0, UNIT), [
    { type: 'insert', char: 'a' },
    { type: 'insert', char: 'b' },
  ]);
});

test('classic kitten -> sitting', () => {
  const ed = new EditDistance('kitten', 'sitting', UNIT);
  assert.equal(ed.cost, 3);
  const brute = enumerateOptimalPaths('kitten', 'sitting', UNIT);
  assert.equal(ed.count, BigInt(brute.paths.length));
  assert.equal(ed.count, 1n);
});

test('repeated symbols: distinct paths, same final string', () => {
  const ed = new EditDistance('aa', 'a', UNIT);
  assert.equal(ed.cost, 1);
  assert.equal(ed.count, 2n);
  const first = ed.unrank(0n);
  const second = ed.unrank(1n);
  assert.notEqual(serialize(first), serialize(second));
  assert.equal(applyPath('aa', first), 'a');
  assert.equal(applyPath('aa', second), 'a');
  assert.deepEqual(first, [
    { type: 'match', char: 'a' },
    { type: 'delete', char: 'a' },
  ]);
  assert.deepEqual(second, [
    { type: 'delete', char: 'a' },
    { type: 'match', char: 'a' },
  ]);
});

test('zero-cost matches create extra optimal paths', () => {
  const ed = new EditDistance('aab', 'ab', UNIT);
  assert.equal(ed.cost, 1);
  assert.equal(ed.count, 2n);
  for (const k of [0n, 1n]) {
    const path = ed.unrank(k);
    assert.equal(applyPath('aab', path), 'ab');
    assert.ok(path.some((op) => op.type === 'match'));
  }

  // substitute ties with delete+insert when substitute = insert + delete
  const tie = new EditDistance('a', 'b', { insert: 1, delete: 1, substitute: 2 });
  assert.equal(tie.cost, 2);
  assert.equal(tie.count, 3n);
  assert.deepEqual(tie.unrank(0), [{ type: 'substitute', from: 'a', to: 'b' }]);
  assert.deepEqual(tie.unrank(1), [
    { type: 'delete', char: 'a' },
    { type: 'insert', char: 'b' },
  ]);
  assert.deepEqual(tie.unrank(2), [
    { type: 'insert', char: 'b' },
    { type: 'delete', char: 'a' },
  ]);
});

test('unrank rejects out-of-range and malformed ranks', () => {
  const ed = new EditDistance('aa', 'a', UNIT);
  assert.throws(() => ed.unrank(-1n), RangeError);
  assert.throws(() => ed.unrank(2n), RangeError);
  assert.throws(() => ed.unrank(1.5), TypeError);
  assert.throws(() => ed.unrank('x'), TypeError);
  assert.equal(serialize(ed.unrank('1')), serialize(ed.unrank(1n)));
});

test('unrank is deterministic and matches the public boundary order', () => {
  assert.deepEqual([...MOVE_ORDER], ['diagonal', 'delete', 'insert']);
  assert.ok(Object.isFrozen(MOVE_ORDER));

  const ed = new EditDistance('abc', 'cba', { insert: 1, delete: 1, substitute: 2 });
  const again = new EditDistance('abc', 'cba', { insert: 1, delete: 1, substitute: 2 });
  const seen = new Set<string>();
  for (let k = 0n; k < ed.count; k++) {
    const path = ed.unrank(k);
    assert.equal(serialize(path), serialize(again.unrank(k)));
    seen.add(serialize(path));
  }
  assert.equal(BigInt(seen.size), ed.count, 'ranks must be pairwise distinct');
});

test('boundary paths of a large binomial count', () => {
  // disjoint alphabets, substitute too expensive: only deletes and inserts.
  // optimal paths = interleavings of 30 deletes and 30 inserts = C(60, 30).
  const costs: OperationCosts = { insert: 1, delete: 1, substitute: 3 };
  const source = 'a'.repeat(30);
  const target = 'b'.repeat(30);
  const ed = new EditDistance(source, target, costs);
  assert.equal(ed.cost, 60);

  let binom = 1n;
  for (let i = 0; i < 30; i++) {
    binom = (binom * BigInt(60 - i)) / BigInt(i + 1);
  }
  assert.equal(ed.count, binom);
  assert.ok(ed.count > 2n ** 53n, 'count must exceed float precision');

  const first = ed.unrank(0n);
  const last = ed.unrank(ed.count - 1n);
  assert.deepEqual(
    first.map((op) => op.type),
    [...Array(30).fill('delete'), ...Array(30).fill('insert')],
  );
  assert.deepEqual(
    last.map((op) => op.type),
    [...Array(30).fill('insert'), ...Array(30).fill('delete')],
  );
  for (const k of [0n, 1n, binom / 2n, binom - 2n, binom - 1n]) {
    const path = ed.unrank(k);
    assert.equal(applyPath(source, path), target);
    assert.equal(pathCost(path, costs), 60);
  }
});

test('unicode code points are handled as single characters', () => {
  const ed = new EditDistance('🙃🙃', '🙃', UNIT);
  assert.equal(ed.cost, 1);
  assert.equal(ed.count, 2n);
  for (const k of [0n, 1n]) {
    assert.equal(applyPath('🙃🙃', ed.unrank(k)), '🙃');
  }
  const mixed = new EditDistance('🙂🙃', '🙃', UNIT);
  assert.equal(mixed.cost, 1);
  assert.equal(mixed.count, 1n);
});

test('exhaustive cross-check against brute-force enumeration', () => {
  const alphabet = ['a', 'b'];
  const strings: string[] = [''];
  for (let length = 1; length <= 4; length++) {
    for (let mask = 0; mask < 2 ** length; mask++) {
      let s = '';
      for (let bit = 0; bit < length; bit++) {
        s += (mask >> bit) & 1 ? 'b' : 'a';
      }
      strings.push(s);
    }
  }
  const costSets: OperationCosts[] = [
    { insert: 1, delete: 1, substitute: 1 },
    { insert: 1, delete: 1, substitute: 2 },
    { insert: 2, delete: 1, substitute: 1 },
    { insert: 1, delete: 2, substitute: 3 },
    { insert: 3, delete: 1, substitute: 2 },
  ];
  let pairs = 0;
  let ranked = 0;
  for (const costs of costSets) {
    for (const source of strings) {
      for (const target of strings) {
        pairs++;
        const ed = new EditDistance(source, target, costs);
        const brute = enumerateOptimalPaths(source, target, costs);
        assert.equal(ed.cost, brute.cost, `cost ${source} -> ${target}`);
        assert.equal(
          ed.count,
          BigInt(brute.paths.length),
          `count ${source} -> ${target} ${JSON.stringify(costs)}`,
        );
        for (let k = 0; k < brute.paths.length; k++) {
          ranked++;
          const path = ed.unrank(k);
          assert.deepEqual(
            path,
            brute.paths[k],
            `rank ${k} of ${source} -> ${target} ${JSON.stringify(costs)}`,
          );
          assert.equal(applyPath(source, path), target);
          assert.equal(pathCost(path, costs), ed.cost);
        }
      }
    }
  }
  assert.ok(pairs > 0 && ranked > 0);
});
