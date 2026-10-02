export type LockMode = 'S' | 'X';

export type TxState =
  | 'ACTIVE'      // growing phase: acquire only, never release
  | 'COMMITTING'  // shrinking point: atomic release of everything
  | 'COMMITTED'
  | 'ABORTING'
  | 'ABORTED'
  | 'CRASHED';    // failure injected, awaiting recovery

export interface LockEvent {
  kind: 'acquire' | 'release';
  txId: number;
  resource: string;
  mode: LockMode;
  at: number; // logical clock
}

export class DeadlockError extends Error {
  readonly victimTxId: number;
  constructor(victimTxId: number, cycle: number[]) {
    super(`deadlock detected, victim=tx${victimTxId}, cycle=[${cycle.join(' -> ')}]`);
    this.name = 'DeadlockError';
    this.victimTxId = victimTxId;
  }
}

export class ShrinkPhaseViolation extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ShrinkPhaseViolation';
  }
}

export class TransactionStateError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'TransactionStateError';
  }
}
