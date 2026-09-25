import { test } from 'node:test';
import assert from 'node:assert/strict';
import { solveLimitObligations } from '../src/limits.ts';
import { enumerateLimitSolutions, referenceCheckLimits } from '../src/reference.ts';
import type { LimitObligation } from '../src/obligations.ts';

/** deterministic PRNG so the randomized cross-check is reproducible */
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomSystem(rand: () => number): LimitObligation[] {
  const names = ['x', 'y', 'z'].slice(0, 2 + Math.floor(rand() * 2));
  const obs: LimitObligation[] = [];
  let n = 0;
  for (const name of names) {
    if (rand() < 0.8) {
      obs.push({
        kind: 'limit', id: `ob-${n++}`, name,
        bound: rand() < 0.6 ? 'upper' : 'lower',
        value: 1 + Math.floor(rand() * 9),
      });
    }
  }
  if (obs.length === 0) {
    obs.push({ kind: 'limit', id: `ob-${n++}`, name: names[0], bound: 'upper', value: 5 });
  }
  // add a random dependency on the first obligation
  const [a, b] = [names[Math.floor(rand() * names.length)], names[Math.floor(rand() * names.length)]];
  if (a !== b) {
    obs[0] = {
      ...obs[0],
      requires: [{
        left: a,
        rel: (['<=', '>=', '=='] as const)[Math.floor(rand() * 3)],
        right: b,
        offset: Math.floor(rand() * 3),
      }],
    };
  }
  return obs;
}

test('solver agrees with finite enumeration on 300 random small systems', () => {
  const rand = mulberry32(20260925);
  let feasible = 0;
  let infeasible = 0;
  for (let i = 0; i < 300; i++) {
    const obs = randomSystem(rand);
    const solved = solveLimitObligations(obs);
    assert.ok(
      referenceCheckLimits(obs, solved),
      `mismatch on system #${i}: ${JSON.stringify(obs)}`,
    );
    if (solved.ok) feasible++; else infeasible++;
  }
  // sanity: the random suite actually exercised both branches
  assert.ok(feasible > 0 && infeasible > 0, `feasible=${feasible} infeasible=${infeasible}`);
});

test('enumeration computes exact feasible projections', () => {
  const obs: LimitObligation[] = [
    { kind: 'limit', id: 'ob-1', name: 'x', bound: 'upper', value: 10,
      requires: [{ left: 'x', rel: '<=', right: 'y', offset: 2 }] },
    { kind: 'limit', id: 'ob-2', name: 'y', bound: 'upper', value: 6 },
    { kind: 'limit', id: 'ob-3', name: 'y', bound: 'lower', value: 1 },
  ];
  const ref = enumerateLimitSolutions(obs);
  assert.ok(ref.ok);
  // x <= y + 2, y <= 6  =>  x <= 8 ; y in [1, 6]
  assert.equal(ref.projection.get('x')!.max, 8);
  assert.deepEqual(ref.projection.get('y'), { min: 1, max: 6 });
  // x has no lower-bound obligation: it saturates the finite domain
  assert.equal(ref.projection.get('x')!.min, ref.domainMin);
});
