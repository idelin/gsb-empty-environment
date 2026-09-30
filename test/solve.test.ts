import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { loadInstance } from '../src/model.ts';
import { solve } from '../src/solve.ts';
import { exhaustiveSolve } from '../src/exhaustive.ts';
import { checkConstraints } from '../src/error.ts';
import { toExactString, cmp, parseDecimal } from '../src/rational.ts';

const demo = loadInstance(JSON.parse(readFileSync(new URL('../samples/demo.json', import.meta.url), 'utf8')));
const tiny = loadInstance(JSON.parse(readFileSync(new URL('../samples/tiny.json', import.meta.url), 'utf8')));

test('B&B optimum matches exhaustive enumeration (demo)', () => {
  const r = solve(demo);
  const ex = exhaustiveSolve(demo);
  assert.equal(r.status, 'optimal');
  assert.ok(r.incumbent);
  assert.ok(ex.best);
  assert.equal(toExactString(r.incumbent.cost), toExactString(ex.best.cost));
  assert.equal(toExactString(r.incumbent.cost), '35');
  // proven lower bound equals achieved cost on completion
  assert.ok(r.lowerBound && cmp(r.lowerBound, r.incumbent.cost) === 0);
  // solution respects all constraints
  assert.deepEqual(checkConstraints(demo, r.incumbent.selection), []);
});

test('B&B optimum matches exhaustive enumeration (tiny)', () => {
  const r = solve(tiny);
  const ex = exhaustiveSolve(tiny);
  assert.equal(r.status, 'optimal');
  assert.equal(toExactString(r.incumbent!.cost), toExactString(ex.best!.cost));
});

test('node budget exhaustion: undecided, bounds reported, optimality not claimed', () => {
  const r = solve(demo, { nodeBudget: 1 });
  assert.equal(r.status, 'undecided');
  assert.ok(r.budgetExhausted);
  assert.equal(r.incumbent, null);
  // proven lower bound = sum of cheapest tier costs = 8+5+6+4 = 23
  assert.ok(r.lowerBound);
  assert.equal(toExactString(r.lowerBound), '23');
  // and the bound is genuinely valid: optimum 35 >= 23
  assert.ok(cmp(r.lowerBound, parseDecimal('35')) <= 0);
});

test('partial budget: incumbent may exist, still undecided, LB <= UB', () => {
  const r = solve(demo, { nodeBudget: 6 });
  assert.equal(r.status, 'undecided');
  assert.ok(r.lowerBound);
  if (r.incumbent) assert.ok(cmp(r.lowerBound, r.incumbent.cost) <= 0);
});

test('infeasible instance: proven, lower bound is +infinity (null)', () => {
  const impossible = loadInstance({
    name: 'impossible',
    errorBudget: '0.0001',
    sources: [{ id: 'n', kind: 'independent' }],
    components: [
      {
        id: 'x',
        tiers: [
          { id: 't1', cost: '1', error: '0.5' },
          { id: 't2', cost: '2', error: '0.2' },
        ],
        sensitivities: { n: '1' },
      },
    ],
  });
  const r = solve(impossible);
  assert.equal(r.status, 'infeasible');
  assert.equal(r.lowerBound, null);
  const ex = exhaustiveSolve(impossible);
  assert.equal(ex.best, null);
});

test('dependency forces expensive tier when antecedent selected', () => {
  const inst = loadInstance({
    name: 'dep',
    errorBudget: '10',
    sources: [{ id: 'n', kind: 'independent' }],
    components: [
      {
        id: 'master',
        tiers: [
          { id: 'boost', cost: '1', error: '0.1' },
          { id: 'plain', cost: '1', error: '0.1' },
        ],
        sensitivities: { n: '1' },
      },
      {
        id: 'slave',
        tiers: [
          { id: 'cheap', cost: '1', error: '0.1' },
          { id: 'needed', cost: '50', error: '0.1' },
        ],
        sensitivities: { n: '1' },
      },
    ],
    dependencies: [
      { if: { component: 'master', tier: 'boost' }, requires: { component: 'slave', tier: 'needed' } },
    ],
  });
  const r = solve(inst);
  assert.equal(r.status, 'optimal');
  // boost+needed costs 51; plain+cheap costs 2 -> solver must avoid the dependency trap
  assert.equal(toExactString(r.incumbent!.cost), '2');
  assert.equal(r.incumbent!.selection.get('master'), 'plain');
});

test('exclusion forbids the otherwise-cheapest pair', () => {
  const inst = loadInstance({
    name: 'excl',
    errorBudget: '10',
    sources: [{ id: 'n', kind: 'independent' }],
    components: [
      {
        id: 'p',
        tiers: [
          { id: 'a', cost: '1', error: '0.1' },
          { id: 'b', cost: '5', error: '0.1' },
        ],
        sensitivities: { n: '1' },
      },
      {
        id: 'q',
        tiers: [
          { id: 'a', cost: '1', error: '0.1' },
          { id: 'b', cost: '7', error: '0.1' },
        ],
        sensitivities: { n: '1' },
      },
    ],
    exclusions: [
      { a: { component: 'p', tier: 'a' }, b: { component: 'q', tier: 'a' } },
    ],
  });
  const r = solve(inst);
  assert.equal(r.status, 'optimal');
  assert.equal(toExactString(r.incumbent!.cost), '6'); // p=b,q=a -> 5+1 (a,a is excluded)
});

// deterministic PRNG for reproducible randomized cross-checks
function lcg(seed: number) {
  let s = seed >>> 0;
  return () => ((s = (s * 1664525 + 1013904223) >>> 0) / 2 ** 32);
}

test('randomized small instances: B&B always matches exhaustive', () => {
  for (let seed = 1; seed <= 30; seed++) {
    const rand = lcg(seed);
    const pick = (arr: string[]) => arr[Math.floor(rand() * arr.length)];
    const nComp = 2 + Math.floor(rand() * 3); // 2..4 components
    const components = [];
    for (let i = 0; i < nComp; i++) {
      const nTier = 2 + Math.floor(rand() * 2); // 2..3 tiers
      const tiers = [];
      for (let j = 0; j < nTier; j++) {
        tiers.push({
          id: `t${j}`,
          cost: (1 + Math.floor(rand() * 30)).toString(),
          error: (Math.floor(rand() * 20) / 1000 + 0.001).toFixed(3),
        });
      }
      const sens: Record<string, string> = { cal: pick(['-1.5', '-0.5', '0.5', '1.0', '2.0']) };
      sens[`n${i}`] = pick(['-0.8', '0.4', '1.0']);
      components.push({ id: `c${i}`, tiers, sensitivities: sens });
    }
    const sources = [
      { id: 'cal', kind: 'shared' },
      ...components.map((_, i) => ({ id: `n${i}`, kind: 'independent' })),
    ];
    const inst = loadInstance({
      name: `rand-${seed}`,
      errorBudget: (Math.floor(rand() * 60) / 1000 + 0.005).toFixed(3),
      sources,
      components,
    });
    const r = solve(inst);
    const ex = exhaustiveSolve(inst);
    if (ex.best === null) {
      assert.equal(r.status, 'infeasible', `seed ${seed}`);
    } else {
      assert.equal(r.status, 'optimal', `seed ${seed}`);
      assert.equal(toExactString(r.incumbent!.cost), toExactString(ex.best.cost), `seed ${seed}`);
    }
  }
});
