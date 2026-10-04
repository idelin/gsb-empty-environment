// Error taxonomy shared by adapters, journal and executor.

/** Adapter applied (or maybe applied) the call, but the receipt was lost in transit. */
export class ReceiptLostError extends Error {
  readonly idempotencyKey: string;
  constructor(idempotencyKey: string) {
    super(`receipt lost for idempotency key ${idempotencyKey}`);
    this.name = 'ReceiptLostError';
    this.idempotencyKey = idempotencyKey;
  }
}

/** Definitive business rejection. The adapter guarantees no state was changed. */
export class AdapterBusinessError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'AdapterBusinessError';
  }
}

/** Transient infrastructure failure. The adapter guarantees no state was changed; retry is safe. */
export class TransientAdapterError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'TransientAdapterError';
  }
}

/** The journal hit a complete but corrupt record during replay. */
export class JournalCorruptionError extends Error {
  readonly line: number;
  constructor(message: string, line: number) {
    super(message);
    this.name = 'JournalCorruptionError';
    this.line = line;
  }
}

/** Test harness: simulated process crash at a persistence boundary. */
export class SimulatedCrashError extends Error {
  constructor(message = 'simulated crash') {
    super(message);
    this.name = 'SimulatedCrashError';
  }
}

/**
 * A run stopped because an action's outcome is unknown (receipt lost).
 * The workflow is NOT failed: resume() reconciles via idempotency-key query first.
 */
export class WorkflowInconclusiveError extends Error {
  readonly workflowId: string;
  readonly idempotencyKey: string;
  constructor(workflowId: string, idempotencyKey: string) {
    super(`workflow ${workflowId} stopped with unknown outcome for ${idempotencyKey}; resume to reconcile`);
    this.name = 'WorkflowInconclusiveError';
    this.workflowId = workflowId;
    this.idempotencyKey = idempotencyKey;
  }
}

/** A recoverable stop (transient adapter failure, e.g. during compensation). Resume to continue. */
export class WorkflowPausedError extends Error {
  readonly workflowId: string;
  constructor(workflowId: string, message: string) {
    super(message);
    this.name = 'WorkflowPausedError';
    this.workflowId = workflowId;
  }
}
