import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildDocument, flatten } from '../src/engine/model.ts';
import { StyleEngine } from '../src/engine/engine.ts';

function makeEngine(css: string) {
  const root = buildDocument({
    tag: 'div', classes: ['app'], children: [
      { tag: 'section', classes: ['card'], children: [
        { tag: 'p', classes: ['desc'], text: 'x' },
      ]},
    ],
  });
  const nodes = flatten(root);
  return { engine: new StyleEngine(root, css), nodes };
}

test('explicit variables resolve and are recorded in the var trace', () => {
  const { engine, nodes } = makeEngine(`
    .app { --accent: red; }
    .desc { color: var(--accent); }
  `);
  const cv = engine.getSnapshot().get(nodes[2].id, 'color')!;
  assert.equal(cv.value, 'red');
  assert.deepEqual(cv.vars, [{ name: '--accent', status: 'resolved', value: 'red' }]);
});

test('scoped variables: a variable set on .card shadows the outer one in its subtree only', () => {
  const { engine, nodes } = makeEngine(`
    .app { --accent: red; }
    .card { --accent: blue; }
  `);
  const snap = engine.getSnapshot();
  assert.equal(snap.get(nodes[0].id, '--accent')!.value, 'red');
  assert.equal(snap.get(nodes[1].id, '--accent')!.value, 'blue');
  const inherited = snap.get(nodes[2].id, '--accent')!;
  assert.equal(inherited.value, 'blue');
  assert.deepEqual(inherited.source, { kind: 'inherited', from: nodes[1].id });
});

test('undefined variable falls back, and the trace records the failure condition', () => {
  const { engine, nodes } = makeEngine(`.desc { color: var(--missing, green); }`);
  const cv = engine.getSnapshot().get(nodes[2].id, 'color')!;
  assert.equal(cv.value, 'green');
  assert.deepEqual(cv.vars, [{ name: '--missing', status: 'fallback', reason: 'undefined', value: 'green' }]);
});

test('nested fallback chains resolve innermost-first and keep the full path', () => {
  const { engine, nodes } = makeEngine(`
    .app { --c: navy; }
    .desc { color: var(--a, var(--b, var(--c))); }
  `);
  const cv = engine.getSnapshot().get(nodes[2].id, 'color')!;
  assert.equal(cv.value, 'navy');
  assert.deepEqual(
    cv.vars.map((v) => [v.name, v.status, v.reason ?? null]),
    [
      ['--a', 'fallback', 'undefined'],
      ['--b', 'fallback', 'undefined'],
      ['--c', 'resolved', null],
    ],
  );
});

test('cyclic variables are invalid at computed-value time; use site falls back', () => {
  const { engine, nodes } = makeEngine(`
    .app { --x: var(--y); --y: var(--x); }
    .desc { color: var(--x, orange); }
  `);
  const snap = engine.getSnapshot();
  const x = snap.get(nodes[0].id, '--x')!;
  const y = snap.get(nodes[0].id, '--y')!;
  assert.equal(x.value, null);
  assert.equal(y.value, null);
  assert.equal(x.invalidReason, 'cycle');
  assert.deepEqual(x.cyclePath, ['--x', '--y']);
  const color = snap.get(nodes[2].id, 'color')!;
  assert.equal(color.value, 'orange');
  assert.equal(color.vars[0].status, 'fallback');
  assert.equal(color.vars[0].reason, 'cycle');
  assert.deepEqual(color.vars[0].cyclePath, ['--x', '--y']);
});

test('cyclic variable without fallback makes the declaration invalid (unset), not the next candidate', () => {
  const { engine, nodes } = makeEngine(`
    .app { --self: var(--self); }
    .desc { color: red; color: var(--self); }
  `);
  const cv = engine.getSnapshot().get(nodes[2].id, 'color')!;
  // The winning declaration is invalid at computed-value time: the property is
  // unset (inherited here), and does NOT fall back to the losing declaration.
  assert.equal(cv.value, 'black'); // inherited initial at root chain: app -> card -> desc all unset
  assert.equal(cv.source.kind, 'invalid');
  if (cv.source.kind === 'invalid') assert.equal(cv.source.unsetTo, 'inherited');
  assert.deepEqual(cv.chain.map((c) => c.outcome), ['lost-order', 'invalid-at-computed-value-time']);
});

test('a variable in a cycle stays invalid even when its own use site has a fallback', () => {
  const { engine, nodes } = makeEngine(`
    .app { --a: var(--b, 3); --b: var(--a); }
    .desc { color: var(--a, pink); }
  `);
  const snap = engine.getSnapshot();
  // Both --a and --b participate in the dependency cycle, so both are invalid.
  assert.equal(snap.get(nodes[0].id, '--a')!.value, null);
  assert.equal(snap.get(nodes[0].id, '--b')!.value, null);
  assert.equal(snap.get(nodes[2].id, 'color')!.value, 'pink');
});

test('variables referencing variables compose transitively', () => {
  const { engine, nodes } = makeEngine(`
    .app { --base: 8; --double: calc-placeholder; }
    .card { --size: var(--base); }
    .desc { font-size: var(--size); }
  `);
  const cv = engine.getSnapshot().get(nodes[2].id, 'font-size')!;
  assert.equal(cv.value, '8');
});
