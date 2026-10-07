import { EventStore } from './event-store';
import { FaultInjector } from './faults';
import { StepDefinition, StepContext, topoLayers, topoOrder } from './graph';

export type RunStatus =
  | 'completed'
  | 'compensated'
  | 'compensation_failed';

interface RunState {
  started: Set<string>;
  succeeded: Set<string>;
  failed: Set<string>;
  compStarted: Set<string>;
  compSucceeded: Set<string>;
  runFailed: boolean;
  runCompensated: boolean;
  runCompleted: boolean;
}

function rebuildState(store: EventStore, runId: string): RunState {
  const state: RunState = {
    started: new Set(),
    succeeded: new Set(),
    failed: new Set(),
    compStarted: new Set(),
    compSucceeded: new Set(),
    runFailed: false,
    runCompensated: false,
    runCompleted: false,
  };
  for (const e of store.forRun(runId)) {
    switch (e.type) {
      case 'StepStarted': state.started.add(e.stepId!); break;
      case 'StepSucceeded': state.succeeded.add(e.stepId!); break;
      case 'StepFailed': state.failed.add(e.stepId!); break;
      case 'CompensationStarted': state.compStarted.add(e.stepId!); break;
      case 'CompensationSucceeded': state.compSucceeded.add(e.stepId!); break;
      case 'RunFailed': state.runFailed = true; break;
      case 'RunCompensated': state.runCompensated = true; break;
      case 'RunCompleted': state.runCompleted = true; break;
      default: break;
    }
  }
  return state;
}

export class Executor {
  constructor(
    private readonly store: EventStore,
    private readonly faults?: FaultInjector,
  ) {}

  private ctx(runId: string, stepId: string): StepContext {
    return { runId, stepId, idempotencyKey: `${runId}:${stepId}` };
  }

  /** Start (or, after a crash, transparently continue) a run. */
  async run(runId: string, steps: StepDefinition[]): Promise<RunStatus> {
    const state = rebuildState(this.store, runId);
    if (state.runCompleted) return 'completed';
    if (state.runCompensated) return 'compensated';
    if (state.runFailed) return this.compensate(runId, steps, state);
    if (state.started.size === 0 && !state.runFailed) {
      this.store.append('RunStarted', runId);
    }
    return this.forward(runId, steps, state);
  }

  /** Resume a crashed or compensation-failed run from the event table. */
  async resume(runId: string, steps: StepDefinition[]): Promise<RunStatus> {
    return this.run(runId, steps);
  }

  private async forward(runId: string, steps: StepDefinition[], state: RunState): Promise<RunStatus> {
    for (const layer of topoLayers(steps)) {
      const runnable = layer.filter(
        (s) =>
          !state.succeeded.has(s.id) &&
          !state.failed.has(s.id) &&
          (s.deps ?? []).every((d) => state.succeeded.has(d)),
      );
      if (runnable.length === 0) continue;

      const results = await Promise.all(
        runnable.map(async (s) => {
          try {
            await this.executeOne(runId, s, state);
            return { step: s, error: undefined as unknown };
          } catch (error) {
            return { step: s, error };
          }
        }),
      );

      const failedStep = results.find((r) => r.error !== undefined);
      if (failedStep) {
        // SimulatedCrash must propagate: the "process" dies, receipts stay as persisted.
        if (failedStep.error instanceof Error && failedStep.error.name === 'SimulatedCrash') {
          throw failedStep.error;
        }
        const message = failedStep.error instanceof Error ? failedStep.error.message : String(failedStep.error);
        this.store.append('StepFailed', runId, failedStep.step.id, message);
        state.failed.add(failedStep.step.id);
        this.store.append('RunFailed', runId, undefined, `step ${failedStep.step.id} failed: ${message}`);
        return this.compensate(runId, steps, state);
      }
    }
    this.store.append('RunCompleted', runId);
    return 'completed';
  }

  private async executeOne(runId: string, step: StepDefinition, state: RunState): Promise<void> {
    // Receipt-loss recovery: StepStarted exists but StepSucceeded was lost.
    // Re-execute with the same idempotency key; the step dedupes its effect.
    if (!state.started.has(step.id)) {
      this.store.append('StepStarted', runId, step.id);
      state.started.add(step.id);
    }
    await step.execute(this.ctx(runId, step.id));
    this.faults?.maybeCrashBeforeSuccessReceipt(step.id);
    this.store.append('StepSucceeded', runId, step.id);
    state.succeeded.add(step.id);
  }

  /**
   * Compensate exactly the steps that succeeded and were not yet compensated,
   * in reverse dependency order (dependents before their dependencies).
   */
  private async compensate(runId: string, steps: StepDefinition[], state: RunState): Promise<RunStatus> {
    const pending = topoOrder(steps)
      .reverse()
      .filter((s) => state.succeeded.has(s.id) && !state.compSucceeded.has(s.id));

    for (const step of pending) {
      if (!state.compStarted.has(step.id)) {
        this.store.append('CompensationStarted', runId, step.id);
        state.compStarted.add(step.id);
      }
      try {
        this.faults?.maybeFailCompensation(step.id);
        if (step.compensate) await step.compensate(this.ctx(runId, step.id));
      } catch (error) {
        const message = error instanceof Error ? error.message : String(error);
        this.store.append('CompensationFailed', runId, step.id, message);
        this.store.append('RunCompensationFailed', runId, undefined, message);
        return 'compensation_failed'; // resumable: resume() retries from here
      }
      this.faults?.maybeCrashBeforeCompensationReceipt(step.id);
      this.store.append('CompensationSucceeded', runId, step.id);
      state.compSucceeded.add(step.id);
    }
    this.store.append('RunCompensated', runId);
    return 'compensated';
  }
}
