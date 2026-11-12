export type BridgeErrorCode =
  | 'BAD_PAYLOAD'
  | 'NON_CLONEABLE_ARGUMENT'
  | 'FORBIDDEN_KEY'
  | 'ACCESSOR_PROPERTY'
  | 'SYMBOL_KEY'
  | 'SCHEMA_MISMATCH'
  | 'UNREGISTERED_METHOD'
  | 'FORBIDDEN_ORIGIN';

export class BridgeError extends Error {
  readonly code: BridgeErrorCode;

  constructor(code: BridgeErrorCode, message: string) {
    super(message);
    this.name = 'BridgeError';
    this.code = code;
  }
}
