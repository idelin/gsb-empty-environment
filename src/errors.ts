/**
 * Error codes raised by the download type guard.
 *
 * The first three codes are the "three mismatch classes". The last one is the
 * content-negotiation / multi-part bypass class.
 */
export type TypeGuardErrorCode =
  | "extension-mismatch"
  | "content-sniff-mismatch"
  | "disposition-mismatch"
  | "negotiation-bypass";

export class TypeGuardError extends Error {
  readonly code: TypeGuardErrorCode;
  readonly detail: Record<string, unknown>;

  constructor(code: TypeGuardErrorCode, message: string, detail: Record<string, unknown> = {}) {
    super(message);
    this.name = "TypeGuardError";
    this.code = code;
    this.detail = detail;
  }
}

export function fail(
  code: TypeGuardErrorCode,
  message: string,
  detail: Record<string, unknown> = {},
): never {
  throw new TypeGuardError(code, message, detail);
}
