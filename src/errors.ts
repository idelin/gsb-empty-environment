export type RestoreErrorCode =
  | 'SCHEMA_VERSION'
  | 'INTEGRITY'
  | 'DUPLICATE_NODE'
  | 'UNKNOWN_DEFINITION'
  | 'DEFINITION_VERSION'
  | 'MISSING_NODE'
  | 'ILLEGAL_CYCLE'
  | 'MONOTONIC_CYCLE'
  | 'NOT_A_STATE_NODE'
  | 'UNKNOWN_NODE';

export class RestoreError extends Error {
  readonly code: RestoreErrorCode;
  constructor(code: RestoreErrorCode, message: string) {
    super(message);
    this.name = 'RestoreError';
    this.code = code;
  }
}
