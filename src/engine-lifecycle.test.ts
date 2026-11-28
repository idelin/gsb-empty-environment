import { describe, test } from 'node:test';
import assert from 'node:assert/strict';

import { IndexedJobEngine, validateSpec } from './engine.js';
import type {
  ContainerState,
  IndexedJobSpec,
  PodFailureInfo,
} from './types.js';

const baseSpec = (overrides: Partial<IndexedJobSpec> = {}): IndexedJobSpec => ({
  completions: 5,
  parallelism: 2,
  backoffLimitPerIndex: 2,
  maxFailedIndexes: 1,
  backoffLimit: 10,
  ...overrides,
});

const makeFailure = (
  index: number,
  podName: string,
  exitCode: number,
): PodFailureInfo => ({
  index,
  podName,
  containers: [{ name: 'app', exitCode } satisfies ContainerState],
  conditions: [],
});

describe('IndexedJobEngine — scheduling and concurrency backfill', () => {
  test('smallest eligible indexes win the parallelism slots', () => {
    const engine = new IndexedJobEngine({
      spec: baseSpec({ completions: 4, parallelism: 2 }),
    });
    const first = engine.schedule();
    assert.deepEqual(first.map((pod) => pod.index), [0, 1]);

    // Completing index 0 frees a slot; the smallest pending index (2) fills it.
    engine.reportPodSuccess({ index: 0, podName: first[0].podName });
    const second = engine.schedule();
    assert.deepEqual(second.map((pod) => pod.index), [2]);

    engine.reportPodSuccess({ index: 1, podName: first[1].podName });
    const third = engine.schedule();
    assert.deepEqual(third.map((pod) => pod.index), [3]);
  });

  test('succeeded and failed indexes are never rescheduled', () => {
    const engine = new IndexedJobEngine({
      spec: baseSpec({
        completions: 3,
        parallelism: 2,
        backoffLimitPerIndex: 0,
        maxFailedIndexes: 2,
      }),
    });
    const wave = engine.schedule();
    assert.deepEqual(wave.map((pod) => pod.index), [0, 1]);
    engine.reportPodSuccess({ index: 0, podName: wave[0].podName });
    engine.reportPodFailure(makeFailure(1, wave[1].podName, 1));

    // Only index 2 remains, and it takes the slot freed by index 0.
    assert.deepEqual(engine.schedule().map((pod) => pod.index), [2]);
  });

  test('never schedules more pods than parallelism allows', () => {
    const engine = new IndexedJobEngine({
      spec: baseSpec({ completions: 10, parallelism: 3 }),
    });
    assert.equal(engine.schedule().length, 3);
    assert.deepEqual(engine.schedule(), []);
  });
});

describe('IndexedJobEngine — global terminal conditions', () => {
  test('Job fails when failed indexes strictly exceed maxFailedIndexes', () => {
    const engine = new IndexedJobEngine({
      spec: baseSpec({
        completions: 4,
        parallelism: 4,
        backoffLimitPerIndex: 0,
        maxFailedIndexes: 1,
      }),
    });
    const wave = engine.schedule();

    // First failed index is tolerated because 1 <= maxFailedIndexes.
    const first = engine.reportPodFailure(makeFailure(0, wave[0].podName, 1));
    assert.equal(first.jobCondition, 'Running');
    assert.equal(engine.getFailedIndexCount(), 1);

    // Second failed index: 2 > 1, so the Job terminates immediately.
    const second = engine.reportPodFailure(makeFailure(1, wave[1].podName, 1));
    assert.equal(second.jobCondition, 'Failed');
    assert.equal(second.failureReason, 'MaxFailedIndexesExceeded');
    assert.deepEqual(
      [...second.terminatedPods].sort(),
      [wave[2].podName, wave[3].podName].sort(),
    );
    assert.equal(engine.getActivePodCount(), 0);
  });

  test('Job fails when total counted failures exceed the global backoffLimit', () => {
    const engine = new IndexedJobEngine({
      spec: baseSpec({
        completions: 4,
        parallelism: 4,
        backoffLimitPerIndex: 100,
        maxFailedIndexes: 100,
        backoffLimit: 2,
      }),
    });
    const wave = engine.schedule();

    engine.reportPodFailure(makeFailure(0, wave[0].podName, 1));
    const second = engine.reportPodFailure(makeFailure(1, wave[1].podName, 1));
    const third = engine.reportPodFailure(makeFailure(2, wave[2].podName, 1));

    assert.equal(second.jobCondition, 'Running');
    assert.equal(third.jobCondition, 'Failed');
    assert.equal(third.failureReason, 'BackoffLimitExceeded');
    assert.equal(engine.getTotalCountedFailures(), 3);
  });

  test('Job completes when all indexes terminate within the failure tolerance', () => {
    const engine = new IndexedJobEngine({
      spec: baseSpec({
        completions: 3,
        parallelism: 3,
        backoffLimitPerIndex: 0,
        maxFailedIndexes: 1,
      }),
    });
    const wave = engine.schedule();

    engine.reportPodFailure(makeFailure(0, wave[0].podName, 1));
    engine.reportPodSuccess({ index: 1, podName: wave[1].podName });
    assert.equal(engine.getJobCondition(), 'Running');

    const finalOutcome = engine.reportPodSuccess({
      index: 2,
      podName: wave[2].podName,
    });
    assert.equal(finalOutcome.jobCondition, 'Complete');
    assert.equal(engine.getJobCondition(), 'Complete');
    assert.equal(engine.getFailedIndexCount(), 1);
  });
});

