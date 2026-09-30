import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { makeArbiter, runTrace, type SceneFixture } from '../src/harness.ts';
import type { GestureEvent } from '../src/types.ts';
import type { NamedTrace, TraceOp } from '../src/traceGen.ts';

const fixture = JSON.parse(readFileSync(new URL('../fixtures/scene.json', import.meta.url), 'utf8')) as SceneFixture;

let seq = 0;
function ev(type: string, pointerId: number, x: number, y: number, time: number, space = 'root') {
  return { kind: 'event', event: { seq: seq++, time, pointerId, type, x, y, space } } as TraceOp;
}
function trace(name: string, ops: TraceOp[]): NamedTrace { return { name, ops }; }
function commits(events: GestureEvent[]) { return events.filter((e) => e.phase === 'commit'); }
function kinds(events: GestureEvent[]) { return events.map((e) => `${e.kind}:${e.phase}`); }

test('clean tap commits click with hit-tested target', () => {
  const arbiter = makeArbiter(fixture);
  const run = runTrace(arbiter, trace('tap', [ev('pointerdown', 1, 55, 60, 0), ev('pointerup', 1, 55, 60, 100)]));
  const c = commits(run.outputs);
  assert.equal(c.length, 1);
  assert.equal(c[0]!.kind, 'click');
  assert.equal(c[0]!.targetId, 'button-label');
});

test('movement at exactly the slop threshold stays a click; beyond it drags', () => {
  for (const [d, expected] of [[8, 'click'], [8.5, 'drag']] as const) {
    const arbiter = makeArbiter(fixture);
    const run = runTrace(arbiter, trace('t', [
      ev('pointerdown', 1, 50, 50, 0),
      ev('pointermove', 1, 50 + d, 50, 16),
      ev('pointerup', 1, 50 + d, 50, 32),
    ]));
    assert.equal(commits(run.outputs)[0]!.kind, expected, `d=${d}`);
  }
});

test('click turned into drag never emits click', () => {
  const arbiter = makeArbiter(fixture);
  const run = runTrace(arbiter, trace('t', [
    ev('pointerdown', 1, 50, 50, 0),
    ev('pointermove', 1, 80, 50, 16),
    ev('pointermove', 1, 50, 50, 32), // returns inside slop
    ev('pointerup', 1, 50, 50, 48),
  ]));
  assert.deepEqual(commits(run.outputs).map((e) => e.kind), ['drag']);
  assert.ok(!run.outputs.some((e) => e.kind === 'click'));
});

test('second pointer joining compensates drag preview, pinch commits as the one final gesture', () => {
  const arbiter = makeArbiter(fixture);
  const run = runTrace(arbiter, trace('t', [
    ev('pointerdown', 1, 50, 50, 0),
    ev('pointermove', 1, 70, 50, 16),   // drag preview starts
    ev('pointerdown', 2, 150, 50, 32),   // generation change
    ev('pointermove', 2, 190, 50, 48),   // pinch preview
    ev('pointerup', 2, 190, 50, 64),     // pinch commits
    ev('pointerup', 1, 70, 50, 80),
  ]));
  assert.deepEqual(kinds(run.outputs), [
    'drag:preview', 'drag:compensate', 'pinch:preview', 'pinch:commit',
  ]);
  assert.equal(commits(run.outputs).length, 1);
});

test('lostpointercapture cancels without commit and state recovers', () => {
  const arbiter = makeArbiter(fixture);
  const run = runTrace(arbiter, trace('t', [
    ev('pointerdown', 1, 50, 50, 0),
    ev('pointermove', 1, 80, 50, 16),
    ev('lostpointercapture', 1, 80, 50, 32),
  ]));
  assert.deepEqual(kinds(run.outputs), ['drag:preview', 'drag:compensate']);
  assert.equal(arbiter.state, 'idle');
  assert.equal(arbiter.activePointerCount, 0);
});

test('stale pointer is expired by tick watchdog, not kept active forever', () => {
  const arbiter = makeArbiter(fixture);
  const run = runTrace(arbiter, trace('t', [
    ev('pointerdown', 1, 50, 50, 0),
    ev('pointermove', 1, 80, 50, 16),
    { kind: 'tick', time: 6000 },
  ]));
  assert.deepEqual(kinds(run.outputs), ['drag:preview', 'drag:compensate']);
  assert.equal(arbiter.state, 'idle');
});

test('delayed events for a removed pointer are dropped', () => {
  const arbiter = makeArbiter(fixture);
  arbiter.dispatch({ seq: 0, time: 0, pointerId: 1, type: 'pointerdown', x: 50, y: 50, space: 'root' });
  arbiter.dispatch({ seq: 1, time: 16, pointerId: 1, type: 'pointerup', x: 50, y: 50, space: 'root' });
  const late = arbiter.dispatch({ seq: 2, time: 32, pointerId: 1, type: 'pointermove', x: 500, y: 500, space: 'root' });
  assert.equal(late.dropped, true);
  assert.equal(late.events.length, 0);
});

test('convertible space change keeps the gesture; drift measured in canonical space', () => {
  const arbiter = makeArbiter(fixture);
  const run = runTrace(arbiter, trace('t', [
    ev('pointerdown', 1, 50, 50, 0),
    ev('pointermove', 1, 40, 30, 16, 'viewport'), // == (50,50) in root: no drift
    ev('pointerup', 1, 40, 30, 32, 'viewport'),
  ]));
  assert.deepEqual(commits(run.outputs).map((e) => e.kind), ['click']);
});

test('unconvertible space change re-anchors and prevents commit', () => {
  const arbiter = makeArbiter(fixture);
  const run = runTrace(arbiter, trace('t', [
    ev('pointerdown', 1, 50, 50, 0),
    ev('pointermove', 1, 80, 50, 16),             // drag preview
    ev('pointermove', 1, 0, 0, 32, 'nowhere'),    // unknown space
    ev('pointerup', 1, 0, 0, 48, 'nowhere'),
  ]));
  assert.deepEqual(kinds(run.outputs), ['drag:preview', 'drag:compensate']);
});

test('at most one final commit even across generations', () => {
  const arbiter = makeArbiter(fixture);
  const run = runTrace(arbiter, trace('t', [
    ev('pointerdown', 1, 100, 100, 0),
    ev('pointerdown', 2, 200, 100, 16),
    ev('pointermove', 2, 300, 100, 32),  // pinch preview
    ev('pointerup', 2, 300, 100, 48),    // pinch commits
    ev('pointermove', 1, 200, 100, 64),  // would-be drag
    ev('pointerup', 1, 200, 100, 80),
  ]));
  assert.deepEqual(commits(run.outputs).map((e) => e.kind), ['pinch']);
});
