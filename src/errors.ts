export class DeadlockError extends Error {
  readonly txnId: number;

  constructor(txnId: number, message: string) {
    super(message);
    this.name = 'DeadlockError';
    this.txnId = txnId;
  }
}

export class IllegalLockReleaseError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'IllegalLockReleaseError';
  }
}

export class TransactionStateError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'TransactionStateError';
  }
}

export class TransactionAbortedError extends Error {
  readonly txnId: number;
  readonly reason: string;

  constructor(txnId: number, reason: string) {
    super(`transaction ${txnId} was aborted (reason: ${reason})`);
    this.name = 'TransactionAbortedError';
    this.txnId = txnId;
    this.reason = reason;
  }
}