describe('IndexedJobEngine — status snapshots and range strings', () => {
  test('exposes canonical completed and failed index ranges', () => {
    const engine = new IndexedJobEngine({
      spec: baseSpec({
        completions: 12,
        parallelism: 12,
        backoffLimitPerIndex: 0,
        maxFailedIndexes: 3,
      }),
    });
    const wave = engine.schedule();
    const podFor = (index: number): string =>
      wave.find((pod) => pod.index === index)?.podName ?? '';

    for (const failedIndex of [4, 6, 7]) {
      engine.reportPodFailure(makeFailure(failedIndex, podFor(failedIndex), 1));
    }
    for (const succeededIndex of [0, 1, 2, 3, 5, 8, 9, 10, 11]) {
      engine.reportPodSuccess({ index: succeededIndex, podName: podFor(succeededIndex) });
    }

    assert.equal(engine.getCompletedIndexRanges(), '0-3,5,8-11');
    assert.equal(engine.getFailedIndexRanges(), '4,6-7');
    assert.equal(engine.getJobCondition(), 'Complete');
  });
});

describe('IndexedJobEngine — validation', () => {
  test('rejects invalid specs and out-of-range indexes', () => {
    assert.throws(() => validateSpec(baseSpec({ completions: 0 })));
    assert.throws(() => validateSpec(baseSpec({ parallelism: 0 })));
    assert.throws(() => validateSpec(baseSpec({ backoffLimitPerIndex: -1 })));
    assert.throws(() => validateSpec(baseSpec({ maxFailedIndexes: -1 })));

    const engine = new IndexedJobEngine({ spec: baseSpec({ completions: 3 }) });
    assert.throws(() => engine.getIndexState(3), RangeError);
    assert.throws(() => engine.getIndexState(-1), RangeError);
  });
});

describe('IndexedJobEngine — strict greater-than boundary semantics', () => {
  test('a failed-index count equal to maxFailedIndexes keeps the Job Running', () => {
    const engine = new IndexedJobEngine({
      spec: baseSpec({
        completions: 3,
        parallelism: 3,
        backoffLimitPerIndex: 0,
        maxFailedIndexes: 2,
      }),
    });
    const wave = engine.schedule();
    engine.reportPodFailure(makeFailure(0, wave[0].podName, 1));
    const second = engine.reportPodFailure(makeFailure(1, wave[1].podName, 1));

    assert.equal(engine.getFailedIndexCount(), 2);
    assert.equal(second.jobCondition, 'Running');

    engine.reportPodSuccess({ index: 2, podName: wave[2].podName });
    assert.equal(engine.getJobCondition(), 'Complete');
  });

  test('a counted-failure total equal to backoffLimit keeps the Job Running', () => {
    const engine = new IndexedJobEngine({
      spec: baseSpec({
        completions: 2,
        parallelism: 2,
        backoffLimitPerIndex: 100,
        maxFailedIndexes: 100,
        backoffLimit: 1,
      }),
    });
    const wave = engine.schedule();
    const first = engine.reportPodFailure(makeFailure(0, wave[0].podName, 1));
    assert.equal(first.jobCondition, 'Running');
    assert.equal(engine.getTotalCountedFailures(), 1);
  });

  test('every retry gets a unique pod name while attempts accumulate', () => {
    const engine = new IndexedJobEngine({
      spec: baseSpec({ completions: 1, parallelism: 1, backoffLimitPerIndex: 5 }),
    });
    const names = new Set<string>();
    let pod = engine.schedule()[0];
    names.add(pod.podName);

    for (let retry = 0; retry < 2; retry += 1) {
      engine.reportPodFailure(makeFailure(0, pod.podName, 1));
      engine.getIndexState(0);
      // Backoff is in the future by default; force the deadline to pass.
      const state = engine.getIndexState(0);
      const due = state.backoffUntil;
      pod = engine.schedule(due)[0];
      names.add(pod.podName);
    }

    assert.equal(names.size, 3);
    assert.equal(engine.getIndexState(0).attempts, 3);
  });
});
