import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rat } from '../src/rational.ts';
import { ProviderContractError, TopKSorter } from '../src/sorter.ts';
import type { Interval } from '../src/sorter.ts';
import { SimulatedProvider } from '../src/simulated.ts';
import type { SimBehavior } from '../src/simulated.ts';

function rat(num: number, den = 1): Rat {
  return Rat.of(num, den);
}

interface Spec {
  readonly id: string;
  readonly lo: number;
  readonly hi: number;
  readonly behavior: SimBehavior;
}

function setup(specs: ReadonlyArray<Spec>, budget: number) {
  const provider = new SimulatedProvider<string>();
  const sorter = new TopKSorter<string>(provider, { budget });
  for (const spec of specs) {
    const initial: Interval = { lo: rat(spec.lo), hi: rat(spec.hi) };
    provider.register(spec.id, initial, spec.behavior);
    sorter.add(spec.id, initial);
  }
  return sorter;
}

test('Rat normalizes and compares exactly', () => {
  assert.ok(Rat.of(2, 4).eq(rat(1, 2)));
  assert.ok(Rat.of(1, -2).eq(rat(-1, 2)));
  assert.ok(rat(1, 3).add(rat(1, 6)).eq(rat(1, 2)));
  assert.ok(rat(2, 3).gt(rat(5, 8)));
  assert.equal(rat(-3, 7).toString(), '-3/7');
});

test('overlapping intervals are refined until disjoint; order is proven, not guessed by midpoints', () => {
  // Midpoints say A(5) < B(7.5); true values are B(6) < A(9).
  const sorter = setup(
    [
      { id: 'A', lo: 0, hi: 10, behavior: { trueValue: rat(9) } },
      { id: 'B', lo: 4, hi: 11, behavior: { trueValue: rat(6) } },
    ],
    100,
  );
  const result = sorter.run(2);
  assert.equal(result.complete, true);
  assert.deepEqual(result.prefix, [['B'], ['A']]);
  assert.deepEqual(result.pending, []);
  assert.ok(result.budgetUsed > 0);
});

test('items certified exactly equal land in the same prefix group', () => {
  const sorter = setup(
    [
      { id: 'A', lo: 0, hi: 10, behavior: { trueValue: rat(5), exactAfter: 2 } },
      { id: 'B', lo: 2, hi: 8, behavior: { trueValue: rat(5), exactAfter: 3 } },
      { id: 'C', lo: 7, hi: 12, behavior: { trueValue: rat(9), exactAfter: 2 } },
    ],
    50,
  );
  const result = sorter.run(3);
  assert.equal(result.complete, true);
  assert.equal(result.prefix.length, 2);
  assert.deepEqual(new Set(result.prefix[0]), new Set(['A', 'B']));
  assert.deepEqual(result.prefix[1], ['C']);
});

test('touching intervals are not considered separated', () => {
  // A.hi == B.lo == 5: no strict gap, so no order may be claimed.
  const sorter = setup(
    [
      { id: 'A', lo: 0, hi: 5, behavior: { trueValue: rat(5), exhaustAfter: 0 } },
      { id: 'B', lo: 5, hi: 10, behavior: { trueValue: rat(5), exhaustAfter: 0 } },
    ],
    10,
  );
  const result = sorter.run(2);
  assert.equal(result.complete, false);
  assert.deepEqual(result.prefix, []);
  assert.deepEqual(new Set(result.pending[0]), new Set(['A', 'B']));
});

test('budget exhaustion returns the determined prefix plus pending groups', () => {
  const sorter = setup(
    [
      { id: 'A', lo: 0, hi: 1, behavior: { trueValue: rat(0) } },
      { id: 'B', lo: 2, hi: 3, behavior: { trueValue: rat(2) } },
      { id: 'C', lo: 4, hi: 6, behavior: { trueValue: rat(5) } },
      { id: 'D', lo: 5, hi: 7, behavior: { trueValue: rat(11, 2) } },
    ],
    0,
  );
  const result = sorter.run(4);
  assert.equal(result.complete, false);
  assert.equal(result.budgetUsed, 0);
  assert.deepEqual(result.prefix, [['A'], ['B']]);
  // C and D overlap; they must stay an unordered pending group (midpoints would
  // have suggested C(5) < D(6), which is not a proven fact).
  assert.equal(result.pending.length, 1);
  assert.deepEqual(new Set(result.pending[0]), new Set(['C', 'D']));
});

