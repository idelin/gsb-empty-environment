import {
  assertNoCookie,
  assertNoOrigin,
  assertNoReferer,
  assertWebIdentity,
  jsonResponse,
  type FedcmRequest,
} from "./headers.ts";

/**
 * 依赖方（RP）在 IdP 的 client_metadata 端点登记的信息。
 * `privacy_policy_url` / `terms_of_service_url` 为 FedCM 规范字段。
 */
export interface ClientMetadata {
  privacy_policy_url?: string;
  terms_of_service_url?: string;
}

/** 本校验包使用的 RP 登记表条目，origins 为允许出现在 Origin 头中的来源集合。 */
export interface RegisteredClient {
  clientId: string;
  /** 该 client_id 允许的全部来源，序列化为 origin 字符串后比较（含默认端口归一化）。 */
  allowedOrigins: string[];
  metadata: ClientMetadata;
}

export type ClientRegistryLookup = (clientId: string) => RegisteredClient | undefined;

export interface ValidatedClientMetadataRequest {
  clientId: string;
}

/**
 * 校验 client_metadata 端点请求。
 *
 * 硬性要求（任务规定）：请求不得携带 Cookie——该端点是公开静态配置，
 * 带 Cookie 的请求会被拒绝，防止会话化查询与缓存投毒。
 *
 * 额外纵深防御（同样符合 FedCM 规范）：要求 `Sec-Fetch-Dest: webidentity`
 * 且不带 Origin / Referer。
 */
export function validateClientMetadataRequest(
  request: FedcmRequest,
  clientId: string,
): ValidatedClientMetadataRequest {
  assertWebIdentity(request);
  assertNoCookie(request);
  assertNoOrigin(request);
  assertNoReferer(request);
  return { clientId };
}

export function clientMetadataResponse(client: RegisteredClient): Response {
  return jsonResponse(client.metadata);
}
