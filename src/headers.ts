import { FedcmError } from "./errors.ts";

/** 浏览器发起 FedCM 凭据请求时固定携带的 Sec-Fetch-Dest 值。 */
export const SEC_FETCH_DEST_WEBIDENTITY = "webidentity";

/** 端点处理器可以依赖的最小请求形状（标准 Web Fetch API 的 Request）。 */
export interface FedcmRequest {
  method: string;
  headers: Headers;
}

function header(request: FedcmRequest, name: string): string | null {
  return request.headers.get(name);
}

/** 大小写不敏感地读取单个头。 */
export function getHeader(request: FedcmRequest, name: string): string | null {
  return header(request, name);
}

/**
 * 仅接受 `Sec-Fetch-Dest: webidentity`。
 * 该头是禁止头（forbidden header），页面脚本无法伪造，缺失或为其他值一律拒绝——
 * 这是区分“浏览器 FedCM 流程”与“攻击者页面普通 fetch”的核心防线。
 */
export function assertWebIdentity(request: FedcmRequest): void {
  const dest = header(request, "sec-fetch-dest");
  if (dest !== SEC_FETCH_DEST_WEBIDENTITY) {
    throw new FedcmError({
      reason: "not_webidentity",
      status: 403,
      message:
        "FedCM endpoints require the Sec-Fetch-Dest: webidentity request header; " +
        `received ${dest === null ? "no header" : `"${dest}"`}.`,
    });
  }
}

/**
 * 浏览器发起的 FedCM 凭据请求不携带 Origin。
 * 任何带 Origin 的请求都来自普通 fetch / 跨站脚本上下文，必须拒绝。
 */
export function assertNoOrigin(request: FedcmRequest): void {
  if (header(request, "origin") !== null) {
    throw new FedcmError({
      reason: "origin_forbidden",
      status: 403,
      message: "FedCM credential requests must not carry an Origin header.",
    });
  }
}

/**
 * 浏览器发起的 FedCM 凭据请求不携带 Referer。
 * 出现 Referer 说明请求来自可导航的页面上下文，必须拒绝。
 */
export function assertNoReferer(request: FedcmRequest): void {
  if (header(request, "referer") !== null) {
    throw new FedcmError({
      reason: "referer_forbidden",
      status: 403,
      message: "FedCM credential requests must not carry a Referer header.",
    });
  }
}

/**
 * client_metadata 是公开的、按 client_id 拉取的静态配置端点，
 * 浏览器请求它时不带凭据；出现 Cookie 意味着被会话化/缓存投毒滥用，必须拒绝。
 */
export function assertNoCookie(request: FedcmRequest): void {
  if (header(request, "cookie") !== null) {
    throw new FedcmError({
      reason: "cookie_forbidden",
      status: 403,
      message: "The client_metadata endpoint must be requested without cookies.",
    });
  }
}

export function assertMethod(request: FedcmRequest, expected: string): void {
  if (request.method.toUpperCase() !== expected.toUpperCase()) {
    throw new FedcmError({
      reason: "method_not_allowed",
      status: 405,
      message: `Only ${expected.toUpperCase()} is allowed; received ${request.method.toUpperCase()}.`,
    });
  }
}

export function assertJsonContentType(request: FedcmRequest): void {
  const contentType = header(request, "content-type");
  if (contentType === null || !contentType.toLowerCase().includes("application/json")) {
    throw new FedcmError({
      reason: "unsupported_media_type",
      status: 415,
      message: "A JSON request body with application/json content type is required.",
    });
  }
}

/** 把 Cookie 请求头解析成键值映射（值做 decodeURIComponent）。 */
export function parseCookieHeader(cookieHeader: string | null): Record<string, string> {
  const cookies: Record<string, string> = {};
  if (cookieHeader === null) {
    return cookies;
  }
  for (const part of cookieHeader.split(";")) {
    const trimmed = part.trim();
    if (trimmed === "") {
      continue;
    }
    const equals = trimmed.indexOf("=");
    if (equals === -1) {
      continue;
    }
    const key = trimmed.slice(0, equals).trim();
    const value = trimmed.slice(equals + 1).trim();
    if (key !== "") {
      cookies[key] = decodeURIComponent(value);
    }
  }
  return cookies;
}

export function readCookies(request: FedcmRequest): Record<string, string> {
  return parseCookieHeader(header(request, "cookie"));
}

export function jsonResponse(body: unknown, init?: { status?: number; headers?: HeadersInit }): Response {
  return new Response(JSON.stringify(body), {
    status: init?.status ?? 200,
    headers: { "content-type": "application/json", ...init?.headers },
  });
}
