export type DecodeErrorCode =
  | 'TRUNCATED'
  | 'VARINT_OVERFLOW'
  | 'INVALID_WIRE_TYPE'
  | 'INVALID_TAG'
  | 'OUT_OF_BOUNDS'
  | 'RECURSION_LIMIT'
  | 'INVALID_UTF8'
  | 'SCHEMA_MISMATCH'
  | 'MISSING_REQUIRED';

export class ProtobufDecodeError extends Error {
  readonly code: DecodeErrorCode;
  readonly offset: number;

  constructor(code: DecodeErrorCode, message: string, offset = -1) {
    super(`[${code}] ${message}${offset >= 0 ? ` (at byte offset ${offset})` : ''}`);
    this.name = 'ProtobufDecodeError';
    this.code = code;
    this.offset = offset;
  }
}
