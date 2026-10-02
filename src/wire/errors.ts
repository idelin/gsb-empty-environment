export type DecodeErrorCode =
  | 'ERR_TRUNCATED'
  | 'ERR_VARINT_OVERFLOW'
  | 'ERR_INVALID_WIRE_TYPE'
  | 'ERR_WIRE_TYPE_MISMATCH'
  | 'ERR_NEGATIVE_LENGTH'
  | 'ERR_DEPTH_LIMIT'
  | 'ERR_INVALID_UTF8'
  | 'ERR_INVALID_TAG';

export class DecodeError extends Error {
  readonly code: DecodeErrorCode;
  readonly offset: number;
  readonly fieldNumber: number | undefined;
  /** Raw message without the [code]/offset decoration, for safe re-wrapping. */
  readonly detail: string;

  constructor(code: DecodeErrorCode, message: string, offset: number, fieldNumber?: number) {
    super(`[${code}] ${message} (offset=${offset}${fieldNumber !== undefined ? `, field=${fieldNumber}` : ''})`);
    this.name = 'DecodeError';
    this.code = code;
    this.offset = offset;
    this.fieldNumber = fieldNumber;
    this.detail = message;
  }
}
