import assert from 'node:assert/strict';
import test from 'node:test';
import { PolicyEngine } from '../src/engine.ts';
import { and, eq, gt, inSet, lit, not, or } from '../src/expr.ts';
import { InMemoryAttrStore } from '../src/store.ts';

function setup() {
  const store = new InMemoryAttrStore();
  const engine = new PolicyEngine(store).bindStore(store);
  return { store, engine };
}

test('x OR NOT x stays unknown when x is missing (never simplified to false)', async () => {
  const { engine } = setup();
  // Excluded middle does NOT hold once attributes can be missing.
  engine.setRule('excluded-middle', or(inSet('x', [1, 2]), not(inSet('x', [1, 2]))));
  const d = await engine.evaluate('excluded-middle', 'subject-without-x');
  assert.equal(d.status, 'ok');
  assert.equal(d.decision, 'unknown');
  assert.notEqual(d.decision, 'false');
});

test('unknown AND true is unknown, not false', async () => {
  const { engine } = setup();
  engine.setRule('r', and(gt('x', 3), lit('true')));
  const d = await engine.evaluate('r', 's');
  assert.equal(d.decision, 'unknown');
});

test('unknown OR false is unknown, not false', async () => {
  const { engine } = setup();
  engine.setRule('r', or(gt('x', 3), lit('false')));
  const d = await engine.evaluate('r', 's');
  assert.equal(d.decision, 'unknown');
});

test('non-integer attribute values are unknown, not false', async () => {
  const { store, engine } = setup();
  store.set('s', 'x', 'not-a-number');
  engine.setRule('r', and(gt('x', 3), eq('x', 7)));
  const d = await engine.evaluate('r', 's');
  assert.equal(d.decision, 'unknown');
});

test('partial evaluation folds known attributes and keeps the residual condition', async () => {
  const { store, engine } = setup();
  engine.setRule('r', and(gt('x', 3), inSet('y', [1, 2]), eq('z', 9)));
  const known = new Map<string, unknown>([
    ['x', 5],
    ['z', 0], // known and falsy -> that conjunct collapses to false
  ]);
  const r1 = engine.residual('r', known);
  assert.equal(r1.text, 'false'); // z == 9 is false, false dominates AND

  const r2 = engine.residual('r', new Map([['x', 5]]));
  assert.equal(r2.text, '(y in {1, 2} AND z == 9)');

  // x known-missing -> the x conjunct is unknown and stays explicit.
  const r3 = engine.residual('r', new Map<string, unknown>([['x', undefined]]));
  assert.equal(r3.text, '(unknown AND y in {1, 2} AND z == 9)');
});
