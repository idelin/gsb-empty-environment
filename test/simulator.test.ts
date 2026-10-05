import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { replay } from '../src/simulator.ts';
import type { Script } from '../src/model.ts';

const example = JSON.parse(
  readFileSync(new URL('../examples/failing-script.json', import.meta.url), 'utf8'),
) as Script;

test('example script triggers the stale-epoch assertion', () => {
  const result = replay(example);
  assert.equal(result.outcome, 'failed');
  if (result.outcome === 'failed') {
    assert.equal(result.failure.stepId, 'cooldown');
    assert.match(result.failure.message, /stale heartbeat B->A/);
  }
});

test('removing the restart makes the script pass', () => {
  const script: Script = {
    ...example,
    steps: example.steps.filter((s) => s.id !== 'restart-a'),
  };
  assert.equal(replay(script).outcome, 'passed');
});

test('dropping the critical in-flight heartbeat masks the failure', () => {
  const script: Script = {
    ...example,
    steps: example.steps.flatMap((s) =>
      s.id === 'cooldown'
        ? [{ id: 'shield', type: 'drop', from: 'B', to: 'A', count: 1 } as const, s]
        : [s],
    ),
  };
  assert.equal(replay(script).outcome, 'passed');
});

test('replay is deterministic', () => {
  assert.deepEqual(replay(example), replay(example));
});
