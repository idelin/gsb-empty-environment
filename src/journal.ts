import type { PublishOutcome } from './types.ts';

/** 操作终态日志：恢复时据此重放幂等结果，而不重新执行 */
export interface Journal {
  get(operationId: string): PublishOutcome | undefined;
  record(operationId: string, outcome: PublishOutcome): void;
}

export class InMemoryJournal implements Journal {
  private readonly outcomes = new Map<string, PublishOutcome>();

  get(operationId: string): PublishOutcome | undefined {
    return this.outcomes.get(operationId);
  }

  record(operationId: string, outcome: PublishOutcome): void {
    if (!this.outcomes.has(operationId)) {
      this.outcomes.set(operationId, outcome);
    }
  }
}
