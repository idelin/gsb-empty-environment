export type FedCMValidationErrorCode =
  | "invalid_request"
  | "invalid_body"
  | "method_not_allowed"
  | "missing_sec_fetch_dest"
  | "invalid_sec_fetch_dest"
  | "unexpected_origin"
  | "unexpected_referer"
  | "redirect_not_allowed"
  | "missing_origin"
  | "invalid_origin"
  | "multiple_origins"
  | "unknown_client"
  | "origin_not_allowed"
  | "missing_cookie"
  | "session_not_found"
  | "missing_client_id"
  | "missing_account_id"
  | "account_not_found"
  | "unexpected_cookie"
  | "disclosure_not_shown";

const STATUS_BY_CODE: Record<FedCMValidationErrorCode, number> = {
  invalid_request: 400,
  invalid_body: 400,
  method_not_allowed: 405,
  missing_sec_fetch_dest: 403,
  invalid_sec_fetch_dest: 403,
  unexpected_origin: 403,
  unexpected_referer: 403,
  redirect_not_allowed: 403,
  missing_origin: 403,
  invalid_origin: 403,
  multiple_origins: 403,
  unknown_client: 403,
  origin_not_allowed: 403,
  missing_cookie: 401,
  session_not_found: 401,
  missing_client_id: 400,
  missing_account_id: 400,
  account_not_found: 403,
  unexpected_cookie: 400,
  disclosure_not_shown: 403,
};

/** An error that an endpoint handler can turn directly into a response. */
export class FedCMValidationError extends Error {
  readonly code: FedCMValidationErrorCode;
  readonly status: number;

  constructor(
    code: FedCMValidationErrorCode,
    message: string,
    status?: number,
  ) {
    super(message);
    this.name = "FedCMValidationError";
    this.code = code;
    this.status = status ?? STATUS_BY_CODE[code];
    Object.setPrototypeOf(this, FedCMValidationError.prototype);
  }
}

export interface FedCMResultOk<T> {
  ok: true;
  value: T;
}

export interface FedCMResultError {
  ok: false;
  error: FedCMValidationError;
}

export type FedCMResult<T> = FedCMResultOk<T> | FedCMResultError;

export function validationOk<T>(value: T): FedCMResultOk<T> {
  return { ok: true, value };
}

export function validationError(
  code: FedCMValidationErrorCode,
  message: string,
  status?: number,
): FedCMResultError {
  return {
    ok: false,
    error: new FedCMValidationError(code, message, status),
  };
}
