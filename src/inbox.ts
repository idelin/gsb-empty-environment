import { authorizeInboxActivity } from './authorize.ts';
import type {
  AuthorizationResult,
  FetchOutcome,
  InboxActivity,
  InboxSignatureContext,
} from './types.ts';

/** 调用方实现：基于原始 HTTP 请求完成 HTTP 签名校验并返回 keyId。 */
export type SignatureVerifier = (request: InboxRequest) => Promise<InboxSignatureContext>;

/**
 * 调用方实现：按对象 id 取回对象。
 * 必须使用 redirect: 'manual'，并把重定向的 Location 通过响应包装返回，
 * 由授权器决定是否拒绝；授权器不会要求第二次请求。
 */
export type RemoteObjectFetcher = (objectId: string) => Promise<FetchOutcome>;

/** 与具体 HTTP 框架解耦的最小请求描述。 */
export interface InboxRequest {
  method: string;
  url: string;
  headers: Record<string, string>;
}

export interface InboxAuthorizerDeps {
  verifySignature: SignatureVerifier;
  fetchObject: RemoteObjectFetcher;
}

export interface InboxAuthorizer {
  /**
   * 收件箱 POST 处理入口。
   * 签名校验失败或归属校验失败都会返回 allowed: false，调用方应丢弃该活动。
   */
  (request: InboxRequest, activity: InboxActivity): Promise<AuthorizationResult>;
}

/**
 * 组装收件箱授权器：把“签名/取回由调用方注入”的边界固定下来，
 * 业务路由只需调用返回的函数。
 */
export function createInboxAuthorizer(deps: InboxAuthorizerDeps): InboxAuthorizer {
  const authorizer = async (
    request: InboxRequest,
    activity: InboxActivity,
  ): Promise<AuthorizationResult> => {
    let signature: InboxSignatureContext;
    try {
      signature = await deps.verifySignature(request);
    } catch (error) {
      return {
        allowed: false,
        reason: 'signature_rejected',
        detail: `http signature verification failed: ${(error as Error).message}`,
        activity,
      };
    }

    return authorizeInboxActivity(activity, {
      signature,
      fetchObject: deps.fetchObject,
    });
  };
  return authorizer;
}
