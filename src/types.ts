/**
 * Type definitions for the Indexed Job scheduling and failure-policy engine.
 *
 * The shapes mirror the relevant parts of the Kubernetes `batch/v1` Job API
 * (`completionMode: Indexed`, `podFailurePolicy`, `backoffLimitPerIndex`,
 * `maxFailedIndexes`) so that a real control-plane object can be mapped onto
 * them with little effort.
 */

/** Actions a {@link PodFailurePolicyRule} may request when it matches. */
export type PodFailurePolicyAction =
  | 'Count'
  | 'FailIndex'
  | 'FailJob'
  | 'Ignore';

/** Set operators supported by the exit-code and pod-condition matchers. */
export type PodFailurePolicyOperator = 'In' | 'NotIn';

/**
 * Exit-code requirement for a pod-failure-policy rule
 * (mirrors `PodFailurePolicyOnExitCodesRequirement`).
 *
 * - `containerName` omitted (or empty) means every container is considered.
 * - A rule only matches when at least one inspected container has terminated
 *   with a non-zero exit code.
 */
export interface PodFailurePolicyOnExitCodes {
  containerName?: string;
  operator: PodFailurePolicyOperator;
  values: number[];
}

/**
 * Pod-condition requirement for a pod-failure-policy rule
 * (mirrors `PodFailurePolicyOnPodConditionsPattern`).
 */
export interface PodFailurePolicyOnPodCondition {
  /** e.g. `DisruptionTarget` for an eviction marker. */
  type: string;
  /** Defaults to `True` when omitted, matching the Kubernetes API. */
  status?: 'True' | 'False' | 'Unknown';
  operator?: PodFailurePolicyOperator;
}

/**
 * One ordered rule of `podFailurePolicy.rules`. All requirements that are
 * present must match (logical AND). A rule carrying neither exit-code nor
 * pod-condition requirements acts as a catch-all.
 */
export interface PodFailurePolicyRule {
  action: PodFailurePolicyAction;
  onExitCodes?: PodFailurePolicyOnExitCodes;
  onPodConditions?: PodFailurePolicyOnPodCondition[];
}

/** Full pod failure policy, equivalent to `JobSpec.podFailurePolicy`. */
export interface PodFailurePolicy {
  rules: PodFailurePolicyRule[];
}

/** Snapshot of a single container's termination state. */
export interface ContainerState {
  name: string;
  /** Exit code once the container has terminated; `undefined` while running. */
  exitCode?: number;
  /** True when the container is still running and has not terminated. */
  running?: boolean;
}

/** A pod condition entry, e.g. `{ type: 'DisruptionTarget', status: 'True' }`. */
export interface PodCondition {
  type: string;
  status: 'True' | 'False' | 'Unknown';
  reason?: string;
}

/**
 * Failure information for one pod reported to the engine. Only the fields
 * needed for policy evaluation and scheduling bookkeeping are required.
 */
export interface PodFailureInfo {
  /** Indexed completion index the pod was working on (`JOB_COMPLETION_INDEX`). */
  index: number;
  /** Unique pod identifier used for active-pod bookkeeping. */
  podName: string;
  /** Terminated container states, keyed by container name. */
  containers: ContainerState[];
  /** Current pod conditions. */
  conditions?: PodCondition[];
  /** Wall-clock timestamp of the failure; defaults to the engine clock. */
  at?: number;
}

/** Successful completion information for one pod. */
export interface PodSuccessInfo {
  index: number;
  podName: string;
}

/** Scheduling configuration equivalent to the relevant `JobSpec` fields. */
export interface IndexedJobSpec {
  /** Number of indexed completions; valid indexes are `[0, completions)`. */
  completions: number;
  /** Maximum number of pods that may run at the same time. */
  parallelism: number;
  /**
   * Per-index failure budget. A value of `0` means the first counted failure
   * permanently fails the index.
   */
  backoffLimitPerIndex: number;
  /**
   * Number of permanently failed indexes tolerated. The Job fails when the
   * number of failed indexes is strictly greater than this value.
   */
  maxFailedIndexes: number;
  /** Global counted-failure budget shared across all indexes. */
  backoffLimit: number;
  /** Optional ordered pod failure policy. */
  podFailurePolicy?: PodFailurePolicy;
}

/** Tunable knobs for the deterministic exponential backoff. */
export interface BackoffConfig {
  /** Base delay before the first retry of an index, in milliseconds. */
  initialIntervalMs: number;
  /** Growth factor applied for each successive retry. */
  multiplier: number;
  /** Hard upper bound for any single delay, in milliseconds. */
  maxIntervalMs: number;
}

/** Construction options for {@link IndexedJobEngine}. */
export interface IndexedJobEngineOptions {
  spec: IndexedJobSpec;
  backoff?: Partial<BackoffConfig>;
  /**
   * Injectable clock. Defaults to `Date.now`, which keeps the engine free of
   * hidden timers and makes every scheduling decision deterministic in tests.
   */
  now?: () => number;
}

/** Lifecycle state of a single indexed completion. */
export type IndexPhase = 'pending' | 'active' | 'succeeded' | 'failed';

/** Mutable runtime bookkeeping for one index. */
export interface IndexState {
  index: number;
  phase: IndexPhase;
  /** Counted failures (every failure except `Ignore` and `FailJob`). */
  failures: number;
  /** Name of the pod currently running for this index, if any. */
  activePod?: string;
  /** Earliest timestamp at which this index may be scheduled again. */
  backoffUntil: number;
  /** Number of times a pod has been launched for this index. */
  attempts: number;
  /** Rule action that permanently failed this index, if any. */
  failedByAction?: PodFailurePolicyAction;
}

/** Overall terminal/non-terminal state of the whole Job. */
export type JobCondition = 'Running' | 'Complete' | 'Failed';

/** Why the Job transitioned into the `Failed` condition. */
export type JobFailureReason =
  | 'FailJobPolicy'
  | 'MaxFailedIndexesExceeded'
  | 'BackoffLimitExceeded';

/** A pod that the scheduler decided to launch. */
export interface ScheduledPod {
  index: number;
  podName: string;
  /** Timestamp at which the pod became due (the engine's current time). */
  scheduledAt: number;
}

/** Result of feeding one failed pod into the engine. */
export interface PodFailureOutcome {
  index: number;
  /** Matched rule action, or `Count` when no rule matched (the default). */
  action: PodFailurePolicyAction;
  /** Zero-based index of the matched rule; `undefined` for the default path. */
  matchedRuleIndex?: number;
  /**
   * Whether the outcome incremented a failure budget. Only the `Count`
   * action (explicit or default) increments counters; policy-driven
   * `FailIndex`/`FailJob` and `Ignore` do not.
   */
  counted: boolean;
  /** Phase of the affected index after applying the outcome. */
  indexPhase: IndexPhase;
  /** Condition of the whole Job after applying the outcome. */
  jobCondition: JobCondition;
  /** Set when this outcome terminated the Job. */
  failureReason?: JobFailureReason;
  /**
   * Pods that must be stopped because the Job terminated. `FailJob` and a
   * breached global threshold terminate every still-running pod.
   */
  terminatedPods: string[];
}

/** Result of feeding one successful pod into the engine. */
export interface PodSuccessOutcome {
  index: number;
  indexPhase: IndexPhase;
  jobCondition: JobCondition;
}
