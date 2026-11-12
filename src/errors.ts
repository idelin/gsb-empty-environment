/**
 * 所有 FedCM 校验失败统一抛出 {@link FedcmError}，
 * 携带机器可读的 {@link FedcmErrorReason} 与对应 HTTP 状态码，
 * 方便端点处理器（或 {@link createFedcmHandler} 包装器）直接转成 JSON 错误响应。
 */

export type FedcmErrorReason =
  | "not_webidentity"
  | "origin_forbidden"
  | "referer_forbidden"
  | "cookie_forbidden"
  | "method_not_allowed"
  | "unsupported_media_type"
  | "invalid_json"
  | "unknown_client"
  | "origin_not_allowed"
  | "session_not_found"
  | "account_not_in_session"
  | "disclosure_required"
  | "redirect_forbidden";

export interface FedcmErrorInit {
  reason: FedcmErrorReason;
  message: string;
  status: number;
}

export class FedcmError extends Error {
  readonly reason: FedcmErrorReason;
  readonly status: number;

  constructor(init: FedcmErrorInit) {
    super(init.message);
    this.name = "FedcmError";
    this.reason = init.reason;
    this.status = init.status;
  }
}

export function isFedcmError(value: unknown): value is FedcmError {
  return value instanceof FedcmError;
}
