/**
 * 收件箱授权包使用的最小 ActivityPub 数据模型。
 * 仅声明授权过程依赖的字段，其余字段原样透传。
 */

export type ActivityPubValue =
  | string
  | number
  | boolean
  | null
  | undefined
  | ActivityPubObject
  | ActivityPubValue[];

export interface ActivityPubObject {
  [key: string]: ActivityPubValue;
  id?: string;
  type?: string | string[];
  actor?: string | ActivityPubObject | Array<string | ActivityPubObject>;
  attributedTo?: string | ActivityPubObject | Array<string | ActivityPubObject>;
  object?: ActivityPubValue;
}

export type InboxActivity = ActivityPubObject;

/** 授权判定结果。允许时 reason 为 ok，其余 reason 均表示必须忽略该活动。 */
export type AuthorizationReason =
  | 'ok'
  | 'signature_rejected'
  | 'missing_activity_type'
  | 'invalid_activity_actor'
  | 'signer_actor_mismatch'
  | 'invalid_object_id'
  | 'object_not_found'
  | 'missing_attribution'
  | 'attribution_mismatch'
  | 'cross_origin_redirect'
  | 'fetch_failed'
  | 'unsupported_response';

export interface AuthorizationResult {
  allowed: boolean;
  reason: AuthorizationReason;
  /** 授权成功后可安全处理的活动（外域对象已被权威副本替换）。 */
  activity: InboxActivity;
  /** 判定基于的活动 actor id。 */
  actorId?: string;
  /** 判定过程中实际取回的对象 id（最多一个）。 */
  fetchedObjectId?: string;
  /** 机器可读的细节，便于调用方记录安全日志。 */
  detail?: string;
}

/** 调用方注入的对象取回函数的返回值，可以是已解析对象或原始响应。 */
export type FetchOutcome = ActivityPubObject | ObjectFetchResponse;

export interface ObjectFetchResponse {
  status: number;
  /**
   * 手动模式下重定向响应的 Location 头。
   * 授权器据此识别重定向；跨域重定向一律拒绝，且不会再次请求。
   */
  location?: string;
  body?: ActivityPubObject;
}

export interface ObjectFetcher {
  /**
   * 取回 objectId 指向的对象。
   *
   * 约定：
   * - 调用方应使用 redirect: 'manual'，不自行跟随重定向；
   * - 授权器每次 authorize 最多调用一次本函数，绝不会重试；
   * - 签名校验与 HTTP 传输由调用方负责，本函数只负责取回内容。
   */
  (objectId: string): Promise<FetchOutcome>;
}

export interface InboxSignatureContext {
  /** 已通过 HTTP 签名校验的 keyId，例如 https://alice.example/keys/1#main-key。 */
  keyId: string;
}

export interface AuthorizeOptions {
  /** 已通过 HTTP 签名校验的签名上下文。 */
  signature: InboxSignatureContext;
  /** 调用方注入的对象取回器。 */
  fetchObject: ObjectFetcher;
}
