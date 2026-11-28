import { describe, test } from 'node:test';
import assert from 'node:assert/strict';

import { IndexedJobEngine } from './engine.js';
import type {
  ContainerState,
  IndexedJobSpec,
  PodFailureInfo,
  PodFailurePolicy,
} from './types.js';

const baseSpec = (overrides: Partial<IndexedJobSpec> = {}): IndexedJobSpec => ({
  completions: 5,
  parallelism: 2,
  backoffLimitPerIndex: 2,
  maxFailedIndexes: 1,
  backoffLimit: 10,
  ...overrides,
});

const appContainer = (exitCode: number): ContainerState => ({
  name: 'app',
  exitCode,
});

const makeFailure = (
  index: number,
  podName: string,
  exitCode: number,
  extra: Partial<PodFailureInfo> = {},
): PodFailureInfo => ({
  index,
  podName,
  containers: [appContainer(exitCode)],
  conditions: [],
  ...extra,
});

/** Virtual clock so time-dependent scheduling stays fully deterministic. */
class FakeClock {
  current = 1_000;
  now = (): number => this.current;
  advance(ms: number): void {
    this.current += ms;
  }
}

describe('IndexedJobEngine — podFailurePolicy first-match actions', () => {
  const policy: PodFailurePolicy = {
    rules: [
      {
        action: 'Ignore',
        onExitCodes: { containerName: 'app', operator: 'In', values: [99] },
      },
      {
        action: 'FailIndex',
        onPodConditions: [{ type: 'DisruptionTarget' }],
      },
      { action: 'FailJob' },
    ],
  };

  test('Ignore does not count a failure and the index is immediately retriable', () => {
    const engine = new IndexedJobEngine({ spec: baseSpec({ podFailurePolicy: policy }) });
    const [pod] = engine.schedule();

    const outcome = engine.reportPodFailure(
      makeFailure(pod.index, pod.podName, 99),
    );

    assert.equal(outcome.action, 'Ignore');
    assert.equal(outcome.matchedRuleIndex, 0);
    assert.equal(outcome.counted, false);
    assert.equal(engine.getIndexState(0).failures, 0);
    assert.equal(engine.getTotalCountedFailures(), 0);
    assert.equal(engine.getIndexState(0).backoffUntil, 0);
    assert.equal(engine.getJobCondition(), 'Running');

    // Immediately schedulable again: no backoff wait required.
    const retry = engine.schedule();
    assert.equal(retry.some((candidate) => candidate.index === 0), true);
  });

  test('FailIndex permanently fails one index even with retries remaining', () => {
    const engine = new IndexedJobEngine({ spec: baseSpec({ podFailurePolicy: policy }) });
    const [pod] = engine.schedule();

    const outcome = engine.reportPodFailure(
      makeFailure(pod.index, pod.podName, 1, {
        conditions: [{ type: 'DisruptionTarget', status: 'True' }],
      }),
    );

    assert.equal(outcome.action, 'FailIndex');
    assert.equal(outcome.matchedRuleIndex, 1);
    assert.equal(outcome.counted, false);
    assert.equal(outcome.indexPhase, 'failed');
    assert.equal(engine.getIndexState(0).failedByAction, 'FailIndex');
    // No counted failure was recorded, yet the index is already dead.
    assert.equal(engine.getIndexState(0).failures, 0);
    assert.equal(engine.getTotalCountedFailures(), 0);
    assert.equal(engine.getFailedIndexCount(), 1);
    assert.equal(engine.getJobCondition(), 'Running');

    // A failed index is never scheduled again: index 1 stays active and the
    // freed slot is backfilled by the smallest pending index (2).
    const next = engine.schedule();
    assert.deepEqual(next.map((candidate) => candidate.index), [2]);
    assert.equal(next.some((candidate) => candidate.index === 0), false);
  });

  test('FailJob terminates the whole Job and kills every running pod', () => {
    const engine = new IndexedJobEngine({ spec: baseSpec({ podFailurePolicy: policy }) });
    const scheduled = engine.schedule();
    assert.equal(scheduled.length, 2);

    // Exit code 42 misses rules 0 and 1, so the catch-all FailJob rule wins.
    const outcome = engine.reportPodFailure(
      makeFailure(scheduled[0].index, scheduled[0].podName, 42),
    );

    assert.equal(outcome.action, 'FailJob');
    assert.equal(outcome.matchedRuleIndex, 2);
    assert.equal(outcome.jobCondition, 'Failed');
    assert.equal(outcome.failureReason, 'FailJobPolicy');
    assert.deepEqual(outcome.terminatedPods, [scheduled[1].podName]);
    assert.equal(engine.getActivePodCount(), 0);

    // No further pods are scheduled once the Job is terminal.
    assert.deepEqual(engine.schedule(), []);
  });

  test('Count (explicit or default) increments per-index and global counters', () => {
    const countPolicy: PodFailurePolicy = {
      rules: [{ action: 'Count', onExitCodes: { operator: 'In', values: [7] } }],
    };
    const engine = new IndexedJobEngine({
      spec: baseSpec({ podFailurePolicy: countPolicy }),
    });
    const [pod] = engine.schedule();

    const matched = engine.reportPodFailure(makeFailure(0, pod.podName, 7));
    assert.equal(matched.action, 'Count');
    assert.equal(matched.counted, true);
    assert.equal(engine.getIndexState(0).failures, 1);
    assert.equal(engine.getTotalCountedFailures(), 1);

    // No policy at all means the same default counted behaviour.
    const defaultEngine = new IndexedJobEngine({ spec: baseSpec() });
    const [defaultPod] = defaultEngine.schedule();
    const defaultOutcome = defaultEngine.reportPodFailure(
      makeFailure(0, defaultPod.podName, 13),
    );
    assert.equal(defaultOutcome.action, 'Count');
    assert.equal(defaultOutcome.matchedRuleIndex, undefined);
    assert.equal(defaultEngine.getIndexState(0).failures, 1);
  });
});

