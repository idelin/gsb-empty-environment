import { resolveBackoff, backoffDeadline } from './backoff.js';
import { evaluateFailurePolicy } from './failure-policy.js';
import { compressIndexRanges } from './ranges.js';
import type {
  BackoffConfig,
  IndexState,
  IndexedJobEngineOptions,
  IndexedJobSpec,
  JobCondition,
  JobFailureReason,
  PodFailureInfo,
  PodFailureOutcome,
  PodSuccessInfo,
  PodSuccessOutcome,
  ScheduledPod,
} from './types.js';

/**
 * Deterministic, in-memory state machine for an Indexed Kubernetes Job.
 *
 * The engine owns no timers: callers feed observed pod outcomes back through
 * {@link reportPodFailure} / {@link reportPodSuccess} and pull new pods out of
 * {@link schedule}. All time decisions go through the injectable clock, which
 * makes the whole lifecycle reproducible in tests.
 */
export class IndexedJobEngine {
  readonly spec: IndexedJobSpec;
  private readonly backoffConfig: BackoffConfig;
  private readonly clock: () => number;
  private podSequence = 0;

  private readonly indexStates: IndexState[];
  private condition: JobCondition = 'Running';
  private failureReason: JobFailureReason | undefined;
  /** Every counted (`Count`/default) failure across all indexes. */
  private totalCountedFailures = 0;

  constructor(options: IndexedJobEngineOptions) {
    validateSpec(options.spec);
    this.spec = options.spec;
    this.backoffConfig = resolveBackoff(options.backoff);
    this.clock = options.now ?? (() => Date.now());
    this.indexStates = Array.from({ length: options.spec.completions }, (_unused, index) => ({
      index,
      phase: 'pending',
      failures: 0,
      backoffUntil: 0,
      attempts: 0,
    }));
  }

  // ---- Public observation -------------------------------------------------

  getJobCondition(): JobCondition {
    return this.condition;
  }

  getFailureReason(): JobFailureReason | undefined {
    return this.failureReason;
  }

  /** Defensive copy of one index's runtime state. */
  getIndexState(index: number): IndexState {
    this.assertIndex(index);
    return { ...this.indexStates[index] };
  }

  /** Defensive copies of every index's runtime state. */
  getIndexStates(): IndexState[] {
    return this.indexStates.map((state) => ({ ...state }));
  }

  getActivePodCount(): number {
    return this.indexStates.reduce(
      (count, state) => (state.activePod !== undefined ? count + 1 : count),
      0,
    );
  }

  getTotalCountedFailures(): number {
    return this.totalCountedFailures;
  }

  getFailedIndexCount(): number {
    return this.indexStates.reduce(
      (count, state) => (state.phase === 'failed' ? count + 1 : count),
      0,
    );
  }
  getSucceededIndexCount(): number {
    return this.indexStates.reduce(
      (count, state) => (state.phase === 'succeeded' ? count + 1 : count),
      0,
    );
  }

  /** Sorted indexes that completed successfully. */
  getCompletedIndexes(): number[] {
    return this.indexStates
      .filter((state) => state.phase === 'succeeded')
      .map((state) => state.index)
      .sort((a, b) => a - b);
  }

  /** Sorted indexes that reached a permanent failure. */
  getFailedIndexes(): number[] {
    return this.indexStates
      .filter((state) => state.phase === 'failed')
      .map((state) => state.index)
      .sort((a, b) => a - b);
  }

  /** Canonical range string of succeeded indexes (e.g. `"0-3,5,8-10"`). */
  getCompletedIndexRanges(): string {
    return compressIndexRanges(this.getCompletedIndexes());
  }

  /** Canonical range string of permanently failed indexes. */
  getFailedIndexRanges(): string {
    return compressIndexRanges(this.getFailedIndexes());
  }

  // ---- Pod outcome ingestion ----------------------------------------------

