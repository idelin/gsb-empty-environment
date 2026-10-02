export type WireErrorCode =
  | 'TRUNCATED'
  | 'MALFORMED_VARINT'
  | 'VARINT_OVERFLOW'
  | 'INVALID_TAG'
  | 'INVALID_WIRE_TYPE'
  | 'WIRE_TYPE_MISMATCH'
  | 'RECURSION_LIMIT'
  | 'INVALID_UTF8'
  | 'INVALID_PACKED_LENGTH'
  | 'UNKNOWN_FIELD';

export class WireError extends Error {
  readonly code: WireErrorCode;
  readonly offset: number;
  readonly path: string;

  constructor(code: WireErrorCode, message: string, offset = -1, path = '') {
    super(path ? `${message} (at ${path}, offset ${offset})` : `${message} (offset ${offset})`);
    this.name = 'WireError';
    this.code = code;
    this.offset = offset;
    this.path = path;
  }
}
