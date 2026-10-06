import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  PolicyEngine,
  decideFlat,
  shadowedFlat,
} from '../src/index.ts';
import type { PolicyConfig, Rule, ShadowedEntry } from '../src/index.ts';

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

const SEGMENTS = ['a', 'b', 'c'];

function randomPrefix(rand: () => number, maxDepth: number): string {
  const d = Math.floor(rand() * (maxDepth + 1));
  if (d === 0) return '/';
  const parts: string[] = [];
  for (let i = 0; i < d; i++) parts.push(SEGMENTS[Math.floor(rand() * SEGMENTS.length)]);
  return '/' + parts.join('/');
}

function randomRule(rand: () => number, conflictHeavy: boolean): Rule {
  return {
    prefix: randomPrefix(rand, conflictHeavy ? 1 : 3),
    effect: rand() < 0.5 ? 'allow' : 'deny',
  };
}

/** Random config; inheritance only points to earlier groups, so it is a DAG. */
function randomConfig(rand: () => number, opts: { conflictHeavy?: boolean } = {}): PolicyConfig {
  const groupCount = 1 + Math.floor(rand() * 5);
  const groups: Record<string, { rules: Rule[]; inherits: string[] }> = {};
  const names: string[] = [];
  for (let i = 0; i < groupCount; i++) {
    const name = `g${i}`;
    const ruleCount = Math.floor(rand() * 5);
    const rules: Rule[] = [];
    for (let r = 0; r < ruleCount; r++) rules.push(randomRule(rand, opts.conflictHeavy ?? false));
    const inherits: string[] = [];
    for (const parent of names) {
      if (rand() < 0.35) inherits.push(parent);
    }
    groups[name] = { rules, inherits };
    names.push(name);
  }
  return { groups };
}

function randomPath(rand: () => number): string {
  return randomPrefix(rand, 4);
}

function shadowKey(entries: ShadowedEntry[]): string[] {
  return entries
    .map((e) => `${e.shadowed.origin}#${e.shadowed.index}:${e.reason}:by:${e.shadowedBy.origin}#${e.shadowedBy.index}`)
    .sort();
}

function assertEngineMatchesModel(engine: PolicyEngine, config: PolicyConfig, rand: () => number, probes: number): void {
  const names = Object.keys(config.groups);
  for (let i = 0; i < probes; i++) {
    const group = names[Math.floor(rand() * names.length)];
    const path = randomPath(rand);
    const expected = decideFlat(config, group, path);
    const actual = engine.decide(group, path);
    assert.equal(actual.effect, expected.effect, `effect for ${group} ${path}`);
    assert.equal(actual.reason, expected.reason, `reason for ${group} ${path}`);
    const w1 = actual.winner ? `${actual.winner.origin}#${actual.winner.index}` : null;
    const w2 = expected.winner ? `${expected.winner.origin}#${expected.winner.index}` : null;
    assert.equal(w1, w2, `winner for ${group} ${path}`);
  }
  const expectedShadow = shadowedFlat(config);
  for (const name of names) {
    assert.deepEqual(
      shadowKey(engine.explain(name).shadowed),
      shadowKey(expectedShadow.get(name) ?? []),
      `shadowed rules for ${name}`,
    );
  }
}

test('cross-check: engine vs expand-all + prefix-match model on random configs', () => {
  const rand = mulberry32(20261007);
  for (let round = 0; round < 300; round++) {
    const config = randomConfig(rand);
    const engine = new PolicyEngine(config);
    assertEngineMatchesModel(engine, config, rand, 20);
  }
});

test('cross-check: same-level conflicts (duplicate prefixes, mixed effects)', () => {
  const rand = mulberry32(424242);
  for (let round = 0; round < 300; round++) {
    const config = randomConfig(rand, { conflictHeavy: true });
    const engine = new PolicyEngine(config);
    assertEngineMatchesModel(engine, config, rand, 30);
  }
});

test('cross-check: rejected updates roll back; reverting reproduces decisions', () => {
  const rand = mulberry32(777);
  let config = randomConfig(rand);
  const engine = new PolicyEngine(config);
  const history: PolicyConfig[] = [config];

  for (let step = 0; step < 60; step++) {
    const action = rand();
    if (action < 0.3 && history.length > 1) {
      // 撤销: revert to an earlier accepted config.
      const idx = Math.floor(rand() * (history.length - 1));
      config = history[idx];
      const result = engine.update(config);
      assert.equal(result.ok, true, `revert at step ${step}`);
    } else if (action < 0.55) {
      // Inject an inheritance cycle; update must be rejected atomically.
      const broken = structuredClone(config);
      const names = Object.keys(broken.groups);
      const victim = names[Math.floor(rand() * names.length)];
      const g = broken.groups[victim];
      g.inherits = [...(g.inherits ?? []), victim];
      const result = engine.update(broken);
      assert.equal(result.ok, false, `cycle rejected at step ${step}`);
      if (!result.ok) assert.equal(result.error.kind, 'inheritance-cycle');
      // Config must be untouched after the rejected update.
      assertEngineMatchesModel(engine, config, rand, 10);
      continue;
    } else {
      config = randomConfig(rand);
      const result = engine.update(config);
      assert.equal(result.ok, true, `update at step ${step}`);
      history.push(config);
    }
    assertEngineMatchesModel(engine, config, rand, 10);
  }
});

test('model rejects cyclic configs too', () => {
  const cyclic: PolicyConfig = {
    groups: {
      a: { inherits: ['b'] },
      b: { inherits: ['a'] },
    },
  };
  assert.throws(() => decideFlat(cyclic, 'a', '/x'), /cycle/);
});