  /**
   * Apply one observed pod failure.
   *
   * Evaluation order:
   * 1. find the first matching `podFailurePolicy` rule (default `Count` when
   *    no rule matches or no policy is configured);
   * 2. apply the action (`Ignore` / `FailIndex` / `FailJob` / `Count`);
   * 3. re-evaluate the per-index and global failure budgets;
   * 4. terminate every running pod when the Job becomes `Failed`.
   */
  reportPodFailure(failure: PodFailureInfo): PodFailureOutcome {
    this.assertIndex(failure.index);
    const target = this.indexStates[failure.index];
    const occurredAt = failure.at ?? this.clock();

    // Clear the active-pod slot regardless of the resulting action.
    if (target.activePod === failure.podName) {
      target.activePod = undefined;
    }

    if (this.condition !== 'Running') {
      return {
        index: failure.index,
        action: evaluateFailurePolicy(this.spec.podFailurePolicy, failure)?.rule.action ?? 'Count',
        counted: false,
        indexPhase: target.phase,
        jobCondition: this.condition,
        failureReason: this.failureReason,
        terminatedPods: [],
      };
    }

    const matched = evaluateFailurePolicy(this.spec.podFailurePolicy, failure);
    const action = matched?.rule.action ?? 'Count';
    const matchedRuleIndex = matched?.ruleIndex;

    switch (action) {
      case 'Ignore':
        // Ignored failures touch neither the index counter, the global
        // counter nor any backoff: the index is immediately re-schedulable.
        target.backoffUntil = 0;
        return this.buildFailureOutcome(failure, action, matchedRuleIndex, false, []);

      case 'FailIndex':
        // Permanent failure of this single index, ignoring remaining retries.
        this.permanentlyFailIndex(target, occurredAt, 'FailIndex');
        const terminatedByFailIndex = this.evaluateGlobalLimits();
        return this.buildFailureOutcome(
          failure,
          action,
          matchedRuleIndex,
          false,
          terminatedByFailIndex,
        );

      case 'FailJob':
        // Whole Job fails immediately and every still-running pod is terminated.
        const terminatedPods = this.snapshotActivePods(failure.podName);
        this.terminateJob('FailJobPolicy');
        return this.buildFailureOutcome(
          failure,
          action,
          matchedRuleIndex,
          false,
          terminatedPods,
        );

      case 'Count':
      default: {
        // Default counted path: per-index counter, global counter and backoff.
        target.failures += 1;
        this.totalCountedFailures += 1;
        target.backoffUntil = backoffDeadline(
          occurredAt,
          target.failures,
          this.backoffConfig,
        );

        if (target.failures > this.spec.backoffLimitPerIndex) {
          this.permanentlyFailIndex(target, occurredAt, 'Count');
        }
        const terminatedByLimits = this.evaluateGlobalLimits();
        return this.buildFailureOutcome(
          failure,
          'Count',
          matchedRuleIndex,
          true,
          terminatedByLimits,
        );
      }
    }
  }

  /** Mark an index permanently failed and re-evaluate the global thresholds. */
  private permanentlyFailIndex(
    target: IndexState,
    occurredAt: number,
    reason: 'Count' | 'FailIndex',
  ): void {
    if (target.phase === 'failed') {
      return;
    }
    target.phase = 'failed';
    target.activePod = undefined;
    target.backoffUntil = occurredAt;
    target.failedByAction = reason;
  }

  /**
   * Fail the Job when either global threshold is breached. Both comparisons
   * are strictly greater-than, matching the "exceeds" wording of the spec.
   */
  private evaluateGlobalLimits(): string[] {
    if (this.condition !== 'Running') {
      return [];
    }
    if (this.getFailedIndexCount() > this.spec.maxFailedIndexes) {
      const terminatedPods = this.snapshotActivePods();
      this.terminateJob('MaxFailedIndexesExceeded');
      return terminatedPods;
    }
    if (this.totalCountedFailures > this.spec.backoffLimit) {
      const terminatedPods = this.snapshotActivePods();
      this.terminateJob('BackoffLimitExceeded');
      return terminatedPods;
    }
    return [];
  }

  /** Move the Job to `Failed` and clear all remaining active pods. */
  private terminateJob(reason: JobFailureReason): void {
    this.condition = 'Failed';
    this.failureReason = reason;
    for (const state of this.indexStates) {
      state.activePod = undefined;
    }
  }

  /**
   * Capture the pods still running at termination time. The pod that triggered
   * the current failure has already had its own slot cleared, so it is
   * excluded via `excludingPodName`.
   */
  private snapshotActivePods(excludingPodName?: string): string[] {
    return this.indexStates
      .map((state) => state.activePod)
      .filter(
        (podName): podName is string =>
          podName !== undefined && podName !== excludingPodName,
      );
  }

