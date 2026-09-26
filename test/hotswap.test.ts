import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createExecutor } from '../src/executor.ts';
import { editorSpec, type EditorState } from '../src/editorSpec.ts';
import { stateHash, type Spec } from '../src/spec.ts';

const setup = () => {
  const exec = createExecutor(editorSpec);
  exec.dispatch({ type: 'MOVE', payload: { to: 4 } });
  exec.dispatch({ type: 'TYPE', payload: { ch: 'a' } });
  return exec;
};

test('migration that throws keeps the old spec and state', () => {
  const exec = setup();
  const before = stateHash(exec.getState());
  const v2: Spec<EditorState> = { ...editorSpec, name: 'editor-v2' };
  const r = exec.hotSwap(v2, () => {
    throw new Error('cannot map legacy field');
  });
  assert.equal(r.ok, false);
  assert.match(r.error!, /cannot map legacy field/);
  assert.equal(exec.getSpec().name, 'editor-v1');
  assert.equal(stateHash(exec.getState()), before);
});

test('migration evidence failure keeps the old spec and state', () => {
  const exec = setup();
  const before = stateHash(exec.getState());
  const v2: Spec<EditorState> = {
    ...editorSpec,
    name: 'editor-v2-strict',
    invariants: [
      ...editorSpec.invariants,
      { name: 'cursor-must-be-zero', check: (s) => (s.cursor === 0 ? null : 'cursor must be 0') },
    ],
  };
  const r = exec.hotSwap(v2, (s) => s); // migrate keeps cursor=5 -> evidence fails
  assert.equal(r.ok, false);
  assert.equal(r.error, 'migration evidence failed');
  const failed = r.evidence.invariants.find((i) => !i.ok);
  assert.equal(failed?.name, 'cursor-must-be-zero');
  assert.equal(exec.getSpec().name, 'editor-v1');
  assert.equal(stateHash(exec.getState()), before);
});

test('successful hot-swap installs the new spec with migrated state', () => {
  const exec = setup();
  const v2: Spec<EditorState> = { ...editorSpec, name: 'editor-v2' };
  const r = exec.hotSwap(v2, (s) => ({ ...s, cursor: 0, anchor: null }));
  assert.equal(r.ok, true);
  assert.equal(r.evidence.modeValid, true);
  assert.ok(r.evidence.invariants.every((i) => i.ok));
  assert.equal(exec.getSpec().name, 'editor-v2');
  assert.equal(exec.getState().cursor, 0);
  // executor keeps working under the new spec
  assert.equal(exec.dispatch({ type: 'TYPE', payload: { ch: 'b' } }).accepted, true);
});

test('migration to a mode unknown to the new spec is rejected', () => {
  const exec = setup();
  exec.dispatch({ type: 'ENTER', payload: { mode: 'readonly' } });
  const before = stateHash(exec.getState());
  const v2: Spec<EditorState> = {
    ...editorSpec,
    name: 'editor-v2-no-readonly',
    modeRoot: { name: 'app', children: [{ name: 'active', children: [{ name: 'insert' }] }] },
  };
  const r = exec.hotSwap(v2, (s) => s);
  assert.equal(r.ok, false);
  assert.equal(r.evidence.modeValid, false);
  assert.equal(exec.getSpec().name, 'editor-v1');
  assert.equal(stateHash(exec.getState()), before);
});
