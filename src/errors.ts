/** Raised at cancellation checkpoints while the job is still cancellable. */
export class CancelledError extends Error {
  constructor(message = 'job cancelled') {
    super(message);
    this.name = 'CancelledError';
  }
}

/**
 * Thrown by `submit` when the remote side definitively refused the job.
 * Any OTHER error from `submit` is treated as an inconclusive outcome
 * (the job may or may not be running) and drives the record to
 * `in-doubt` rather than "not executed".
 */
export class SubmissionRejectedError extends Error {
  constructor(message = 'submission rejected') {
    super(message);
    this.name = 'SubmissionRejectedError';
  }
}

/** Illegal phase transition attempted on the state machine. */
export class InvalidTransitionError extends Error {
  constructor(from: string, to: string) {
    super(`invalid phase transition: ${from} -> ${to}`);
    this.name = 'InvalidTransitionError';
  }
}