  private buildFailureOutcome(
    failure: PodFailureInfo,
    action: PodFailureOutcome['action'],
    matchedRuleIndex: number | undefined,
    counted: boolean,
    terminatedPods: string[],
  ): PodFailureOutcome {
    return {
      index: failure.index,
      action,
      matchedRuleIndex,
      counted,
      indexPhase: this.indexStates[failure.index].phase,
      jobCondition: this.condition,
      failureReason: this.failureReason,
      terminatedPods,
    };
  }

  /** Apply one observed successful pod and complete its index. */
  reportPodSuccess(success: PodSuccessInfo): PodSuccessOutcome {
    this.assertIndex(success.index);
    const target = this.indexStates[success.index];
    if (target.activePod === success.podName) {
      target.activePod = undefined;
    }

    if (this.condition === 'Running' && target.phase !== 'failed') {
      target.phase = 'succeeded';
      target.backoffUntil = 0;
      this.recomputeCompletion();
    }

    return {
      index: success.index,
      indexPhase: target.phase,
      jobCondition: this.condition,
    };
  }

  /** Complete the Job once every index has reached a terminal phase. */
  private recomputeCompletion(): void {
    if (this.condition !== 'Running') {
      return;
    }
    const allTerminal = this.indexStates.every(
      (state) => state.phase === 'succeeded' || state.phase === 'failed',
    );
    if (!allTerminal) {
      return;
    }
    // All indexes are terminal and the failed-index ceiling was never
    // breached (otherwise evaluateGlobalLimits would already have failed us).
    this.condition = 'Complete';
  }

  // ---- Scheduling ---------------------------------------------------------

  /**
   * Launch as many pods as `parallelism` allows.
   *
   * Eligible indexes are, in ascending index order: not completed, not
   * permanently failed, without an active pod, and whose backoff deadline has
   * expired. The smallest indexes win the available concurrency slots.
   */
  schedule(now: number = this.clock()): ScheduledPod[] {
    if (this.condition !== 'Running') {
      return [];
    }

    const scheduled: ScheduledPod[] = [];
    let availableSlots = this.spec.parallelism - this.getActivePodCount();
    if (availableSlots <= 0) {
      return scheduled;
    }

    for (const state of this.indexStates) {
      if (availableSlots === 0) {
        break;
      }
      if (!this.isScheduleable(state, now)) {
        continue;
      }

      this.podSequence += 1;
      const podName = `job-pod-${state.index}-${this.podSequence}`;
      state.phase = 'active';
      state.activePod = podName;
      state.attempts += 1;
      scheduled.push({ index: state.index, podName, scheduledAt: now });
      availableSlots -= 1;
    }

    return scheduled;
  }

  private isScheduleable(state: IndexState, now: number): boolean {
    if (state.phase === 'succeeded' || state.phase === 'failed') {
      return false;
    }
    if (state.activePod !== undefined) {
      return false;
    }
    if (state.backoffUntil > now) {
      return false;
    }
    return true;
  }

  // ---- Internals ----------------------------------------------------------

  private assertIndex(index: number): void {
    if (!Number.isInteger(index) || index < 0 || index >= this.spec.completions) {
      throw new RangeError(
        `Index ${String(index)} is outside [0, ${this.spec.completions - 1}]`,
      );
    }
  }
}

/** Reject Job specs that cannot describe a valid Indexed Job. */
export function validateSpec(spec: IndexedJobSpec): void {
  if (!Number.isInteger(spec.completions) || spec.completions <= 0) {
    throw new Error('spec.completions must be a positive integer');
  }
  if (!Number.isInteger(spec.parallelism) || spec.parallelism <= 0) {
    throw new Error('spec.parallelism must be a positive integer');
  }
  if (!Number.isInteger(spec.backoffLimitPerIndex) || spec.backoffLimitPerIndex < 0) {
    throw new Error('spec.backoffLimitPerIndex must be a non-negative integer');
  }
  if (!Number.isInteger(spec.maxFailedIndexes) || spec.maxFailedIndexes < 0) {
    throw new Error('spec.maxFailedIndexes must be a non-negative integer');
  }
  if (!Number.isInteger(spec.backoffLimit) || spec.backoffLimit < 0) {
    throw new Error('spec.backoffLimit must be a non-negative integer');
  }
  if (spec.podFailurePolicy !== undefined && !Array.isArray(spec.podFailurePolicy.rules)) {
    throw new Error('spec.podFailurePolicy.rules must be an array');
  }
}
