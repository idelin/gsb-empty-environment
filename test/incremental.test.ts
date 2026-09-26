import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildDocument, flatten } from '../src/engine/model.ts';
import { StyleEngine } from '../src/engine/engine.ts';
import { computeAllStyles } from '../src/engine/reference.ts';

const CSS = `
.app { --accent: red; color: var(--accent); }
.app.alt { --accent: blue; }
.card { padding: 4; }
.desc { color: var(--accent); margin: 2; }
.desc:hover { color: gold; }
`;

function makeEngine() {
  const root = buildDocument({
    tag: 'div', classes: ['app'], children: [
      { tag: 'section', classes: ['card'], children: [
        { tag: 'p', classes: ['desc'], text: 'a' },
        { tag: 'p', classes: ['desc'], text: 'b' },
      ]},
    ],
  });
  const nodes = flatten(root);
  return { engine: new StyleEngine(root, CSS), nodes };
}

test('ancestor class change recomputes only affected properties', () => {
  const { engine, nodes } = makeEngine();
  const before = engine.getSnapshot();
  assert.equal(before.get(nodes[2].id, 'color')!.value, 'red');
  const report = engine.setClass(nodes[0].id, 'alt', true);
  const after = engine.getSnapshot();
  assert.equal(after.get(nodes[2].id, 'color')!.value, 'blue');
  // Only --accent entries and their dependents may be recomputed.
  const evaluated = new Set(report.evaluated);
  assert.ok(evaluated.has(`${nodes[0].id}|--accent`));
  assert.ok(evaluated.has(`${nodes[2].id}|color`));
  for (const key of evaluated) {
    const prop = key.slice(key.indexOf('|') + 1);
    assert.ok(prop === '--accent' || prop === 'color', `unexpected recompute: ${key}`);
  }
  assert.ok(after.version > before.version);
});

test('variable edit propagates to dependents only, leaving unrelated entries untouched', () => {
  const { engine, nodes } = makeEngine();
  const before = engine.getSnapshot();
  const untouched = new Map(before.store);
  const report = engine.updateStylesheet(CSS.replace('--accent: red', '--accent: teal'));
  const after = engine.getSnapshot();
  assert.equal(after.get(nodes[3].id, 'color')!.value, 'teal');
  for (const key of report.evaluated) {
    const prop = key.slice(key.indexOf('|') + 1);
    assert.ok(prop === '--accent' || prop === 'color', `unexpected recompute: ${key}`);
  }
  // Entries outside the dirty closure keep their identity and value.
  for (const [key, value] of after.store) {
    if (!report.evaluated.includes(key)) {
      assert.equal(untouched.get(key), value, `entry ${key} should be untouched`);
    }
  }
});

test('updates publish atomically: an old snapshot never observes mixed versions', () => {
  const { engine, nodes } = makeEngine();
  const snap1 = engine.getSnapshot();
  const frozen = new Map(snap1.store);
  engine.setClass(nodes[0].id, 'alt', true);
  engine.toggleState(nodes[2].id, 'hover');
  const snap2 = engine.getSnapshot();
  // The old snapshot is fully intact (every entry identical to its frozen copy).
  assert.equal(snap1.version + 2, snap2.version);
  for (const [key, value] of snap1.store) {
    assert.equal(frozen.get(key), value);
  }
  assert.equal(snap1.get(nodes[2].id, 'color')!.value, 'red');
  // The new snapshot is fully consistent with a from-scratch recomputation.
  assert.equal(snap2.get(nodes[2].id, 'color')!.value, 'gold');
  assert.equal(snap2.get(nodes[3].id, 'color')!.value, 'blue');
});

test('inline variable change only invalidates its dependent subtree entries', () => {
  const { engine, nodes } = makeEngine();
  const report = engine.setInline(nodes[1].id, '--accent', 'purple');
  const after = engine.getSnapshot();
  assert.equal(after.get(nodes[2].id, 'color')!.value, 'purple');
  assert.equal(after.get(nodes[3].id, 'color')!.value, 'purple');
  const evaluated = new Set(report.evaluated);
  assert.ok(evaluated.has(`${nodes[1].id}|--accent`));
  assert.ok(!evaluated.has(`${nodes[0].id}|margin`));
  assert.ok(!evaluated.has(`${nodes[2].id}|padding`));
});

test('state toggles re-match only nodes whose selectors consulted the toggled node', () => {
  const { engine, nodes } = makeEngine();
  const report = engine.toggleState(nodes[2].id, 'hover');
  assert.equal(engine.getSnapshot().get(nodes[2].id, 'color')!.value, 'gold');
  const evaluated = new Set(report.evaluated);
  assert.ok(evaluated.has(`${nodes[2].id}|color`));
  assert.ok(!evaluated.has(`${nodes[3].id}|color`));
});

test('incremental results match the independent full interpreter after each step', () => {
  const { engine, nodes } = makeEngine();
  const inline = new Map<number, Map<string, string>>();
  const check = (css: string) => {
    const ref = computeAllStyles(nodes, css, inline);
    assert.deepStrictEqual(engine.getSnapshot().store, ref);
  };
  check(CSS);
  engine.setClass(nodes[0].id, 'alt', true);
  check(CSS);
  engine.toggleState(nodes[3].id, 'hover');
  check(CSS);
  inline.set(nodes[1].id, new Map([['--accent', 'olive']]));
  engine.setInline(nodes[1].id, '--accent', 'olive');
  check(CSS);
  const css2 = CSS.replace('--accent: red', '--accent: teal');
  engine.updateStylesheet(css2);
  check(css2);
});
