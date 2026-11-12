export enum ValidationErrorCode {
  MALFORMED_MESSAGE = 'MALFORMED_MESSAGE',
  TYPE_BODY_MISMATCH = 'TYPE_BODY_MISMATCH',
  HASH_MISMATCH = 'HASH_MISMATCH',
  DATA_BYTES_MISMATCH = 'DATA_BYTES_MISMATCH',
  FUTURE_TIMESTAMP = 'FUTURE_TIMESTAMP',
  INVALID_SIGNATURE = 'INVALID_SIGNATURE',
  SIGNER_NOT_ACTIVE = 'SIGNER_NOT_ACTIVE',
  UNKNOWN_CUSTODY = 'UNKNOWN_CUSTODY',
  DUPLICATE_MESSAGE = 'DUPLICATE_MESSAGE',
  SIGNER_ADD_SUPPRESSED = 'SIGNER_ADD_SUPPRESSED',
}

export class ValidationError extends Error {
  readonly code: ValidationErrorCode;

  constructor(code: ValidationErrorCode, message: string) {
    super(message);
    this.name = 'ValidationError';
    this.code = code;
  }
}
