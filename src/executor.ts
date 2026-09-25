import type { Obligation } from './obligations.ts';

export interface ObligationHandlers {
  apply(obligation: Obligation): void | Promise<void>;
  compensate(obligation: Obligation): void | Promise<void>;
}

export type ExecutionStatus = 'fulfilled' | 'compensated' | 'compensation-pending';

export interface ExecutionState {
  readonly status: ExecutionStatus;
  /** Obligations applied successfully, in application order. */
  readonly applied: readonly Obligation[];
  /** The obligation whose application failed, if any. */
  readonly failedObligation?: Obligation;
  readonly failure?: unknown;
  /** Compensations still owed, in the order they must run (reverse of application). */
  readonly pendingCompensations: readonly Obligation[];
  readonly compensationErrors: readonly unknown[];
}

/**
 * Applies obligations in order. On failure, already-applied obligations are
 * compensated in strict reverse order. A failed compensation does not lose
 * state: the remaining compensations are kept in a `compensation-pending`
 * state that can be resumed with `retry`.
 */
export class ObligationExecutor {
  readonly #handlers: ObligationHandlers;

  constructor(handlers: ObligationHandlers) {
    this.#handlers = handlers;
  }

  async execute(obligations: readonly Obligation[]): Promise<ExecutionState> {
    const applied: Obligation[] = [];
    for (const obligation of obligations) {
      try {
        await this.#handlers.apply(obligation);
        applied.push(obligation);
      } catch (failure) {
        return this.#runCompensations([...applied].reverse(), {
          applied,
          failedObligation: obligation,
          failure,
          compensationErrors: [],
        });
      }
    }
    return { status: 'fulfilled', applied, pendingCompensations: [], compensationErrors: [] };
  }

  /** Resumes a `compensation-pending` state; other states are returned unchanged. */
  async retry(state: ExecutionState): Promise<ExecutionState> {
    if (state.status !== 'compensation-pending') return state;
    return this.#runCompensations([...state.pendingCompensations], state);
  }

  async #runCompensations(
    pending: Obligation[],
    base: Omit<ExecutionState, 'status' | 'pendingCompensations'>,
  ): Promise<ExecutionState> {
    const remaining = [...pending];
    const compensationErrors = [...base.compensationErrors];
    while (remaining.length > 0) {
      try {
        await this.#handlers.compensate(remaining[0]);
        remaining.shift();
      } catch (error) {
        compensationErrors.push(error);
        return {
          ...base,
          status: 'compensation-pending',
          pendingCompensations: remaining,
          compensationErrors,
        };
      }
    }
    return { ...base, status: 'compensated', pendingCompensations: [], compensationErrors };
  }
}
