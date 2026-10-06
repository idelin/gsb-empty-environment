import { EventStore } from './eventStore.ts';
import { topoOrder } from './graph.ts';
import { FaultInjector, SimulatedCrashError, noFaults } from './faults.ts';

export interface StepDef<C> {
  id: string;
  dependsOn?: string[];
  run(ctx: C): void | Promise<void>;
  compensate?(ctx: C): void | Promise<void>;
}

export type StepStatus =
  | 'pending'
  | 'started'
  | 'completed'
  | 'failed'
  | 'compensating'
  | 'compensated'
  | 'compensation_failed';

export type RunStatus = 'completed' | 'compensated' | 'compensation_failed';

export interface ExecutorOptions<C> {
  workflowId: string;
  steps: StepDef<C>[];
  ctx: C;
  store: EventStore;
  faults?: FaultInjector;
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

export class SagaExecutor<C> {
  private readonly stepMap = new Map<string, StepDef<C>>();
  private readonly deps: Map<string, string[]>;
  private readonly order: string[];
  private readonly faults: FaultInjector;
  private readonly store: EventStore;
  private readonly ctx: C;

  constructor(options: ExecutorOptions<C>) {
    this.store = options.store;
    this.ctx = options.ctx;
    this.faults = options.faults ?? noFaults;
    this.deps = new Map();
    for (const step of options.steps) {
      if (this.stepMap.has(step.id)) {
        throw new Error(`duplicate step id: ${step.id}`);
      }
      this.stepMap.set(step.id, step);
      this.deps.set(step.id, [...(step.dependsOn ?? [])]);
    }
    for (const [id, upstream] of this.deps) {
      for (const dep of upstream) {
        if (!this.stepMap.has(dep)) {
          throw new Error(`step ${id} has unknown dependency: ${dep}`);
        }
      }
    }
    this.order = topoOrder([...this.stepMap.keys()], this.deps);
  }

  execute(): Promise<RunStatus> {
    if (this.store.readAll().length > 0) {
      throw new Error('event table is not empty; use recover() to resume');
    }
    return this.drive();
  }

  recover(): Promise<RunStatus> {
    return this.drive();
  }

  statuses(): Map<string, StepStatus> {
    const status = new Map<string, StepStatus>();
    for (const id of this.stepMap.keys()) status.set(id, 'pending');
    for (const event of this.store.readAll()) {
      if (!event.stepId) continue;
      switch (event.type) {
        case 'run_started':
          status.set(event.stepId, 'started');
          break;
        case 'step_completed':
          status.set(event.stepId, 'completed');
          break;
        case 'step_failed':
          status.set(event.stepId, 'failed');
          break;
        case 'compensation_started':
          status.set(event.stepId, 'compensating');
          break;
        case 'step_compensated':
          status.set(event.stepId, 'compensated');
          break;
        case 'compensation_failed':
          status.set(event.stepId, 'compensation_failed');
          break;
      }
    }
    return status;
  }

  private async drive(): Promise<RunStatus> {
    const events = this.store.readAll();
    if (events.some((e) => e.type === 'workflow_completed')) return 'completed';
    if (events.some((e) => e.type === 'workflow_failed')) return 'compensated';
    const forward = await this.forward();
    if (forward.ok) {
      this.store.append({ type: 'workflow_completed' });
      return 'completed';
    }
    return this.compensate(errorMessage(forward.error));
  }

  private async forward(): Promise<{ ok: true } | { ok: false; error: unknown }> {
    for (;;) {
      const status = this.statuses();
      const failedId = this.order.find((id) => status.get(id) === 'failed');
      if (failedId !== undefined) {
        return { ok: false, error: new Error(`step ${failedId} failed`) };
      }
      if (this.order.every((id) => status.get(id) === 'completed')) {
        return { ok: true };
      }
      const ready = this.order.filter((id) => {
        const st = status.get(id);
        if (st !== 'pending' && st !== 'started') return false;
        return this.deps.get(id)!.every((dep) => status.get(dep) === 'completed');
      });
      if (ready.length === 0) {
        throw new Error('executor deadlocked: inconsistent event table');
      }
      const results = await Promise.allSettled(ready.map((id) => this.runStep(id)));
      for (const result of results) {
        if (result.status === 'rejected' && result.reason instanceof SimulatedCrashError) {
          throw result.reason;
        }
      }
      const businessFailure = results.find((r) => r.status === 'rejected');
      if (businessFailure && businessFailure.status === 'rejected') {
        return { ok: false, error: businessFailure.reason };
      }
    }
  }

  private async runStep(id: string): Promise<void> {
    const step = this.stepMap.get(id)!;
    this.store.append({ type: 'run_started', stepId: id });
    try {
      this.faults.check('run', id);
      await step.run(this.ctx);
    } catch (err) {
      if (err instanceof SimulatedCrashError) throw err;
      this.store.append({ type: 'step_failed', stepId: id, detail: errorMessage(err) });
      throw err;
    }
    this.faults.check('run_receipt', id);
    this.store.append({ type: 'step_completed', stepId: id });
  }

  private async compensate(reason: string): Promise<RunStatus> {
    for (const id of [...this.order].reverse()) {
      const st = this.statuses().get(id);
      if (st !== 'completed' && st !== 'compensating' && st !== 'compensation_failed') continue;
      const step = this.stepMap.get(id)!;
      this.store.append({ type: 'compensation_started', stepId: id });
      try {
        this.faults.check('compensate', id);
        await step.compensate?.(this.ctx);
      } catch (err) {
        if (err instanceof SimulatedCrashError) throw err;
        this.store.append({ type: 'compensation_failed', stepId: id, detail: errorMessage(err) });
        return 'compensation_failed';
      }
      this.faults.check('compensate_receipt', id);
      this.store.append({ type: 'step_compensated', stepId: id });
    }
    this.store.append({ type: 'workflow_failed', detail: reason });
    return 'compensated';
  }
}
