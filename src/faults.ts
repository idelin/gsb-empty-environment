export type FaultPhase = 'before' | 'after';

/** 持久化边界：每个边界都可能在其写入之前或之后发生崩溃。 */
export const Boundary = {
  TxnBegin: 'txn.begin',
  TxnOrderUpsert: 'txn.orderUpsert',
  TxnOutboxAdd: 'txn.outboxAdd',
  TxnCommit: 'txn.commit',
  RelayPublish: 'relay.publish',
  RelayMarkSent: 'relay.markSent',
  ReceiverRecord: 'receiver.record',
} as const;

export type BoundaryPoint = (typeof Boundary)[keyof typeof Boundary];

export const ALL_BOUNDARIES: BoundaryPoint[] = Object.values(Boundary);

export class CrashError extends Error {
  readonly point: BoundaryPoint;
  readonly phase: FaultPhase;

  constructor(point: BoundaryPoint, phase: FaultPhase) {
    super(`simulated crash at ${point} (${phase})`);
    this.name = 'CrashError';
    this.point = point;
    this.phase = phase;
  }
}

interface ArmedFault {
  point: BoundaryPoint;
  phase: FaultPhase;
  remaining: number;
}

/** 故障注入器：武装某个边界，代码经过该边界时抛出 CrashError 模拟进程崩溃。 */
export class FaultInjector {
  private armedFault: ArmedFault | null = null;

  arm(point: BoundaryPoint, phase: FaultPhase, times = 1): void {
    this.armedFault = { point, phase, remaining: times };
  }

  disarm(): void {
    this.armedFault = null;
  }

  check(point: BoundaryPoint, phase: FaultPhase): void {
    const fault = this.armedFault;
    if (fault && fault.point === point && fault.phase === phase) {
      fault.remaining -= 1;
      if (fault.remaining <= 0) this.armedFault = null;
      throw new CrashError(point, phase);
    }
  }
}
