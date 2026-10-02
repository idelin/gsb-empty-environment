import { test } from 'node:test';
import assert from 'node:assert/strict';
import { GestureRecognizer } from '../src/recognizer.ts';
import { generateTraces } from '../src/tracegen.ts';
import { checkTrace } from '../src/checker.ts';
import type { GestureOutput } from '../src/types.ts';

test('finite trace generator: every enumerated trace satisfies the spec invariants', () => {
  const traces = generateTraces();
  assert.ok(traces.length >= 40, `expected a meaningful enumeration, got ${traces.length}`);

  const allViolations: string[] = [];
  for (const trace of traces) {
    allViolations.push(...checkTrace(trace, new GestureRecognizer()));
  }
  assert.deepEqual(allViolations, []);
});

test('generator coverage: enumeration exercises commits, compensation and ignored events', () => {
  const traces = generateTraces();
  const seen = { click: 0, drag: 0, pinch: 0, compensate: 0, ignored: 0, cancel: 0 };
  for (const trace of traces) {
    const rec = new GestureRecognizer();
    for (const ev of trace.events) {
      for (const o of rec.handle(ev) as GestureOutput[]) {
        if (o.kind === 'commit') seen[o.gesture] += 1;
        if (o.kind === 'compensate') seen.compensate += 1;
        if (o.kind === 'ignored') seen.ignored += 1;
        if (o.kind === 'cancel') seen.cancel += 1;
      }
    }
  }
  assert.ok(seen.click > 0, 'no click commits generated');
  assert.ok(seen.drag > 0, 'no drag commits generated');
  assert.ok(seen.pinch > 0, 'no pinch commits generated');
  assert.ok(seen.compensate > 0, 'no compensation generated');
  assert.ok(seen.ignored > 0, 'no ignored events generated');
  assert.ok(seen.cancel > 0, 'no cancels generated');
});

test('commit counting: across all traces, no generation ever commits twice', () => {
  const traces = generateTraces();
  for (const trace of traces) {
    const rec = new GestureRecognizer();
    const commitsPerGen = new Map<number, number>();
    for (const ev of trace.events) {
      for (const o of rec.handle(ev)) {
        if (o.kind === 'commit') {
          commitsPerGen.set(o.gen, (commitsPerGen.get(o.gen) ?? 0) + 1);
        }
      }
    }
    for (const [gen, count] of commitsPerGen) {
      assert.ok(count <= 1, `${trace.name}: generation ${gen} committed ${count} times`);
    }
  }
});
