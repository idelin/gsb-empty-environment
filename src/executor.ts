import type { Obligation } from './types.ts';

export interface ObligationHandler {
  execute(obligation: Obligation): void;
  compensate(obligation: Obligation): void;
}

export type ExecutorStatus =
  | 'pending'
  | 'completed'
  | 'compensated'
  | 'compensation-failed';

export class ObligationExecutor {
  status: ExecutorStatus = 'pending';
  readonly executed: string[] = [];
  readonly compensated: string[] = [];
  private readonly handlers: Record<string, ObligationHandler>;
  private doneStack: Obligation[] = [];

  constructor(handlers: Record<string, ObligationHandler>) {
    this.handlers = handlers;
  }

  run(obligations: readonly Obligation[]): ExecutorStatus {
    this.doneStack = [];
    try {
      for (const ob of obligations) {
        this.handlerFor(ob).execute(ob);
        this.executed.push(describe(ob));
        this.doneStack.push(ob);
      }
      this.status = 'completed';
    } catch {
      this.compensate();
    }
    return this.status;
  }

  retry(): ExecutorStatus {
    if (this.status === 'compensation-failed') this.compensate();
    return this.status;
  }

  get pendingCompensation(): readonly Obligation[] {
    return this.doneStack;
  }

  private compensate(): void {
    const queue = [...this.doneStack].reverse();
    this.doneStack = [];
    while (queue.length > 0) {
      const ob = queue[0];
      try {
        this.handlerFor(ob).compensate(ob);
      } catch {
        this.doneStack = queue;
        this.status = 'compensation-failed';
        return;
      }
      queue.shift();
      this.compensated.push(describe(ob));
    }
    this.status = 'compensated';
  }

  private handlerFor(ob: Obligation): ObligationHandler {
    const handler = this.handlers[ob.kind];
    if (!handler) throw new Error(`no handler for obligation kind: ${ob.kind}`);
    return handler;
  }
}

function describe(ob: Obligation): string {
  return ob.kind === 'quota' ? `quota:${ob.id}` : `mask:${ob.id}:${ob.field}`;
}
