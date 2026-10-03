/** Raised when the simulated disk runs out of space (ENOSPC). */
export class DiskFullError extends Error {
  readonly code = 'ENOSPC';
  constructor(message: string) {
    super(message);
    this.name = 'DiskFullError';
  }
}

/** Raised by the fault injector to simulate a sudden power loss / process crash. */
export class SimulatedCrashError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'SimulatedCrashError';
  }
}

/** Raised when a transaction conflicts on a lock held by another transaction. */
export class LockConflictError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'LockConflictError';
  }
}
