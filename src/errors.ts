/** XA-style error codes, mirroring the xa.h return codes. */
export class XaError extends Error {
  readonly code: string;

  constructor(code: string, message: string) {
    super(message);
    this.name = 'XaError';
    this.code = code;
  }
}

/** The resource is locked by another (in-doubt) transaction. */
export class LockConflictError extends XaError {
  constructor(key: string, holder: string) {
    super('XAER_LOCK_CONFLICT', `resource "${key}" is locked by transaction "${holder}"`);
  }
}

/** The xid is not known to this resource manager (XAER_NOTA). */
export class UnknownTransactionError extends XaError {
  constructor(xid: string) {
    super('XAER_NOTA', `unknown transaction "${xid}"`);
  }
}

/** The operation is invalid for the transaction's current state (XAER_PROTO). */
export class InvalidStateError extends XaError {
  constructor(xid: string, detail: string) {
    super('XAER_PROTO', `transaction "${xid}": ${detail}`);
  }
}
