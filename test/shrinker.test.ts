import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { shrink, ShrinkError } from '../src/shrinker.ts';
import { ReplayLog } from '../src/replay-log.ts';
import { removeStep, type Script } from '../src/model.ts';
import { validateScript } from '../src/validate.ts';
import { replay } from '../src/simulator.ts';

const example = JSON.parse(
  readFileSync(new URL('../examples/failing-script.json', import.meta.url), 'utf8'),
) as Script;

function shrinkExample() {
  const log = new ReplayLog();
  const result = shrink(example, log);
  return { log, result };
}

test('shrunk script still reproduces the failure', () => {
  const { result } = shrinkExample();
  assert.deepEqual(validateScript(result.script), []);
  assert.equal(replay(result.script).outcome, 'failed');
});

test('shrunk script is 1-minimal: no single-step deletion fails', () => {
  const { result } = shrinkExample();
  for (const step of result.script.steps) {
    const candidate = removeStep(result.script, step.id);
    if (validateScript(candidate).length > 0) continue; // not executable
    assert.notEqual(
      replay(candidate).outcome,
      'failed',
      `deleting "${step.id}" should no longer fail`,
    );
  }
});

test('dependency chains survive shrinking', () => {
  const { result } = shrinkExample();
  const ids = result.script.steps.map((s) => s.id);
  assert.deepEqual(ids, ['warmup', 'queue', 'restart-a', 'cooldown']);
});

test('every replay is summarized and invalid candidates are never accepted', () => {
  const { log, result } = shrinkExample();
  assert.equal(log.entries.length, result.replayCount);
  assert.ok(log.entries.some((e) => e.kind === 'initial'));
  assert.ok(log.entries.some((e) => e.kind === 'final'));
  assert.ok(log.entries.some((e) => e.kind === 'verify'));

  const invalid = log.entries.filter((e) => !e.valid);
  assert.ok(invalid.length > 0, 'expected some deletions to be rejected as not executable');
  for (const entry of invalid) {
    assert.equal(entry.outcome, 'not-replayed');
    assert.equal(entry.accepted, false);
    assert.ok(entry.validationErrors.length > 0);
  }
  for (const entry of log.entries.filter((e) => e.accepted)) {
    assert.equal(entry.valid, true);
    assert.equal(entry.outcome, 'failed');
  }
});

test('sequence numbers are unique and increasing', () => {
  const { log } = shrinkExample();
  log.entries.forEach((entry, index) => assert.equal(entry.seq, index + 1));
});

test('refuses to shrink a script that does not fail', () => {
  const passing: Script = {
    nodes: ['A', 'B'],
    steps: [{ id: 's1', type: 'delay', ms: 2 }],
  };
  assert.throws(() => shrink(passing, new ReplayLog()), ShrinkError);
});

test('refuses to shrink an invalid script', () => {
  const invalid = {
    nodes: ['A', 'B'],
    steps: [{ id: 's1', type: 'restart', node: 'Z' }],
  } as unknown as Script;
  assert.throws(() => shrink(invalid, new ReplayLog()), /not executable/);
});
