import { assertNotRedirect } from "./accounts.ts";
import { isFedcmError, type FedcmError } from "./errors.ts";
import { jsonResponse } from "./headers.ts";

/**
 * 把任意异常转成 FedCM 错误 JSON 响应。
 * 未知异常不回传内部细节，统一为 500。
 */
export function toFedcmErrorResponse(error: unknown): Response {
  if (isFedcmError(error)) {
    const fedcmError = error as FedcmError;
    return jsonResponse(
      { error: { reason: fedcmError.reason, message: fedcmError.message } },
      { status: fedcmError.status },
    );
  }
  return jsonResponse(
    { error: { reason: "internal_error", message: "Internal server error." } },
    { status: 500 },
  );
}

/**
 * 统一的 FedCM 端点处理器包装器：
 * - 捕获 {@link FedcmError} 并映射为对应状态码的 JSON 错误；
 * - 拦截一切 3xx 响应（FedCM 端点不允许重定向），改写为 500，
 *   避免重定向后浏览器在新请求上丢失 `Sec-Fetch-Dest` 等安全头校验；
 * - 未知异常收敛为不含内部信息的 500。
 */
export function createFedcmHandler(
  handler: (request: Request) => Promise<Response> | Response,
): (request: Request) => Promise<Response> {
  return async (request: Request): Promise<Response> => {
    try {
      const response = await handler(request);
      return assertNotRedirect(response);
    } catch (error) {
      return toFedcmErrorResponse(error);
    }
  };
}
