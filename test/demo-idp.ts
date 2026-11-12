import {
  accountsResponse,
  assertEmailDisclosure,
  createFedcmHandler,
  jsonResponse,
  validateAccountsRequest,
  validateClientMetadataRequest,
  validateIdAssertionRequest,
  type FedcmAccount,
  type IdAssertionDeps,
  type RegisteredClient,
  type SessionContext,
} from "../index.ts";

export const REGISTERED_CLIENTS = new Map<string, RegisteredClient>([
  [
    "rp-good",
    {
      clientId: "rp-good",
      allowedOrigins: ["https://rp.example"],
      metadata: {
        privacy_policy_url: "https://idp.example/privacy",
        terms_of_service_url: "https://idp.example/terms",
      },
    },
  ],
  [
    "rp-partner",
    {
      clientId: "rp-partner",
      allowedOrigins: ["https://partner.example:443"],
      metadata: { privacy_policy_url: "https://idp.example/privacy" },
    },
  ],
]);

export const SESSIONS = new Map<string, SessionContext>([
  [
    "session-alice",
    {
      sessionId: "session-alice",
      accounts: [
        {
          id: "acct-alice",
          given_name: "Ali",
          name: "Alice Zhang",
          email: "alice@idp.example",
        },
        {
          id: "acct-alice-work",
          given_name: "Ali (work)",
          name: "Alice Zhang (Work)",
          email: "alice.work@idp.example",
        },
      ],
    },
  ],
  [
    "session-bob",
    {
      sessionId: "session-bob",
      accounts: [
        {
          id: "acct-bob",
          given_name: "Bobby",
          name: "Bob Li",
          email: "bob@idp.example",
        },
      ],
    },
  ],
]);

const assertionDeps: IdAssertionDeps = {
  lookupClient: (clientId) => REGISTERED_CLIENTS.get(clientId),
  resolveSession: (cookies) => {
    const sessionId = cookies.sid;
    if (sessionId === undefined) {
      return undefined;
    }
    return SESSIONS.get(sessionId);
  },
};

export function encodeToken(claims: Record<string, unknown>): string {
  return Buffer.from(JSON.stringify(claims), "utf8").toString("base64url");
}

export function decodeToken<T = Record<string, unknown>>(token: string): T {
  return JSON.parse(Buffer.from(token, "base64url").toString("utf8")) as T;
}

/** GET /fedcm/accounts —— 凭会话 Cookie 返回该用户的账户列表。 */
export const accountsHandler = createFedcmHandler((request: Request) => {
  const { sessionId } = validateAccountsRequest(request, "sid");
  if (sessionId === undefined) {
    return accountsResponse([]);
  }
  const session = SESSIONS.get(sessionId);
  return accountsResponse(session?.accounts ?? []);
});

/** GET /fedcm/client_metadata?client_id=... —— 公开静态配置，禁止 Cookie。 */
export const clientMetadataHandler = createFedcmHandler((request: Request) => {
  const clientId = new URL(request.url).searchParams.get("client_id") ?? "";
  validateClientMetadataRequest(request, clientId);
  const client = REGISTERED_CLIENTS.get(clientId);
  if (client === undefined) {
    return jsonResponse({ error: { reason: "unknown_client", message: "Unknown client_id." } }, { status: 400 });
  }
  return jsonResponse(client.metadata);
});

/** POST /fedcm/id_assertion —— 校验来源、账户归属与披露后签发断言。 */
export const idAssertionHandler = createFedcmHandler(async (request: Request) => {
  const validated = await validateIdAssertionRequest(request, assertionDeps);

  const claims = {
    iss: "https://idp.example",
    aud: validated.client.clientId,
    sub: validated.account.id,
    email: validated.account.email,
    nonce: validated.body.nonce,
  };

  // 硬性策略：未展示披露文本时，含邮箱的断言一律不得签发。
  assertEmailDisclosure(claims, validated.disclosureTextShown);

  return jsonResponse({ token: encodeToken(claims) });
});

/** 故意返回 3xx 的处理器，用于验证包装器会拦截重定向。 */
export const redirectingHandler = createFedcmHandler(() => {
  return new Response(null, { status: 302, headers: { location: "https://idp.example/login" } });
});

export function sessionCookie(sessionId: string): string {
  return `sid=${sessionId}`;
}

export function aliceAccount(): FedcmAccount {
  return SESSIONS.get("session-alice")!.accounts[0];
}
