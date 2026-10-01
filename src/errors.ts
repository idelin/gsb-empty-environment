export type ErrorCode =
  | 'EMPTY_INPUT'
  | 'INVALID_CHARACTER'
  | 'INVALID_EXPONENT'
  | 'TRUNCATED_INPUT'
  | 'BUDGET_EXCEEDED'
  | 'EXPONENT_OVERFLOW'
  | 'OUT_OF_RANGE'
  | 'PARSER_CLOSED';

export class DecimalError extends Error {
  readonly code: ErrorCode;
  /** Absolute character offset in the logical input stream, when applicable. */
  readonly position: number;
  constructor(code: ErrorCode, message: string, position = -1) {
    super(message);
    this.name = 'DecimalError';
    this.code = code;
    this.position = position;
  }
}

export class DecimalParseError extends DecimalError {
  readonly state: string;
  constructor(code: ErrorCode, message: string, position: number, state: string) {
    super(code, message, position);
    this.name = 'DecimalParseError';
    this.state = state;
  }
}
