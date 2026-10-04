// Persistent saga executor.
//
// Semantics
// ---------
// * Every external action is wrapped in an intent record and a result record.
//   An intent without a result means the outcome is UNKNOWN; recovery always
//   reconciles via the adapter's idempotency-key query before deciding, and
//   never assumes failure.
// * Only steps whose execute result is confirmed successful are compensated,
//   strictly in reverse order.
// * Compensation is itself journaled (intent + result), so a crash mid-
//   compensation resumes exactly where it stopped and completed compensations
//   are never re-applied.
// * Terminal states (completed | compensated | cancelled) are written exactly
//   once. tryTerminal() is a synchronous check-and-append, so a cancel racing
//   the final success produces exactly one terminal record.
import {
  AdapterBusinessError,
  ReceiptLostError,
  TransientAdapterError,
  WorkflowInconclusiveError,
  WorkflowPausedError,
} from './errors.ts';
import { Journal } from './journal.ts';

export type TerminalState = 'completed' | 'compensated' | 'cancelled';
export type Phase = 'execute' | 'compensate';

export interface WorkflowStep {
  readonly name: string;
  execute(idempotencyKey: string): Promise<unknown>;
  compensate(idempotencyKey: string): Promise<unknown>;
  /** Reconciliation: what does the external system have on record for this key? */
  query(idempotencyKey: string): Promise<{ kind: 'execute' | 'compensate' } | null>;
}

export interface ExecutorHooks {
  /** Test hook: runs after each step's result is durably journaled. */
  afterStep?: (stepIndex: number) => void | Promise<void>;
}

interface StepProgress {
  intent: boolean;
  outcome: 'success' | 'failure' | null;
  compIntent: boolean;
  compDone: boolean;
}

export class WorkflowExecutor {
  private readonly progress: StepProgress[];
  private cancelRequested = false;
  private terminal: TerminalState | null = null;

  readonly workflowId: string;
  private readonly steps: WorkflowStep[];
  private readonly journal: Journal;
  private readonly hooks: ExecutorHooks;

  constructor(
    workflowId: string,
    steps: WorkflowStep[],
    journal: Journal,
    hooks: ExecutorHooks = {},
  ) {
    this.workflowId = workflowId;
    this.steps = steps;
    this.journal = journal;
    this.hooks = hooks;
    this.progress = steps.map(() => ({ intent: false, outcome: null, compIntent: false, compDone: false }));
    for (const record of journal.records) this.apply(record.type, record.data);
  }

  private execKey(index: number): string {
    return `${this.workflowId}:${this.steps[index].name}`;
  }

  private compKey(index: number): string {
    return `${this.workflowId}:${this.steps[index].name}:compensate`;
  }

  private apply(type: string, data: Record<string, unknown>): void {
    const index = typeof data.step === 'string' ? this.steps.findIndex((s) => s.name === data.step) : -1;
    switch (type) {
      case 'intent':
        if (data.phase === 'compensate') this.progress[index].compIntent = true;
        else this.progress[index].intent = true;
        break;
      case 'result':
        if (data.phase === 'compensate') this.progress[index].compDone = data.outcome === 'success';
        else this.progress[index].outcome = data.outcome as 'success' | 'failure';
        break;
      case 'cancel':
        this.cancelRequested = true;
        break;
      case 'terminal':
        this.terminal = data.state as TerminalState;
        break;
    }
  }

  private log(type: string, data: Record<string, unknown>): void {
    this.journal.append(type, this.workflowId, data);
    this.apply(type, data);
  }

  /**
   * Synchronous check-and-append: the only writer of terminal states.
   * Atomic within the event loop, so concurrent cancel/success resolve to
   * exactly one terminal record.
   */
  private tryTerminal(state: TerminalState): TerminalState {
    if (this.terminal) return this.terminal;
    this.log('terminal', { state });
    return state;
  }

  /** Request cancellation. Returns the terminal state if one already exists. */
  cancel(reason = 'requested'): TerminalState | 'requested' {
    if (this.terminal) return this.terminal;
    if (!this.cancelRequested) this.log('cancel', { reason });
    return this.terminal ?? 'requested';
  }

