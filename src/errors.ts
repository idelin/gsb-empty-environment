export type ParseErrorCode =
  | 'syntax'
  | 'duplicate_key'
  | 'type_mismatch'
  | 'id64_format'
  | 'id64_overflow'
  | 'safeint_overflow'
  | 'unsafe_undeclared_number'
  | 'number_non_finite'
  | 'negative_zero';

export class BodyParseError extends Error {
  readonly code: ParseErrorCode;
  readonly path: string;
  readonly index: number;

  constructor(code: ParseErrorCode, message: string, path: string, index: number) {
    super(`${message} (at ${path === '' ? '$' : path}, offset ${index})`);
    this.name = 'BodyParseError';
    this.code = code;
    this.path = path;
    this.index = index;
  }
}
