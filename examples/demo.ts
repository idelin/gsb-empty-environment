import {
  largestRemainder,
  solve,
  reallocate,
  verifyCertificate,
  type SolveInput,
} from '../src/index.ts';

const show = (v: unknown): string =>
  JSON.stringify(
    v,
    (_, x) => (typeof x === 'bigint' ? `${x}n` : x),
    2,
  );

const section = (title: string): void => {
  console.log(`\n=== ${title} ===`);
};

// 1. Largest remainder baseline: total 1 over three equal shares.
section('1. Largest remainder baseline: total 1, three equal weights');
const base = largestRemainder(
  [
    { id: 'a', weight: '1/3' },
    { id: 'b', weight: '1/3' },
    { id: 'c', weight: '1/3' },
  ],
  1n,
);
console.log(show(base));
console.log('-> exactly one share of 1; equal remainders go to stable id "a"');

// 2. Exact rational weights, huge total (no Number transit).
section('2. Exact rational weights with a huge total');
const big = largestRemainder(
  [
    { id: 'x', weight: '1/7' },
    { id: 'y', weight: '2/7' },
    { id: 'z', weight: '4/7' },
  ],
  10n ** 30n + 7n,
);
if (big.status === 'ok') {
  console.log('allocation:', show(big.allocation));
  const sum = big.allocation.x + big.allocation.y + big.allocation.z;
  console.log('conserved:', sum === 10n ** 30n + 7n);
}

// 3. Constrained solve with overlapping groups + certificates.
section('3. Overlapping group bounds, solved globally');
const constrained: SolveInput = {
  total: 10n,
  items: [
    { id: 'a', weight: 1n },
    { id: 'b', weight: 1n },
    { id: 'c', weight: 1n },
  ],
  groups: [
    { id: 'g1', members: ['a', 'b'], lower: 7n },
    { id: 'g2', members: ['b', 'c'], lower: 7n },
  ],
};
const r3 = solve(constrained);
console.log(show(r3));
if (r3.status === 'optimal') {
  const v = verifyCertificate(constrained, r3);
  console.log('independent verification ok:', v.ok);
  for (const c of v.checks) console.log(`  [${c.ok ? 'PASS' : 'FAIL'}] ${c.name}: ${c.detail}`);
}

// 4. Infeasible bounds: deficit reported, kept pending, never forced.
section('4. Infeasible lower bounds -> deficit + pending');
const r4 = solve({
  total: 10n,
  items: [
    { id: 'a', weight: 1n, lower: 5n },
    { id: 'b', weight: 1n, lower: 5n },
    { id: 'c', weight: 1n, lower: 4n },
  ],
});
console.log(show(r4));

// 5. Negative weights rejected.
section('5. Negative weight rejected');
console.log(
  show(
    solve({
      total: 5n,
      items: [
        { id: 'a', weight: '-1/2' },
        { id: 'b', weight: 1n },
      ],
    }),
  ),
);

// 6. Re-weighting: minimal movement first, then deviation.
section('6. Re-weighting: movement first, then deviation');
const r6 = reallocate({
  total: 10n,
  previous: { a: 5n, b: 5n },
  items: [
    { id: 'a', weight: 1n },
    { id: 'b', weight: 9n },
  ],
});
console.log(show(r6));
console.log('-> weights say 1:9, but zero movement beats deviation optimization');

// 7. Re-weighting with insufficient budget: shortfall stays pending.
section('7. Re-weighting with insufficient budget -> pending');
console.log(
  show(
    reallocate({
      total: 8n,
      previous: { a: 4n, b: 4n },
      items: [
        { id: 'a', weight: 1n, lower: 5n },
        { id: 'b', weight: 1n, lower: 5n },
      ],
    }),
  ),
);
