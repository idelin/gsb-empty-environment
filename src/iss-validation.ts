/**
 * RFC 9207 — OAuth 2.0 Authorization Server Issuer Identifier
 * in the Authorization Response.
 *
 * 防护授权码混淆攻击：授权请求时把所用授权服务器的 issuer 记入会话，
 * 回调时对响应中的 iss 参数做严格校验，校验通过前不得使用 code。
 */

/**
 * 授权服务器信息。issuer 必须与发起授权请求时使用的 issuer URL
 * 完全一致（同一字符串），元数据字段来自授权服务器的
 * authorization_server_metadata（RFC 8414）。
 */
export interface AuthorizationServerRecord {
  /** 授权服务器的 issuer URL，按原样保存、逐字节比较。 */
  issuer: string;
  /** 元数据 authorization_response_iss_parameter_supported 的值。 */
  authorizationResponseIssParameterSupported?: boolean;
}

/**
 * 回调路由所绑定的会话对象需要满足的最小形状。
 * 字段名固定，便于直接挂到 express-session / 自研会话等存储上。
 */
export interface IssuerValidationSession {
  oauthAuthorizationServer?: AuthorizationServerRecord;
}

export type IssuerValidationErrorCode =
  | 'missing_session'
  | 'missing_iss'
  | 'duplicate_iss'
  | 'iss_mismatch';

/** 所有 iss 校验失败都抛出该错误，路由可按 code 映射为 400。 */
export class IssuerValidationError extends Error {
  readonly code: IssuerValidationErrorCode;

  constructor(code: IssuerValidationErrorCode, message: string) {
    super(message);
    this.name = 'IssuerValidationError';
    this.code = code;
  }
}

/** 回调入参兼容完整 URL 字符串、相对路径、URL 对象与 URLSearchParams。 */
export type CallbackInput = string | URL | URLSearchParams;

/**
 * 发起授权请求时调用：把本次授权所用授权服务器的 issuer 与
 * 元数据能力写入会话，供回调阶段校验。
 */
export function rememberAuthorizationServer(
  session: IssuerValidationSession,
  record: AuthorizationServerRecord,
): void {
  if (typeof record.issuer !== 'string' || record.issuer.length === 0) {
    throw new TypeError('record.issuer must be a non-empty string');
  }
  session.oauthAuthorizationServer = {
    issuer: record.issuer,
    authorizationResponseIssParameterSupported:
      record.authorizationResponseIssParameterSupported === true,
  };
}

/**
 * 从各种回调入参中取出查询参数。重复参数由 URLSearchParams 保留，
 * 后续通过 getAll('iss') 识别。
 */
export function parseCallbackParams(input: CallbackInput): URLSearchParams {
  if (input instanceof URLSearchParams) {
    return new URLSearchParams(input);
  }
  if (input instanceof URL) {
    return new URLSearchParams(input.searchParams);
  }
  return parseQueryString(input);
}

function parseQueryString(raw: string): URLSearchParams {
  let value = raw;
  const hashIndex = value.indexOf('#');
  if (hashIndex !== -1) {
    value = value.slice(0, hashIndex);
  }
  const questionIndex = value.indexOf('?');
  if (questionIndex !== -1) {
    value = value.slice(questionIndex + 1);
  }
  return new URLSearchParams(value);
}

/**
 * RFC 9207 第 4.2 / 5.2 节的核心校验规则，成功响应与错误响应共用：
 *
 * 1. iss 出现超过一次：无法确定签发者，拒绝。
 * 2. iss 缺失：仅当服务器声明支持该参数时拒绝；未声明支持时放行。
 * 3. iss 存在：必须与会话中记录的 issuer 逐字节相等，
 *    不做任何 URL 规范化（不补/去尾部斜杠、不统一大小写）。
 */
export function verifyResponseIssuer(
  params: URLSearchParams,
  record: AuthorizationServerRecord,
): void {
  const receivedIssuers = params.getAll('iss');

  if (receivedIssuers.length > 1) {
    throw new IssuerValidationError(
      'duplicate_iss',
      `authorization response contains ${receivedIssuers.length} iss parameters; exactly one is allowed`,
    );
  }

  if (receivedIssuers.length === 0) {
    if (record.authorizationResponseIssParameterSupported === true) {
      throw new IssuerValidationError(
        'missing_iss',
        'authorization response omits iss although the authorization server declares authorization_response_iss_parameter_supported=true',
      );
    }
    return;
  }

  const receivedIssuer = receivedIssuers[0];
  if (receivedIssuer !== record.issuer) {
    throw new IssuerValidationError(
      'iss_mismatch',
      'authorization response iss does not exactly match the issuer recorded for the authorization request',
    );
  }
}

export interface VerifiedCallback {
  /** 已通过 iss 校验的全部回调参数。 */
  readonly params: URLSearchParams;
  /** 是否为 OAuth 错误响应（查询中带 error 参数）。 */
  readonly isErrorResponse: boolean;
  /** 会话中记录的本次授权服务器信息。 */
  readonly record: AuthorizationServerRecord;
}

/**
 * 回调路由入口：在触碰 code 之前完成全部 iss 校验。
 * 校验失败抛 IssuerValidationError，调用方必须立即结束请求，
 * 不得继续调用任何令牌端点。
 */
export function verifyAuthorizationCallback(
  callback: CallbackInput,
  session: IssuerValidationSession,
): VerifiedCallback {
  const record = session.oauthAuthorizationServer;
  if (!record) {
    throw new IssuerValidationError(
      'missing_session',
      'no authorization server record found in session; cannot verify response issuer',
    );
  }

  const params = parseCallbackParams(callback);
  verifyResponseIssuer(params, record);

  return {
    params,
    isErrorResponse: params.has('error'),
    record,
  };
}
