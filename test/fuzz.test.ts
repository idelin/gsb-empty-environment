/**
 * Randomized cross-validation: every snapshot produced by a random sequence
 * of module updates (including renames, deletions of final definitions,
 * diamonds and cycles) is checked against the independent reference resolver,
 * which searches from each reference site separately.
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Project } from '../src/index.ts';
import { validateSnapshot } from '../src/validate.ts';
import type { ModuleSpec } from '../src/types.ts';

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomSpec(rnd: () => number, ids: readonly string[], names: readonly string[]): ModuleSpec {
  const pick = <T>(arr: readonly T[]): T => arr[Math.floor(rnd() * arr.length)]!;
  const definitions = names.filter(() => rnd() < 0.3);
  const reexports = Array.from({ length: Math.floor(rnd() * 3) }, () => ({
    name: pick(names),
    from: pick(ids),
    as: rnd() < 0.5 ? pick(names) : undefined,
  }));
  const imports = Array.from({ length: Math.floor(rnd() * 3) }, () => ({
    name: pick(names),
    from: pick(ids),
  }));
  return { definitions, reexports, imports };
}

test('incremental engine matches the independent reference on random module graphs', () => {
  const NAMES = ['a', 'b', 'c', 'd', 'e'];
  for (let seed = 1; seed <= 60; seed++) {
    const rnd = mulberry32(seed);
    const project = new Project();
    const ids = Array.from({ length: 4 + Math.floor(rnd() * 5) }, (_, i) => `/m${i}.ts`);

    for (const id of ids) {
      project.setModule(id, randomSpec(rnd, ids, NAMES));
    }
    assert.deepEqual(validateSnapshot(project.snapshot()), [], `seed ${seed} initial`);

    const steps = 3 + Math.floor(rnd() * 6);
    for (let step = 0; step < steps; step++) {
      const id = ids[Math.floor(rnd() * ids.length)]!;
      if (rnd() < 0.2) {
        project.removeModule(id);
      } else {
        project.setModule(id, randomSpec(rnd, ids, NAMES));
      }
      const snap = project.snapshot();
      assert.deepEqual(validateSnapshot(snap), [], `seed ${seed} step ${step}`);
      for (const mid of snap.moduleIds()) {
        snap.diagnostics(mid); // must never throw
      }
    }
  }
});
