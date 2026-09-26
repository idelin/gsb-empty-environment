import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildDocument, flatten } from '../src/engine/model.ts';
import { StyleEngine } from '../src/engine/engine.ts';

function makeEngine(css: string) {
  const root = buildDocument({
    tag: 'div', id: 'root', classes: ['app'], children: [
      { tag: 'p', classes: ['text', 'lead'], text: 'hello' },
    ],
  });
  const nodes = flatten(root);
  return { engine: new StyleEngine(root, css), nodes };
}

test('higher specificity wins even when it appears earlier', () => {
  const { engine, nodes } = makeEngine(`
    #root .text { color: red; }
    p.lead { color: blue; }
  `);
  const snap = engine.getSnapshot();
  const cv = snap.get(nodes[1].id, 'color')!;
  assert.equal(cv.value, 'red');
  assert.deepEqual(cv.chain.map((c) => c.outcome), ['lost-specificity', 'won']);
});

test('equal specificity: later source order wins, earlier loses with lost-order', () => {
  const { engine, nodes } = makeEngine(`
    .text { color: red; }
    .lead { color: green; }
    .text.lead { color: blue; }
    .text.lead { color: purple; }
  `);
  const snap = engine.getSnapshot();
  const cv = snap.get(nodes[1].id, 'color')!;
  assert.equal(cv.value, 'purple');
  assert.deepEqual(
    cv.chain.map((c) => [c.selectorText, c.outcome]),
    [
      ['.text', 'lost-specificity'],
      ['.lead', 'lost-specificity'],
      ['.text.lead', 'lost-order'],
      ['.text.lead', 'won'],
    ],
  );
});

test('inline declarations outrank selector declarations', () => {
  const { engine, nodes } = makeEngine(`#root .text.lead { color: red; }`);
  engine.setInline(nodes[1].id, 'color', 'teal');
  const cv = engine.getSnapshot().get(nodes[1].id, 'color')!;
  assert.equal(cv.value, 'teal');
  assert.deepEqual(cv.chain.map((c) => c.outcome), ['lost-specificity', 'won']);
});

test('source chain records every candidate with specificity and raw value', () => {
  const { engine, nodes } = makeEngine(`
    p { margin: 4; }
    .text { margin: 8; }
  `);
  const cv = engine.getSnapshot().get(nodes[1].id, 'margin')!;
  assert.equal(cv.value, '8');
  assert.equal(cv.chain.length, 2);
  assert.deepEqual(cv.chain[0].specificity, [0, 0, 1]);
  assert.deepEqual(cv.chain[1].specificity, [0, 1, 0]);
  assert.equal(cv.source.kind, 'declaration');
  if (cv.source.kind === 'declaration') assert.equal(cv.source.selectorText, '.text');
});

test('conditional selectors only apply while the state is active', () => {
  const { engine, nodes } = makeEngine(`
    .text { color: black; }
    .text:hover { color: red; }
  `);
  assert.equal(engine.getSnapshot().get(nodes[1].id, 'color')!.value, 'black');
  engine.toggleState(nodes[1].id, 'hover');
  assert.equal(engine.getSnapshot().get(nodes[1].id, 'color')!.value, 'red');
  engine.toggleState(nodes[1].id, 'hover');
  assert.equal(engine.getSnapshot().get(nodes[1].id, 'color')!.value, 'black');
});
