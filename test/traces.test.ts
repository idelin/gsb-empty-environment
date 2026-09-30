import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { generateTraces } from '../src/traceGen.ts';
import { checkTrace } from '../src/invariants.ts';
import { makeArbiter, runTrace, type SceneFixture } from '../src/harness.ts';
import { DEFAULT_THRESHOLDS } from '../src/types.ts';

const fixture = JSON.parse(readFileSync(new URL('../fixtures/scene.json', import.meta.url), 'utf8')) as SceneFixture;

test('finite trace suite satisfies all invariants', () => {
  const traces = generateTraces(DEFAULT_THRESHOLDS);
  assert.ok(traces.length >= 25, 'trace generator should enumerate a non-trivial suite');
  const allFailures = [];
  for (const trace of traces) {
    const arbiter = makeArbiter(fixture);
    const run = runTrace(arbiter, trace);
    allFailures.push(...checkTrace(trace.name, trace.ops, run.steps));
  }
  assert.deepEqual(allFailures, []);
});

test('checker is independent: it catches a doctored double-commit log', () => {
  const fake = [{
    stateBefore: 'tracking', stateAfter: 'tracking',
    outputs: [
      { kind: 'drag', phase: 'commit', interactionId: 1, generation: 1, pointerIds: [1], targetId: null, x: 0, y: 0, dx: 0, dy: 0, scale: 1, time: 0 },
      { kind: 'pinch', phase: 'commit', interactionId: 1, generation: 2, pointerIds: [1, 2], targetId: null, x: 0, y: 0, dx: 0, dy: 0, scale: 1, time: 1 },
    ],
  }] as const;
  const ops = [{ kind: 'event', event: { seq: 0, time: 0, pointerId: 1, type: 'pointerdown', x: 0, y: 0, space: 'root' } }] as const;
  const failures = checkTrace('doctored', [...ops], [...fake] as never);
  assert.ok(failures.some((f) => f.rule === 'exclusive-commit'));
  assert.ok(failures.some((f) => f.rule === 'unopened-close'));
});