  currentState(): TerminalState | 'running' {
    return this.terminal ?? 'running';
  }

  /**
   * Run or resume the workflow. Idempotent: safe to call any number of times
   * after crashes, lost receipts or transient failures.
   */
  async run(): Promise<TerminalState> {
    if (this.terminal) return this.terminal;
    if (!this.journal.records.some((r) => r.type === 'started' && r.workflowId === this.workflowId)) {
      this.log('started', {});
    }

    await this.reconcileUnknownOutcomes();
    if (this.terminal) return this.terminal;

    let failed = false;
    if (!this.cancelRequested) {
      for (let i = 0; i < this.steps.length; i++) {
        if (this.cancelRequested) break;
        const step = this.progress[i];
        if (step.outcome === 'success') continue;
        if (step.outcome === 'failure') {
          failed = true;
          break;
        }
        if (!step.intent) this.log('intent', { step: this.steps[i].name, phase: 'execute', key: this.execKey(i) });
        try {
          const result = await this.steps[i].execute(this.execKey(i));
          this.log('result', { step: this.steps[i].name, phase: 'execute', outcome: 'success', result });
        } catch (error) {
          if (error instanceof ReceiptLostError) {
            // Outcome unknown; do NOT journal a failure. Resume reconciles by key.
            throw new WorkflowInconclusiveError(this.workflowId, error.idempotencyKey);
          }
          if (error instanceof TransientAdapterError) {
            throw new WorkflowPausedError(this.workflowId, `step ${this.steps[i].name} paused: ${error.message}`);
          }
          if (error instanceof AdapterBusinessError) {
            this.log('result', {
              step: this.steps[i].name,
              phase: 'execute',
              outcome: 'failure',
              error: error.message,
            });
            failed = true;
            break;
          }
          throw error;
        }
        await this.hooks.afterStep?.(i);
      }
    }

    if (!failed && !this.cancelRequested) {
      return this.tryTerminal('completed');
    }

    await this.compensate();
    return this.tryTerminal(this.cancelRequested ? 'cancelled' : 'compensated');
  }

  /**
   * For every journaled intent that has no result, ask the adapter what
   * actually happened (by idempotency key) before deciding anything.
   */
  private async reconcileUnknownOutcomes(): Promise<void> {
    for (let i = 0; i < this.steps.length; i++) {
      const step = this.progress[i];
      if (step.intent && step.outcome === null) {
        const receipt = await this.steps[i].query(this.execKey(i));
        if (receipt?.kind === 'execute') {
          this.log('result', { step: this.steps[i].name, phase: 'execute', outcome: 'success', reconciled: true });
        }
        // No receipt: the action never committed; the forward pass will
        // re-execute it under the same idempotency key.
      }
      if (step.compIntent && !step.compDone) {
        const receipt = await this.steps[i].query(this.compKey(i));
        if (receipt?.kind === 'compensate') {
          this.log('result', { step: this.steps[i].name, phase: 'compensate', outcome: 'success', reconciled: true });
        }
      }
    }
  }

  /** Compensate confirmed-successful steps only, in reverse order. */
  private async compensate(): Promise<void> {
    for (let i = this.steps.length - 1; i >= 0; i--) {
      const step = this.progress[i];
      if (step.outcome !== 'success' || step.compDone) continue;
      if (!step.compIntent) {
        this.log('intent', { step: this.steps[i].name, phase: 'compensate', key: this.compKey(i) });
      }
      try {
        await this.steps[i].compensate(this.compKey(i));
        this.log('result', { step: this.steps[i].name, phase: 'compensate', outcome: 'success' });
      } catch (error) {
        if (error instanceof ReceiptLostError) {
          throw new WorkflowInconclusiveError(this.workflowId, error.idempotencyKey);
        }
        if (error instanceof TransientAdapterError || error instanceof AdapterBusinessError) {
          // Recoverable: stay non-terminal; a later run() resumes here.
          throw new WorkflowPausedError(
            this.workflowId,
            `compensation for ${this.steps[i].name} paused: ${error.message}`,
          );
        }
        throw error;
      }
    }
  }
}
