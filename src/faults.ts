/** Simulates a hard process crash: the executor dies without persisting the next receipt. */
export class SimulatedCrash extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'SimulatedCrash';
  }
}

/**
 * Fault injection hooks consulted by the executor at exactly the points
 * where a real process could die or a downstream call could fail.
 */
export class FaultInjector {
  /** Crash after execute() applied its effect but before StepSucceeded is persisted (receipt loss). */
  readonly crashBeforeSuccessReceipt = new Set<string>();
  /** Crash after compensate() applied its effect but before CompensationSucceeded is persisted. */
  readonly crashBeforeCompensationReceipt = new Set<string>();
  /** Number of times compensate(stepId) should throw before succeeding. */
  private compensationFailures = new Map<string, number>();

  failCompensation(stepId: string, times: number): void {
    this.compensationFailures.set(stepId, times);
  }

  maybeCrashBeforeSuccessReceipt(stepId: string): void {
    if (this.crashBeforeSuccessReceipt.delete(stepId)) {
      throw new SimulatedCrash(`crash before StepSucceeded receipt for ${stepId}`);
    }
  }

  maybeCrashBeforeCompensationReceipt(stepId: string): void {
    if (this.crashBeforeCompensationReceipt.delete(stepId)) {
      throw new SimulatedCrash(`crash before CompensationSucceeded receipt for ${stepId}`);
    }
  }

  maybeFailCompensation(stepId: string): void {
    const remaining = this.compensationFailures.get(stepId) ?? 0;
    if (remaining > 0) {
      this.compensationFailures.set(stepId, remaining - 1);
      throw new Error(`injected compensation failure for ${stepId}`);
    }
  }
}
