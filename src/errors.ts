import type { JsonObject, TraceEntry } from './types.js';

/** Base class for every failure raised by the pipeline. */
export class AdmissionPipelineError extends Error {
  public readonly object: JsonObject;
  public readonly trace: TraceEntry[];

  constructor(message: string, object: JsonObject, trace: TraceEntry[]) {
    super(message);
    this.name = 'AdmissionPipelineError';
    this.object = object;
    this.trace = trace;
  }
}

/** Raised when a `Fail` webhook denies the request or its patch cannot apply. */
export class AdmissionFailureError extends AdmissionPipelineError {
  public readonly webhookName: string;
  public readonly round: number;
  public readonly kind: 'denied' | 'patch-failed' | 'handler-error';

  constructor(
    message: string,
    params: {
      webhookName: string;
      round: number;
      kind: 'denied' | 'patch-failed' | 'handler-error';
      object: JsonObject;
      trace: TraceEntry[];
    },
  ) {
    super(message, params.object, params.trace);
    this.name = 'AdmissionFailureError';
    this.webhookName = params.webhookName;
    this.round = params.round;
    this.kind = params.kind;
  }
}

export interface OscillationDetails {
  reason: 'state-cycle' | 'round-limit';
  /** Webhook names in the order they participated in the conflicting rewrites. */
  participatingWebhooks: string[];
  /** JSON Pointer paths participating in the conflict (deduplicated). */
  conflictPaths: string[];
  /** Present for `state-cycle`: canonicalized state that repeated. */
  repeatedState?: JsonObject;
  /** First pass that observed the repeated state (state-cycle only). */
  firstSeenRound?: number;
  repeatedRound?: number;
  /** Total passes executed when the conflict was declared. */
  roundsExecuted: number;
  trace: TraceEntry[];
  object: JsonObject;
}

/** Raised when mutating webhooks oscillate (state cycle) or exhaust the budget. */
export class OscillationConflictError extends AdmissionPipelineError {
  public readonly details: OscillationDetails;

  constructor(message: string, details: OscillationDetails) {
    super(message, details.object, details.trace);
    this.name = 'OscillationConflictError';
    this.details = details;
  }
}
