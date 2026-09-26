import { test } from 'node:test';
import assert from 'node:assert/strict';
import { check, inputDomain, step } from '../src/checker.ts';
import { editorSpec, type EditorState } from '../src/editorSpec.ts';
import type { Spec } from '../src/spec.ts';

test('checker verifies invariants and unreachable states over the finite domain', () => {
  const report = check(editorSpec, { maxDepth: 5 });
  assert.equal(report.ok, true, JSON.stringify(report.counterexample, null, 2));
  assert.ok(report.exploredStates > 1000);
});

test('checker returns a shortest counterexample for a broken spec', () => {
  // break the spec: entering a dialog no longer clears the selection
  const broken: Spec<EditorState> = {
    ...editorSpec,
    events: editorSpec.events.map((def) =>
      def.name === 'ENTER'
        ? { ...def, action: (s, e) => ({ ...s, mode: (e.payload as { mode: string }).mode }) }
        : def,
    ),
  };
  const report = check(broken, { maxDepth: 6 });
  assert.equal(report.ok, false);
  const cx = report.counterexample!;
  assert.equal(cx.kind, 'unreachable-reached');
  assert.equal(cx.name, 'dialog-with-selection');
  // shortest path: ENTER select mode, SELECT, ENTER dialog => 3 events
  assert.equal(cx.trace.length, 3);
  assert.deepEqual(
    cx.trace.map((e) => e.type),
    ['ENTER', 'SELECT', 'ENTER'],
  );
});

test('checker finds invariant violations with minimal traces', () => {
  const broken: Spec<EditorState> = {
    ...editorSpec,
    events: editorSpec.events.map((def) =>
      def.name === 'MOVE' ? { ...def, guard: undefined } : def,
    ),
  };
  const report = check(broken, { maxDepth: 4 });
  assert.equal(report.ok, false);
  const cx = report.counterexample!;
  assert.equal(cx.kind, 'invariant-violated');
  assert.equal(cx.name, 'cursor-in-bounds');
  // shortest: focus the empty paragraph p3, then MOVE past its end
  assert.equal(cx.trace.length, 2);
  assert.deepEqual(
    cx.trace.map((e) => e.type),
    ['FOCUS', 'MOVE'],
  );
});

test('one-step semantics rejects illegal events without throwing', () => {
  const s = editorSpec.initial();
  assert.equal(step(editorSpec, s, { type: 'NOPE' }).ok, false);
  assert.equal(step(editorSpec, s, { type: 'TYPE', payload: { ch: '中' } }).ok, false);
  const ok = step(editorSpec, s, { type: 'TYPE', payload: { ch: 'a' } });
  assert.equal(ok.ok, true);
});

test('input domain is finite and covers every event', () => {
  const domain = inputDomain(editorSpec);
  const types = new Set(domain.map((e) => e.type));
  for (const def of editorSpec.events) assert.ok(types.has(def.name));
  assert.ok(domain.length < 100);
});
