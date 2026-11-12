export type SecurityErrorCode =
  | 'INVALID_URL'
  | 'UNSUPPORTED_SCHEME'
  | 'CROSS_ORIGIN_SCRIPT'
  | 'CROSS_ORIGIN_REDIRECT'
  | 'CROSS_ORIGIN_SCOPE'
  | 'ENCODED_SEPARATOR_AMBIGUITY'
  | 'INVALID_PERCENT_ENCODING'
  | 'HEADER_SCOPE_NOT_ANCESTOR'
  | 'SCOPE_OUTSIDE_DEFAULT'
  | 'SCOPE_OUTSIDE_ALLOWED_HEADER'
  | 'BAD_STATE';

export interface SecurityErrorOptions {
  code: SecurityErrorCode;
  details?: Record<string, string>;
}

export class RegistrationSecurityError extends Error {
  readonly code: SecurityErrorCode;
  readonly details: Record<string, string>;

  constructor(message: string, options: SecurityErrorOptions) {
    super(message);
    this.name = 'RegistrationSecurityError';
    this.code = options.code;
    this.details = options.details ?? {};
  }
}

export function fail(
  code: SecurityErrorCode,
  message: string,
  details?: Record<string, string>,
): never {
  throw new RegistrationSecurityError(message, { code, details });
}
