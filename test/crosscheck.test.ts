import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createExecutor } from '../src/executor.ts';
import { crossValidate } from '../src/crosscheck.ts';
import { editorSpec } from '../src/editorSpec.ts';
import type { Event } from '../src/spec.ts';

const script: Event[] = [
  { type: 'MOVE', payload: { to: 3 } },
  { type: 'TYPE', payload: { ch: 'a' } },
  { type: 'BACKSPACE' },
  { type: 'FOCUS', payload: { target: 'p2' } },
  { type: 'TYPE', payload: { ch: 'b' } },
  { type: 'RESIZE', payload: { width: 40 } },
  { type: 'ENTER', payload: { mode: 'active.select' } },
  { type: 'SELECT', payload: { to: 2 } },
];

test('executor trace cross-validates against checker one-step semantics', () => {
  const exec = createExecutor(editorSpec);
  for (const e of script) assert.equal(exec.dispatch(e).accepted, true);
  const report = crossValidate(editorSpec, exec.getTrace());
  assert.equal(report.ok, true, JSON.stringify(report.mismatches, null, 2));
  assert.equal(report.steps, script.length);
});

test('cross-validation detects a tampered trace', () => {
  const exec = createExecutor(editorSpec);
  for (const e of script) exec.dispatch(e);
  const trace = exec.getTrace().map((t, i) =>
    i === 2 ? { ...t, stateHash: 'deadbeef' } : t,
  );
  const report = crossValidate(editorSpec, trace);
  assert.equal(report.ok, false);
  assert.equal(report.mismatches.length >= 1, true);
  assert.equal(report.mismatches[0].seq, trace[2].seq);
});
