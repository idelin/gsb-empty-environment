/**
 * 供 JMAP 请求分发器调用的授权包类型定义。
 * 语义对齐 RFC 8620（JMAP）：
 * - 3.3 能力
 * - 3.4 方法调用 / 错误类型
 * - 3.6 结果引用
 * - 3.7 对象内结果引用
 * - 6.1 会话
 */

/** 方法对账户能力所需的访问级别（RFC 8621 各方法按读写分类）。 */
export type AccessLevel = "read" | "write";

/** 会话中的单个账户（RFC 8620 §6.1 会话对象的 accounts 项）。 */
export interface SessionAccount {
  /** 该账户支持的能力标识，如 urn:ietf:params:jmap:mail。 */
  capabilities: ReadonlySet<string>;
  /** 只读账户不允许写方法（RFC 8620 §1.7 账户读写模型）。 */
  isReadOnly: boolean;
  /** 自定义名称，便于日志与测试断言。 */
  name?: string;
}

/** 当前会话（仅保留授权所需字段）。 */
export interface Session {
  /** accountId -> 账户信息。账户必须在此映射中才属于当前会话。 */
  accounts: ReadonlyMap<string, SessionAccount>;
  /** 会话级能力标识（对应会话对象顶层 capabilities）。 */
  capabilities: ReadonlySet<string>;
  /** 单请求方法调用数上限（RFC 8620 §3.4.1）。 */
  maxCallsInRequest: number;
  /** 自定义名称，便于日志与测试断言。 */
  name?: string;
}

/** 分发器注册的方法描述符。 */
export interface MethodDescriptor {
  /** 方法所属能力，如 Email/query 属于 urn:ietf:params:jmap:mail。 */
  capability: string;
  /** 方法需要的读或写权限。 */
  access: AccessLevel;
  /** 实际执行方法的处理器；仅在授权通过、引用解析成功后调用。 */
  handler: (args: Record<string, unknown>) => Record<string, unknown>;
}

/** 原始结果引用（RFC 8620 §3.6）。 */
export interface ResultReference {
  resultOf: string;
  name: string;
  path: string;
}

/** 方法调用三元组 [方法名, 参数, 调用 id]（RFC 8620 §3.4）。 */
export type MethodCall = [
  name: string,
  args: Record<string, unknown>,
  callId: string,
];

/** 方法响应三元组 [方法名/错误类型, 参数, 调用 id]。 */
export type MethodResponse = [
  name: string,
  args: Record<string, unknown>,
  callId: string,
];

/** 已完成的方法调用记录，供结果引用解析使用。 */
export interface CompletedCall {
  callId: string;
  methodName: string;
  /** true 表示该调用成功产出结果；错误响应不可被引用。 */
  succeeded: boolean;
  responseArgs: Record<string, unknown>;
  /** 该调用被授权访问的账户；用于引用数据的来源账户追踪。 */
  accountId: string;
}

/** 整体请求被拒绝时返回的请求级错误（unknownCapability / limit 等）。 */
export class RequestError extends Error {
  readonly type: string;
  readonly details: Record<string, unknown>;

  constructor(
    type: string,
    message: string,
    details: Record<string, unknown> = {},
  ) {
    super(message);
    this.name = "JmapRequestError";
    this.type = type;
    this.details = details;
  }
}

/** 单个方法调用的授权 / 执行错误（forbidden、unknownCapability 等）。 */
export class MethodError extends Error {
  readonly errorType: string;
  readonly details: Record<string, unknown>;

  constructor(
    errorType: string,
    message: string,
    details: Record<string, unknown> = {},
  ) {
    super(message);
    this.name = "JmapMethodError";
    this.errorType = errorType;
    this.details = details;
  }
}

/** 结果引用无法解析（RFC 8620 §3.6.1 invalidResultReference）。 */
export class ResultReferenceError extends Error {
  readonly details: Record<string, unknown>;

  constructor(message: string, details: Record<string, unknown> = {}) {
    super(message);
    this.name = "JmapResultReferenceError";
    this.details = details;
  }
}