describe('IndexedJobEngine — per-index retry and exponential backoff', () => {
  test('an index retries until its per-index budget is exhausted', () => {
    const clock = new FakeClock();
    const engine = new IndexedJobEngine({
      spec: baseSpec({
        completions: 1,
        backoffLimitPerIndex: 2,
        maxFailedIndexes: 1,
        parallelism: 1,
      }),
      now: clock.now,
      backoff: { initialIntervalMs: 100, multiplier: 2, maxIntervalMs: 10_000 },
    });

    let pod = engine.schedule()[0];
    assert.equal(pod.index, 0);
    engine.reportPodFailure(makeFailure(0, pod.podName, 1));
    assert.equal(engine.getIndexState(0).failures, 1);

    // Still inside backoff: nothing is schedulable.
    assert.deepEqual(engine.schedule(), []);

    clock.advance(100);
    pod = engine.schedule()[0];
    engine.reportPodFailure(makeFailure(0, pod.podName, 1));
    assert.equal(engine.getIndexState(0).failures, 2);

    clock.advance(200);
    pod = engine.schedule()[0];
    const terminal = engine.reportPodFailure(makeFailure(0, pod.podName, 1));

    // Third counted failure exceeds limit 2: index 0 is permanently failed.
    assert.equal(engine.getIndexState(0).failures, 3);
    assert.equal(engine.getIndexState(0).phase, 'failed');
    assert.equal(terminal.indexPhase, 'failed');
  });

  test('backoff deadlines are independent across indexes', () => {
    const clock = new FakeClock();
    const engine = new IndexedJobEngine({
      spec: baseSpec({ completions: 3, parallelism: 3, backoffLimitPerIndex: 5 }),
      now: clock.now,
      backoff: { initialIntervalMs: 1_000, multiplier: 2, maxIntervalMs: 100_000 },
    });

    const firstWave = engine.schedule();
    assert.deepEqual(firstWave.map((pod) => pod.index), [0, 1, 2]);

    engine.reportPodFailure(makeFailure(0, firstWave[0].podName, 1));
    clock.advance(500);
    engine.reportPodFailure(makeFailure(1, firstWave[1].podName, 1));

    assert.equal(engine.getIndexState(0).backoffUntil, 2_000);
    assert.equal(engine.getIndexState(1).backoffUntil, 2_500);

    clock.advance(500); // t = 2000: index 0 due, index 1 still waiting.
    const secondWave = engine.schedule();
    assert.deepEqual(secondWave.map((pod) => pod.index), [0]);
  });
});
