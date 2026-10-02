import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  CircularSignalUpdate,
  createReactiveSystem,
} from '../src/index.ts';
import type { Computed } from '../src/index.ts';

test('acceptance: effect reading and writing signal A is blocked at iteration 50', () => {
  const sys = createReactiveSystem({ maxIterations: 50 });
  const A = new sys.Signal(0, 'A');
  let runs = 0;
  let caught: unknown;
  try {
    new sys.Effect(() => {
      runs += 1;
      A.value = A.value + 1;
    }, 'effect-A');
  } catch (err) {
    caught = err;
  }

  assert.ok(caught instanceof CircularSignalUpdate, 'must throw CircularSignalUpdate');
  const report = caught.report;
  assert.equal(report.reason, 'iteration-threshold');
  assert.equal(report.iterations, 50, 'blocked exactly at the 50th iteration');
  assert.equal(report.threshold, 50);
  assert.equal(runs, 50, '1 initial run + 49 flushed runs; the 50th is blocked');
  assert.equal(report.triggerSource, 'effect-A');
  assert.deepEqual([...report.cycle], ['effect-A', 'A', 'effect-A']);
  assert.match(caught.message, /effect-A/);
  assert.match(caught.message, /\bA\b/);
  assert.match(caught.message, /iteration-threshold/);
});

test('indirect loop across two effects is blocked and reports the full cycle', () => {
  const sys = createReactiveSystem({ maxIterations: 50 });
  const A = new sys.Signal(0, 'A');
  const B = new sys.Signal(0, 'B');

  new sys.Effect(() => {
    B.value = A.value + 1;
  }, 'effect-1');

  let caught: unknown;
  try {
    new sys.Effect(() => {
      A.value = B.value;
    }, 'effect-2');
  } catch (err) {
    caught = err;
  }

  assert.ok(caught instanceof CircularSignalUpdate);
  const report = caught.report;
  assert.equal(report.reason, 'iteration-threshold');
  assert.equal(report.iterations, 50);
  const cycle = [...report.cycle];
  assert.equal(cycle[0], cycle[cycle.length - 1], 'cycle path is closed');
  for (const label of ['effect-1', 'effect-2', 'A', 'B']) {
    assert.ok(cycle.includes(label), `cycle should contain ${label}`);
  }
  assert.ok(report.chain.length > 0, 'causal chain is recorded');
});

test('computed cycle is blocked immediately via call-stack detection', () => {
  const sys = createReactiveSystem();
  const C1: Computed<number> = new sys.Computed(() => C2.value + 1, 'C1');
  const C2: Computed<number> = new sys.Computed(() => C1.value + 1, 'C2');

  assert.throws(
    () => C1.value,
    (err: unknown) => {
      assert.ok(err instanceof CircularSignalUpdate);
      assert.equal(err.report.reason, 'call-stack-cycle');
      assert.deepEqual([...err.report.cycle], ['C1', 'C2', 'C1']);
      return true;
    },
  );
});

test('computed writing to its own dependency is blocked at the threshold', () => {
  const sys = createReactiveSystem({ maxIterations: 50 });
  const A = new sys.Signal(1, 'A');
  let computes = 0;
  const C = new sys.Computed(() => {
    computes += 1;
    const v = A.value;
    A.value = v + 1;
    return v;
  }, 'C');

  let caught: unknown;
  try {
    new sys.Effect(() => {
      void C.value;
    }, 'consumer');
  } catch (err) {
    caught = err;
  }

  assert.ok(caught instanceof CircularSignalUpdate);
  assert.equal(caught.report.reason, 'iteration-threshold');
  assert.equal(caught.report.iterations, 50);
  assert.equal(computes, 50);
  assert.match(caught.message, /\bC\b/);
});

test('benign bounded updates pass under the threshold', () => {
  const sys = createReactiveSystem({ maxIterations: 50 });
  const count = new sys.Signal(0, 'count');
  const doubled = new sys.Computed(() => count.value * 2, 'doubled');
  const seen: number[] = [];
  const logger = new sys.Effect(() => {
    seen.push(doubled.value);
  }, 'logger');

  count.value = 1;
  count.value = 2;
  count.value = 3;

  assert.deepEqual(seen, [0, 2, 4, 6]);
  logger.dispose();
  count.value = 4;
  assert.deepEqual(seen, [0, 2, 4, 6], 'disposed effect no longer reacts');
});

test('guard recovers after an interception and the system stays usable', () => {
  const sys = createReactiveSystem({ maxIterations: 10 });
  const A = new sys.Signal(0, 'A');

  assert.throws(
    () => {
      new sys.Effect(() => {
        A.value = A.value + 1;
      }, 'loop');
    },
    CircularSignalUpdate,
  );

  const seen: number[] = [];
  new sys.Effect(() => {
    seen.push(A.value);
  }, 'ok');
  A.value = 100;
  assert.equal(seen.at(-1), 100);
});
