export type LedgerErrorCode =
  | "INVALID_AUTHORIZATION"
  | "INVALID_SIGNATURE"
  | "INVALID_AMOUNT"
  | "INVALID_BLOCK"
  | "INVALID_EVENT"
  | "INVALID_PROOF"
  | "UNKNOWN_AUTHORIZATION"
  | "UNKNOWN_BLOCK"
  | "BLOCK_CONFLICT"
  | "CHAIN_MISMATCH"
  | "OUT_OF_VALIDITY"
  | "INSUFFICIENT_ALLOWANCE"
  | "DUPLICATE_REQUEST"
  | "AUTHORIZATION_REVOKED"
  | "AUTHORIZATION_INVALIDATED"
  | "FINALITY_CONFLICT"
  | "SNAPSHOT_CONFLICT"
  | "PERSISTENCE_ERROR";

export class LedgerError extends Error {
  readonly code: LedgerErrorCode;

  constructor(code: LedgerErrorCode, message: string) {
    super(message);
    this.name = "LedgerError";
    this.code = code;
  }
}

export function ledgerError(code: LedgerErrorCode, message: string): LedgerError {
  return new LedgerError(code, message);
}
