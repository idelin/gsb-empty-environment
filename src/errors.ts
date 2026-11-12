export type ImportMapErrorCode =
  | 'INVALID_JSON'
  | 'DUPLICATE_KEY'
  | 'PROTO_KEY'
  | 'INVALID_SHAPE'
  | 'INVALID_URL'
  | 'DISALLOWED_ORIGIN'
  | 'PREFIX_MISMATCH'
  | 'PREFIX_ESCAPE'
  | 'SECOND_MAP'
  | 'GUARD_LOCKED'
  | 'NO_MATCH';

export class ImportMapError extends Error {
  readonly code: ImportMapErrorCode;

  constructor(code: ImportMapErrorCode, message: string) {
    super(message);
    this.name = 'ImportMapError';
    this.code = code;
  }
}
