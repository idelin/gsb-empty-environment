export class SimulatedCrashError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'SimulatedCrashError';
  }
}

export type InjectionPoint = 'run' | 'run_receipt' | 'compensate' | 'compensate_receipt';

type FaultAction = 'crash' | 'fail';

interface FaultRule {
  point: InjectionPoint;
  stepId: string;
  action: FaultAction;
  remaining: number;
}

export class FaultInjector {
  private rules: FaultRule[] = [];

  crashAt(point: InjectionPoint, stepId: string, times = 1): this {
    this.rules.push({ point, stepId, action: 'crash', remaining: times });
    return this;
  }

  failAt(point: InjectionPoint, stepId: string, times = 1): this {
    this.rules.push({ point, stepId, action: 'fail', remaining: times });
    return this;
  }

  check(point: InjectionPoint, stepId: string): void {
    const rule = this.rules.find((r) => r.point === point && r.stepId === stepId && r.remaining > 0);
    if (!rule) return;
    rule.remaining -= 1;
    if (rule.action === 'crash') {
      throw new SimulatedCrashError(`simulated crash at ${point}:${stepId}`);
    }
    throw new Error(`injected failure at ${point}:${stepId}`);
  }
}

export const noFaults = new FaultInjector();