test('partial budget: refinement happens but unresolved items remain pending', () => {
  const sorter = setup(
    [
      { id: 'C', lo: 4, hi: 6, behavior: { trueValue: rat(5) } },
      { id: 'D', lo: 5, hi: 7, behavior: { trueValue: rat(6) } },
    ],
    2,
  );
  const result = sorter.run(2);
  assert.equal(result.complete, false);
  assert.equal(result.budgetUsed, 2);
  assert.deepEqual(result.prefix, []);
  assert.deepEqual(new Set(result.pending[0]), new Set(['C', 'D']));
});

test('provider exhaustion stops refinement instead of looping', () => {
  const sorter = setup(
    [
      { id: 'C', lo: 4, hi: 6, behavior: { trueValue: rat(5), exhaustAfter: 1 } },
      { id: 'D', lo: 5, hi: 7, behavior: { trueValue: rat(6), exhaustAfter: 1 } },
    ],
    100,
  );
  const result = sorter.run(2);
  assert.equal(result.complete, false);
  assert.equal(result.budgetUsed, 4);
  assert.deepEqual(new Set(result.pending[0]), new Set(['C', 'D']));
});

test('cancelled items are withdrawn from the ranking', () => {
  const sorter = setup(
    [
      { id: 'A', lo: 0, hi: 2, behavior: { trueValue: rat(1) } },
      { id: 'C', lo: 3, hi: 7, behavior: { trueValue: rat(5), cancelAfter: 0 } },
      { id: 'D', lo: 4, hi: 8, behavior: { trueValue: rat(6) } },
      { id: 'B', lo: 9, hi: 10, behavior: { trueValue: rat(19, 2) } },
    ],
    20,
  );
  const result = sorter.run(4);
  assert.deepEqual(result.cancelled, ['C']);
  assert.equal(result.complete, true);
  assert.deepEqual(result.prefix, [['A'], ['D'], ['B']]);
});

test('no budget is spent on items that cannot affect the top-k', () => {
  const sorter = setup(
    [
      { id: 'P', lo: 0, hi: 1, behavior: { trueValue: rat(0) } },
      { id: 'Q', lo: 2, hi: 3, behavior: { trueValue: rat(2) } },
      { id: 'R', lo: 7, hi: 9, behavior: { trueValue: rat(8) } },
      { id: 'S', lo: 8, hi: 10, behavior: { trueValue: rat(9) } },
    ],
    100,
  );
  const result = sorter.run(1);
  assert.equal(result.complete, true);
  assert.equal(result.budgetUsed, 0);
  // The determined prefix extends past k (Q is also proven); only the
  // overlapping R/S group remains pending. No refinement was needed.
  assert.deepEqual(result.prefix, [['P'], ['Q']]);
  assert.deepEqual(result.pending, [['R', 'S']]);
});

test('widening an interval violates monotonic tightening', () => {
  const sorter = setup(
    [
      { id: 'A', lo: 0, hi: 10, behavior: { trueValue: rat(5), corruptAt: 1, corruptWith: 'widen' } },
      { id: 'B', lo: 3, hi: 7, behavior: { trueValue: rat(5) } },
    ],
    10,
  );
  assert.throws(
    () => sorter.run(2),
    (err: unknown) => err instanceof ProviderContractError && /not contained/.test(err.message),
  );
});

test('returning the same interval violates strict tightening', () => {
  const sorter = setup(
    [
      { id: 'A', lo: 0, hi: 10, behavior: { trueValue: rat(5), corruptAt: 1, corruptWith: 'stagnate' } },
      { id: 'B', lo: 3, hi: 7, behavior: { trueValue: rat(5) } },
    ],
    10,
  );
  assert.throws(() => sorter.run(2), /did not shrink/);
});

test('an exact certificate outside the current interval is rejected', () => {
  const sorter = setup(
    [
      { id: 'A', lo: 0, hi: 10, behavior: { trueValue: rat(5), corruptAt: 1, corruptWith: 'exact-outside' } },
      { id: 'B', lo: 3, hi: 7, behavior: { trueValue: rat(5) } },
    ],
    10,
  );
  assert.throws(() => sorter.run(2), /outside the current interval/);
});

test('invalid initial intervals and arguments are rejected', () => {
  const provider = new SimulatedProvider<string>();
  const sorter = new TopKSorter<string>(provider, { budget: 1 });
  assert.throws(() => sorter.add('X', { lo: rat(2), hi: rat(1) }), ProviderContractError);
  assert.throws(() => new TopKSorter<string>(provider, { budget: -1 }), RangeError);
  assert.throws(() => sorter.run(-1), RangeError);
});
