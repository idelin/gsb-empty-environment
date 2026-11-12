import { FedcmError } from "./errors.ts";
import {
  assertJsonContentType,
  assertMethod,
  assertWebIdentity,
  getHeader,
  readCookies,
  type FedcmRequest,
} from "./headers.ts";
import type { FedcmAccount } from "./accounts.ts";
import type { ClientRegistryLookup, RegisteredClient } from "./client-metadata.ts";

/** 浏览器 POST 到 id_assertion 端点的 JSON 请求体。 */
export interface IdAssertionRequestBody {
  client_id: string;
  account_id: string;
  nonce?: string;
  disclosure_text_shown?: boolean;
  [key: string]: unknown;
}

export interface SessionContext {
  sessionId: string;
  /** 该会话（即用户 Cookie）名下可被选择的账户列表。 */
  accounts: FedcmAccount[];
}

export interface IdAssertionDeps {
  /** 按 client_id 查找已登记 RP（含允许来源与 client_metadata）。 */
  lookupClient: ClientRegistryLookup;
  /** 根据请求 Cookie 解析当前会话及其账户列表；无会话返回 undefined。 */
  resolveSession: (cookies: Record<string, string>) => SessionContext | undefined;
  /** 承载会话标识的 Cookie 名，默认 `sid`。 */
  sessionCookieName?: string;
}

export interface ValidatedIdAssertion {
  client: RegisteredClient;
  session: SessionContext;
  account: FedcmAccount;
  body: IdAssertionRequestBody;
  /** 浏览器是否实际向用户展示了披露文本（disclosure_text_shown === true）。 */
  disclosureTextShown: boolean;
}

function serializeOrigin(value: string): string {
  try {
    return new URL(value).origin;
  } catch {
    throw new FedcmError({
      reason: "origin_not_allowed",
      status: 403,
      message: `The request Origin "${value}" is not a valid origin.`,
    });
  }
}

/**
 * 校验 id_assertion 端点请求并返回签发断言所需的全部已验证上下文。
 *
 * 校验顺序（每一步失败都抛出 {@link FedcmError}）：
 * 1. 方法必须为 POST，且 `Sec-Fetch-Dest: webidentity`；
 * 2. 正文必须是 JSON，且包含字符串型 `client_id` / `account_id`；
 * 3. `client_id` 必须在登记表中存在；
 4. 请求 Origin 必须是该 client_id 在 client_metadata 登记来源之一——
 *    防止恶意 RP 冒用其他依赖方的来源发起请求；
 * 5. Cookie 必须能解析出会话，且正文 `account_id` 必须属于该会话的账户列表——
 *    防止用户 A 的请求里塞入用户 B 的 account_id。
 *
 * 披露要求（邮箱）由 {@link assertEmailDisclosure} / {@link withEmailClaim} 单独执行。
 */
export async function validateIdAssertionRequest(
  request: FedcmRequest & { json(): Promise<unknown> },
  deps: IdAssertionDeps,
): Promise<ValidatedIdAssertion> {
  assertWebIdentity(request);
  assertMethod(request, "POST");
  assertJsonContentType(request);

  let body: IdAssertionRequestBody;
  try {
    const parsed: unknown = await request.json();
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
      throw new Error("body must be a JSON object");
    }
    body = parsed as IdAssertionRequestBody;
  } catch (cause) {
    throw new FedcmError({
      reason: "invalid_json",
      status: 400,
      message: `The id_assertion request body is not valid JSON: ${(cause as Error).message}`,
    });
  }

  if (typeof body.client_id !== "string" || body.client_id === "") {
    throw new FedcmError({
      reason: "invalid_json",
      status: 400,
      message: "The id_assertion body requires a string \"client_id\".",
    });
  }
  if (typeof body.account_id !== "string" || body.account_id === "") {
    throw new FedcmError({
      reason: "invalid_json",
      status: 400,
      message: "The id_assertion body requires a string \"account_id\".",
    });
  }

  const client = deps.lookupClient(body.client_id);
  if (client === undefined) {
    throw new FedcmError({
      reason: "unknown_client",
      status: 400,
      message: `Unknown client_id "${body.client_id}".`,
    });
  }

  const originHeader = getHeader(request, "origin");
  if (originHeader === null) {
    throw new FedcmError({
      reason: "origin_not_allowed",
      status: 403,
      message: "The id_assertion endpoint requires an Origin header.",
    });
  }
  const requestOrigin = serializeOrigin(originHeader);
  const allowed = client.allowedOrigins.some(
    (registered) => serializeOrigin(registered) === requestOrigin,
  );
  if (!allowed) {
    throw new FedcmError({
      reason: "origin_not_allowed",
      status: 403,
      message: `Origin "${requestOrigin}" is not registered for client_id "${body.client_id}".`,
    });
  }

  const cookies = readCookies(request);
  const session = deps.resolveSession(cookies);
  if (session === undefined) {
    throw new FedcmError({
      reason: "session_not_found",
      status: 401,
      message: "No session could be resolved from the request cookies.",
    });
  }

  const account = session.accounts.find((candidate) => candidate.id === body.account_id);
  if (account === undefined) {
    throw new FedcmError({
      reason: "account_not_in_session",
      status: 403,
      message:
        `account_id "${body.account_id}" does not belong to the session ` +
        `"${session.sessionId}"; cross-account assertion attempts are rejected.`,
    });
  }

  return {
    client,
    session,
    account,
    body,
    disclosureTextShown: body.disclosure_text_shown === true,
  };
}
