// Differential testing: the incremental engine is cross-checked against the
// independent full interpreter over random trees, random stylesheets and
// random mutations (class flips, state toggles, inline variable edits and
// rule modifications). Cyclic variables, inheritance overrides and
// same-specificity source order are exercised by the generators.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildDocument, flatten } from '../src/engine/model.ts';
import { StyleEngine } from '../src/engine/engine.ts';
import { computeAllStyles } from '../src/engine/reference.ts';
import { mulberry32, randomRule, randomStylesheet, randomTree } from './randomkit.ts';
import type { DocNode } from '../src/engine/model.ts';

const CLASSES = ['a', 'b', 'c', 'd'];
const STATES = ['hover', 'active'];
const INLINE_PROPS = ['--v0', '--v2', 'color', 'margin'];
const INLINE_VALUES = ['3', 'teal', 'var(--v1, 6)', 'var(--v3)', '12'];

function crosscheck(seed: number, steps: number): void {
  const rng = mulberry32(seed);
  const root = buildDocument(randomTree(rng));
  const nodes = flatten(root);
  const rules = randomStylesheet(rng);
  const inline = new Map<number, Map<string, string>>();
  const engine = new StyleEngine(root, rules.join('\n'));
  const pick = <T,>(arr: T[]): T => arr[Math.floor(rng() * arr.length)];

  const verify = (label: string): void => {
    const snap = engine.getSnapshot();
    const ref = computeAllStyles(nodes, rules.join('\n'), inline);
    try {
      assert.deepStrictEqual(snap.store, ref);
    } catch (err) {
      throw new Error(`divergence at seed ${seed}, ${label}: ${(err as Error).message}`);
    }
  };
  verify('initial build');

  for (let step = 0; step < steps; step++) {
    const kind = rng();
    if (kind < 0.35) {
      const node = pick(nodes);
      const cls = pick(CLASSES);
      const on = rng() < 0.5;
      engine.setClass(node.id, cls, on);
    } else if (kind < 0.5) {
      engine.toggleState(pick(nodes).id, pick(STATES));
    } else if (kind < 0.7) {
      const node = pick(nodes);
      const prop = pick(INLINE_PROPS);
      if (rng() < 0.25) {
        engine.setInline(node.id, prop, null);
        inline.get(node.id)?.delete(prop);
      } else {
        const value = pick(INLINE_VALUES);
        engine.setInline(node.id, prop, value);
        let map = inline.get(node.id);
        if (!map) {
          map = new Map();
          inline.set(node.id, map);
        }
        map.set(prop, value);
      }
    } else if (kind < 0.85) {
      // Rule modification: rewrite one rule, occasionally add or remove one.
      const idx = Math.floor(rng() * rules.length);
      const roll = rng();
      if (roll < 0.2 && rules.length > 2) rules.splice(idx, 1);
      else if (roll < 0.35) rules.push(randomRule(rng));
      else rules[idx] = randomRule(rng);
      engine.updateStylesheet(rules.join('\n'));
    } else {
      engine.setClass(pick(nodes).id, pick(CLASSES), rng() < 0.5);
      engine.toggleState(pick(nodes).id, pick(STATES));
    }
    verify(`step ${step}`);
  }
}

test('differential: incremental engine vs full interpreter on random inputs', () => {
  for (let seed = 1; seed <= 100; seed++) {
    crosscheck(seed, 25);
  }
});

test('differential: cyclic-variable heavy seeds', () => {
  for (let seed = 1000; seed < 1015; seed++) {
    const rng = mulberry32(seed);
    const root = buildDocument(randomTree(rng));
    const nodes = flatten(root);
    const rules = [
      '.a { --v0: var(--v1); --v1: var(--v0); color: var(--v0, red); }',
      '.b { --v2: var(--v2); color: var(--v2); margin: var(--v9, 3); }',
      '.a .b { color: var(--v1, blue); }',
      ...randomStylesheet(rng),
    ];
    const engine = new StyleEngine(root, rules.join('\n'));
    const ref = computeAllStyles(nodes, rules.join('\n'), new Map());
    assert.deepStrictEqual(engine.getSnapshot().store, ref);
    // Mutate classes to move nodes in and out of the cyclic rules' scope.
    for (const node of nodes) {
      engine.setClass(node.id, 'a', true);
      engine.setClass(node.id, 'b', true);
    }
    assert.deepStrictEqual(engine.getSnapshot().store, computeAllStyles(nodes, rules.join('\n'), new Map()));
  }
});

test('directed: same-specificity source order survives incremental rule edits', () => {
  const root = buildDocument({ tag: 'p', classes: ['a'], text: 'x' });
  const nodes = flatten(root);
  const css1 = '.a { color: red; } .a { color: blue; }';
  const engine = new StyleEngine(root, css1);
  assert.equal(engine.getSnapshot().get(nodes[0].id, 'color')!.value, 'blue');
  const css2 = '.a { color: blue; } .a { color: red; }';
  engine.updateStylesheet(css2);
  const snap = engine.getSnapshot();
  assert.equal(snap.get(nodes[0].id, 'color')!.value, 'red');
  assert.deepStrictEqual(snap.store, computeAllStyles(nodes, css2, new Map()));
});

test('directed: low-specificity later rules never override high-specificity earlier ones', () => {
  const root = buildDocument({
    tag: 'div', id: 'x1', children: [{ tag: 'p', classes: ['a'], text: 'x' }],
  });
  const nodes = flatten(root);
  const css = `
    #x1 p { color: red; }
    .a { color: blue; }
    p.a { color: green; }
    div .a { color: orange; }
  `;
  const engine = new StyleEngine(root, css);
  const cv = engine.getSnapshot().get(nodes[1].id, 'color')!;
  assert.equal(cv.value, 'red');
  assert.deepEqual(cv.chain.map((c) => c.outcome), [
    'lost-specificity',
    'lost-specificity',
    'lost-specificity',
    'won',
  ]);
  assert.deepStrictEqual(engine.getSnapshot().store, computeAllStyles(nodes, css, new Map()));
});

test('directed: inheritance override at multiple depths', () => {
  const root = buildDocument({
    tag: 'div', classes: ['a'], children: [
      { tag: 'section', children: [{ tag: 'p', classes: ['b'], text: 'x' }] },
    ],
  });
  const nodes = flatten(root);
  const css = '.a { color: red; } section { color: green; } .b { color: var(--missing, blue); }';
  const engine = new StyleEngine(root, css);
  const snap = engine.getSnapshot();
  assert.equal(snap.get(nodes[0].id, 'color')!.value, 'red');
  assert.equal(snap.get(nodes[1].id, 'color')!.value, 'green');
  assert.equal(snap.get(nodes[2].id, 'color')!.value, 'blue');
  assert.deepStrictEqual(snap.store, computeAllStyles(nodes, css, new Map()));
});
