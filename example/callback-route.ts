/**
 * 回调路由集成示例（零依赖，结构与 express 风格路由一致）。
 * 运行：node --experimental-strip-types example/callback-route.ts
 */

import {
  type IssuerValidationSession,
  IssuerValidationError,
  rememberAuthorizationServer,
} from '../src/iss-validation.ts';
import { handleAuthorizationCallback } from '../src/callback.ts';

interface MinimalRequest {
  url: string;
  session?: IssuerValidationSession;
}

interface MinimalResponse {
  status(code: number): this;
  send(body: string): void;
  redirect(url: string): void;
}

/** 模拟从授权服务器元数据（RFC 8414）中取到的配置。 */
const HONEST_SERVER = {
  issuer: 'https://honest.example',
  authorizationEndpoint: 'https://honest.example/authorize',
  tokenEndpoint: 'https://honest.example/token',
  authorizationResponseIssParameterSupported: true,
};

/** 1. 发起授权请求：先把 issuer 写进会话，再重定向。 */
export function startAuthorization(
  req: MinimalRequest & { session: IssuerValidationSession },
  res: MinimalResponse,
): void {
  rememberAuthorizationServer(req.session, {
    issuer: HONEST_SERVER.issuer,
    authorizationResponseIssParameterSupported:
      HONEST_SERVER.authorizationResponseIssParameterSupported,
  });

  const authorizationUrl =
    `${HONEST_SERVER.authorizationEndpoint}` +
    '?response_type=code' +
    '&client_id=s6BhdRkqt3' +
    '&redirect_uri=https%3A%2F%2Fclient.example.org%2Fcb' +
    '&state=xyz';
  res.redirect(authorizationUrl);
}

/** 2. 回调路由：iss 校验在任何令牌请求之前完成。 */
export async function authorizationCallback(
  req: MinimalRequest,
  res: MinimalResponse,
): Promise<void> {
  const session = req.session ?? {};

  try {
    const outcome = await handleAuthorizationCallback(
      req.url,
      session,
      async ({ code, record }) => {
        // 只有到这里，code 才被允许使用；且发往的是 record 对应的服务器。
        assertIssuerAllowed(record.issuer);
        return exchangeCodeAtTokenEndpoint(
          HONEST_SERVER.tokenEndpoint,
          code,
        );
      },
    );

    if (outcome.kind === 'error_response') {
      res
        .status(400)
        .send(
          `authorization error: ${outcome.error}` +
            (outcome.errorDescription ? ` (${outcome.errorDescription})` : ''),
        );
      return;
    }

    res.status(200).send('login complete');
  } catch (error) {
    if (error instanceof IssuerValidationError) {
      // 校验失败：立即结束，exchangeCodeAtTokenEndpoint 从未被调用。
      res.status(400).send(`invalid authorization response: ${error.code}`);
      return;
    }
    res.status(500).send('internal error');
  }
}

function assertIssuerAllowed(issuer: string): void {
  if (issuer !== HONEST_SERVER.issuer) {
    throw new Error(`refusing to contact unknown issuer: ${issuer}`);
  }
}

async function exchangeCodeAtTokenEndpoint(
  tokenEndpoint: string,
  code: string,
): Promise<{ access_token: string }> {
  // 真实实现里在此处向 tokenEndpoint 发起 POST。
  void tokenEndpoint;
  return { access_token: `exchanged:${code}` };
}
