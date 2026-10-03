export type CodecErrorCode =
  | 'TRUNCATED'
  | 'BAD_MAGIC'
  | 'BAD_CRC'
  | 'UNSUPPORTED_VERSION'
  | 'LENGTH_OVERFLOW'
  | 'VARINT_OVERFLOW'
  | 'BAD_WIRE_TYPE'
  | 'MALFORMED';

export class CodecError extends Error {
  readonly code: CodecErrorCode;
  constructor(code: CodecErrorCode, message: string) {
    super(message);
    this.name = 'CodecError';
    this.code = code;
  }
}
