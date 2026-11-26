/**
 * Public type surface for the embeddable admission-pipeline orchestrator.
 *
 * The component is intentionally free of any Kubernetes runtime dependency:
 * resource objects are plain JSON values and webhook handlers are injected by
 * the embedding process.
 */

export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

export type JsonObject = { [key: string]: JsonValue };

/** RFC 6902 JSON Patch operation (also carrying the optional RFC 6902 test op). */
export interface JsonPatchOperation {
  op: 'add' | 'remove' | 'replace' | 'move' | 'copy' | 'test';
  path: string;
  value?: JsonValue;
  from?: string;
}

export type ReinvocationPolicy = 'Never' | 'IfNeeded';
export type FailurePolicy = 'Fail' | 'Ignore';

/**
 * Subset of metav1.LabelSelector. `matchExpressions` operators are:
 * In, NotIn, Exists, DoesNotExist, Gt, Lt.
 */
export interface LabelSelector {
  matchLabels?: Record<string, string>;
  matchExpressions?: Array<{
    key: string;
    operator: 'In' | 'NotIn' | 'Exists' | 'DoesNotExist' | 'Gt' | 'Lt';
    values?: string[];
  }>;
}

export interface WebhookDefinition {
  name: string;
  objectSelector?: LabelSelector;
  namespaceSelector?: LabelSelector;
  reinvocationPolicy: ReinvocationPolicy;
  failurePolicy: FailurePolicy;
  /**
   * Deterministic, side-effect-free processor supplied by the caller.
   * Receives a defensive copy of the live object; returning a patch never
   * mutates the pipeline state outside the orchestrator's atomic apply step.
   */
  handler: AdmissionHandler;
}

export interface AdmissionRequest {
  object: JsonObject;
  namespaceLabels?: Record<string, string>;
}

export interface AdmissionResponse {
  allowed: boolean;
  /** Present only when `allowed` is true. Empty/undefined means no mutation. */
  patch?: JsonPatchOperation[];
  /** Optional human-readable reason, surfaced on the trace/error. */
  statusMessage?: string;
}

export type AdmissionHandler = (
  request: AdmissionRequest,
  context: HandlerContext,
) => AdmissionResponse;

export interface HandlerContext {
  webhookName: string;
  /** Zero-based pass number; the initial pass is 0. */
  round: number;
  /** How many times this webhook has previously been invoked. */
  invocationCount: number;
}

export interface PipelineInput {
  object: JsonObject;
  webhooks: WebhookDefinition[];
  namespaceLabels?: Record<string, string>;
  /**
   * Maximum number of re-invocation passes after the initial pass.
   * 0 means "initial pass only". The total number of passes is therefore
   * `maxReinvocationRounds + 1`.
   */
  maxReinvocationRounds: number;
}

export type TraceAction =
  | 'invoke'
  | 'skip-selector-miss'
  | 'skip-never-reinvoke'
  | 'skip-no-trigger';

export type TraceOutcome =
  | 'allowed'
  | 'no-change'
  | 'denied-fail'
  | 'denied-ignored'
  | 'patch-failed-fail'
  | 'patch-failed-ignored'
  | 'error-fail'
  | 'error-ignored'
  | 'skipped';

export interface TraceEntry {
  round: number;
  /** Registration order index. */
  index: number;
  webhookName: string;
  action: TraceAction;
  outcome: TraceOutcome;
  /**
   * RFC 6901 JSON Pointer paths touched by the *successfully applied* mutation
   * operations of this invocation (deduplicated, first-seen order).
   */
  touchedPaths: string[];
  /** Present when an invocation was skipped or failed. */
  detail?: string;
}

export interface PipelineResult {
  object: JsonObject;
  trace: TraceEntry[];
  roundsExecuted: number;
  invocations: Record<string, number>;
}
