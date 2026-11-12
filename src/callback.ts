/**
 * 回调路由的参考封装：固定“先校验 iss、后使用 code”的顺序，
 * 令牌端点交换以依赖注入方式提供，校验失败时绝不会被调用。
 */

import {
  type AuthorizationServerRecord,
  type CallbackInput,
  type IssuerValidationSession,
  verifyAuthorizationCallback,
} from './iss-validation.ts';

export interface TokenExchangeInput {
  /** 已确认可安全使用的授权码。 */
  code: string;
  /** 已通过 iss 校验的全部回调参数。 */
  params: URLSearchParams;
  /** 会话中记录的本次授权服务器信息。 */
  record: AuthorizationServerRecord;
}

/** 由宿主应用实现，通常向 record 对应服务器的 token_endpoint 发请求。 */
export type TokenExchange = (
  input: TokenExchangeInput,
) => Promise<unknown> | unknown;

export type CallbackOutcome =
  | {
      kind: 'error_response';
      error: string;
      errorDescription: string | null;
      params: URLSearchParams;
      record: AuthorizationServerRecord;
    }
  | {
      kind: 'token';
      token: unknown;
      params: URLSearchParams;
      record: AuthorizationServerRecord;
    };

/**
 * 处理授权回调。任何 iss 校验失败都会直接抛 IssuerValidationError，
 * 且 exchange 不会被调用；错误响应（RFC 9207 5.2）在 iss 校验通过后
 * 原样返回，同样不会触发令牌交换。
 */
export async function handleAuthorizationCallback(
  callback: CallbackInput,
  session: IssuerValidationSession,
  exchange: TokenExchange,
): Promise<CallbackOutcome> {
  const { params, isErrorResponse, record } = verifyAuthorizationCallback(
    callback,
    session,
  );

  if (isErrorResponse) {
    return {
      kind: 'error_response',
      error: params.get('error') ?? 'unknown_error',
      errorDescription: params.get('error_description'),
      params,
      record,
    };
  }

  const code = params.get('code');
  if (!code) {
    throw new Error('authorization response is missing the code parameter');
  }

  const token = await exchange({ code, params, record });
  return { kind: 'token', token, params, record };
}
