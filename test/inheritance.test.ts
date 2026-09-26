import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildDocument, flatten } from '../src/engine/model.ts';
import { StyleEngine } from '../src/engine/engine.ts';

function makeEngine(css: string) {
  const root = buildDocument({
    tag: 'div', classes: ['app'], children: [
      { tag: 'section', children: [
        { tag: 'p', text: 'leaf' },
      ]},
    ],
  });
  const nodes = flatten(root);
  return { engine: new StyleEngine(root, css), nodes };
}

test('inherited and initial values stay separate in the source record', () => {
  const { engine, nodes } = makeEngine(`.app { color: red; margin: 4; }`);
  const snap = engine.getSnapshot();
  const leafColor = snap.get(nodes[2].id, 'color')!;
  assert.equal(leafColor.value, 'red');
  assert.deepEqual(leafColor.source, { kind: 'inherited', from: nodes[1].id });
  const leafMargin = snap.get(nodes[2].id, 'margin')!;
  assert.equal(leafMargin.value, '0');
  assert.deepEqual(leafMargin.source, { kind: 'initial' });
  const rootMargin = snap.get(nodes[0].id, 'margin')!;
  assert.equal(rootMargin.value, '4');
  assert.equal(rootMargin.source.kind, 'declaration');
});

test('own declaration overrides the inherited value at any depth', () => {
  const { engine, nodes } = makeEngine(`
    .app { color: red; }
    p { color: blue; }
  `);
  const snap = engine.getSnapshot();
  assert.equal(snap.get(nodes[1].id, 'color')!.value, 'red');
  const leaf = snap.get(nodes[2].id, 'color')!;
  assert.equal(leaf.value, 'blue');
  assert.equal(leaf.source.kind, 'declaration');
});

test('non-inherited properties never propagate to children', () => {
  const { engine, nodes } = makeEngine(`.app { background: black; padding: 10; }`);
  const snap = engine.getSnapshot();
  assert.equal(snap.get(nodes[1].id, 'background')!.value, 'transparent');
  assert.equal(snap.get(nodes[2].id, 'padding')!.value, '0');
});

test('invalid-at-computed-value-time on an inherited property inherits, on others resets to initial', () => {
  const { engine, nodes } = makeEngine(`
    .app { color: green; }
    section { color: var(--nope); margin: var(--nope); }
  `);
  const snap = engine.getSnapshot();
  const color = snap.get(nodes[1].id, 'color')!;
  assert.equal(color.value, 'green');
  assert.equal(color.source.kind, 'invalid');
  if (color.source.kind === 'invalid') assert.equal(color.source.unsetTo, 'inherited');
  const margin = snap.get(nodes[1].id, 'margin')!;
  assert.equal(margin.value, '0');
  assert.equal(margin.source.kind, 'invalid');
  if (margin.source.kind === 'invalid') assert.equal(margin.source.unsetTo, 'initial');
});
