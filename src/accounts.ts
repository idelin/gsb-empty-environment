import { FedcmError } from "./errors.ts";
import {
  assertNoOrigin,
  assertNoReferer,
  assertWebIdentity,
  jsonResponse,
  readCookies,
  type FedcmRequest,
} from "./headers.ts";

/** FedCM accounts 端点返回的单条账户（字段与浏览器 FedCM API 对齐）。 */
export interface FedcmAccount {
  id: string;
  given_name: string;
  name: string;
  email: string;
  picture?: string;
  approved_clients?: string[];
  login_hints?: string[];
}

export interface FedcmAccountsBody {
  accounts: FedcmAccount[];
}

export interface ValidatedAccountsRequest {
  /** 从 Cookie 中解析出的会话标识（由处理器决定具体 Cookie 名）。 */
  sessionId: string | undefined;
  cookies: Record<string, string>;
}

/**
 * 校验 accounts 端点请求。
 *
 * 安全保证：
 * 1. 必须是浏览器 FedCM 流程：`Sec-Fetch-Dest: webidentity`，页面 fetch 无法伪造；
 * 2. 不得携带 Origin / Referer，防止跨站页面上下文触发账户枚举；
 * 3. 重定向由 createFedcmHandler 包装器在响应侧统一拦截，本端点处理器
 *    永远不应返回 3xx，任何 3xx 都会被改写成 500，避免跳转丢失 FedCM 头校验。
 *
 * @param sessionCookieName 承载会话标识的 Cookie 名。
 */
export function validateAccountsRequest(
  request: FedcmRequest,
  sessionCookieName = "sid",
): ValidatedAccountsRequest {
  assertWebIdentity(request);
  assertNoOrigin(request);
  assertNoReferer(request);

  const cookies = readCookies(request);
  return {
    sessionId: cookies[sessionCookieName],
    cookies,
  };
}

export function accountsResponse(accounts: FedcmAccount[]): Response {
  const body: FedcmAccountsBody = { accounts };
  return jsonResponse(body);
}

/** 未登录时的标准空列表响应（浏览器会引导用户走登录流程）。 */
export function noAccountsResponse(): Response {
  return accountsResponse([]);
}

/** 防御性兜底：处理器内部若误用了重定向，直接转为 500 而不是发出 3xx。 */
export function assertNotRedirect(response: Response): Response {
  if (response.status >= 300 && response.status < 400) {
    throw new FedcmError({
      reason: "redirect_forbidden",
      status: 500,
      message: "FedCM endpoints must not issue redirects.",
    });
  }
  return response;
}
