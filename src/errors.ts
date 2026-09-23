export type ErrorCode =
  | 'SCHEMA_VERSION'
  | 'INTEGRITY'
  | 'MISSING_NODE'
  | 'UNKNOWN_DEFINITION'
  | 'DEFINITION_VERSION'
  | 'EVALUATION_CYCLE'
  | 'MONOTONIC_NONCONVERGENCE'
  | 'DUPLICATE_NODE'
  | 'INVALID_ENVELOPE'
  | 'UNKNOWN_NODE'
  | 'NOT_A_CELL'
  | 'TX_CLOSED';

export class GraphError extends Error {
  readonly code: ErrorCode;
  constructor(code: ErrorCode, message: string) {
    super(message);
    this.name = 'GraphError';
    this.code = code;
  }
}
