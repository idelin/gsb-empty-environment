export type TrimErrorCode =
  | 'UNKNOWN_VARIABLE'
  | 'UNCLOSED_CONDITION'
  | 'UNEXPECTED_DIRECTIVE'
  | 'DUPLICATE_ELSE'
  | 'ELIF_AFTER_ELSE'
  | 'INVALID_EXPRESSION';

export class TrimError extends Error {
  readonly code: TrimErrorCode;
  /** 1-based line number in the original source. */
  readonly line: number;

  constructor(code: TrimErrorCode, line: number, message: string) {
    super(`line ${line}: ${message}`);
    this.name = 'TrimError';
    this.code = code;
    this.line = line;
  }
}
