// JMAP (RFC 8620) 授权包的核心类型定义。

/** JMAP 能力 URN，例如 `urn:ietf:params:jmap:mail`。 */
export type CapabilityId = string;

/** JMAP 方法名，例如 `Mailbox/query`。 */
export type MethodName = string;

/** 方法调用的客户端指定 id，结果引用通过它定位前置调用。 */
export type ClientId = string;

/** JMAP 账户 id。 */
export type AccountId = string;

/** JMAP 实体 id（Mailbox、Email 等）。 */
export type EntityId = string;

/** 方法所需的访问级别。 */
export type AccessKind = "read" | "write";

// RFC 8620 §5.1 方法级错误类型。本包只会产出 forbidden、
// unknownCapability、invalidResultReference 与 unknownMethod
// （注册表未登记方法名时）。
export type MethodErrorType =
  | "forbidden"
  | "unknownCapability"
  | "invalidResultReference"
  | "unknownMethod";

/** RFC 8620 §3.6.1 整个请求级别的错误类型。 */
export type RequestErrorType =
  | "limit"
  | "notRequest"
  | "unknownCapability";

/** 入站方法调用三元组：[方法名, 参数, 客户端 id]。 */
export type MethodCall = [MethodName, Record<string, unknown>, ClientId];

/** 方法响应三元组：[方法名或错误类型, 参数, 客户端 id]。 */
export type MethodResponse = [MethodName, Record<string, unknown>, ClientId];

/** 入站 JMAP 请求结构（RFC 8620 §3.3）。 */
export interface JmapRequest {
  using: CapabilityId[];
  methodCalls: MethodCall[];
  createdMethods?: Record<ClientId, MethodResponse>;
  [key: string]: unknown;
}

/** 会话中单个账户的描述（RFC 8620 §2）。 */
export interface AccountInfo {
  name: string;
  isPersonal: boolean;
  // 该账户具备的能力；false/缺省表示账户不支持该能力。
  accountCapabilities: Record<CapabilityId, boolean | Record<string, unknown>>;
  /** 只读账户上写方法必须被拒绝（RFC 8620 §6.1）。 */
  isReadOnly?: boolean;
}

/** 当前会话（RFC 8620 §2），授权决策的全部依据。 */
export interface Session {
  username: string;
  apiUrl: string;
  downloadUrl?: string;
  uploadUrl?: string;
  eventSourceUrl?: string;
  // key 为账户 id；不在该表中的账户一律拒绝。
  accounts: Record<AccountId, AccountInfo>;
  /** 会话级能力（RFC 8620 §2 顶层 capabilities）。 */
  capabilities: Record<CapabilityId, Record<string, unknown>>;
  /** 主账户 id，可选。 */
  primaryAccounts?: Record<CapabilityId, AccountId>;
}

// 方法描述符：分发器为每个可分发方法登记的元数据，授权器据此判断
// 能力归属、账户要求、读写级别和结果引用绑定。
export interface MethodDescriptor {
  /** 方法所属能力 URN，必须在请求 `using` 中声明。 */
  capability: CapabilityId;
  kind: AccessKind;
  // false 表示方法不针对账户（RFC 8620 §6.1 的 Core 方法，
  // 如 Core/echo），此时不做账户归属与读写校验。
  requiresAccount?: boolean;
  // 结果引用绑定：解出的实体 id 所在参数路径（相对方法参数）。
  // 授权器会把这些 id 按被调用方 accountId 重新授权。
  idBindings?: IdBinding[];
}

/** 一条结果引用 id 绑定声明。 */
export interface IdBinding {
  /** 方法参数中的路径（点号分隔，如 `filter.inMailbox`）。 */
  path: string;
  /** 传给 `IdAccessChecker` 的实体类型标识，如 `Mailbox`、`Email`。 */
  entityType: string;
  /** 该路径上的值是单个 id 还是 id 数组，默认单个。 */
  multiple?: boolean;
}

// 实体 id 访问检查器（由分发器注入）：判定 entityIds 是否都属于
// accountId 且当前会话可读/写。返回通过授权的 id 子集；不在结果中
// 的 id 即视为越权。
export type IdAccessChecker = (input: {
  accountId: AccountId;
  entityType: string;
  kind: AccessKind;
  entityIds: EntityId[];
}) => EntityId[] | Promise<EntityId[]>;

export interface AuthorizationOptions {
  session: Session;
  /** 方法名 -> 描述符。 */
  methods: Record<MethodName, MethodDescriptor>;
  /** 结果引用解出的 id 的二次授权回调。 */
  idAccessChecker?: IdAccessChecker;
  // 覆盖默认的调用上限（RFC 8620 §2 core 能力的
  // maxCallsInRequest，缺省 16）。
  maxCallsInRequest?: number;
}

/** 单个方法调用的授权决策。 */
export type CallDecision =
  | {
      allowed: true;
      /** 已解析并替换结果引用后的参数（新对象，可直接交给执行器）。 */
      resolvedArgs: Record<string, unknown>;
      methodName: MethodName;
      clientId: ClientId;
      accountId?: AccountId;
      kind: AccessKind;
    }
  | {
      allowed: false;
      /** 直接作为 method-level 错误响应返回，继续处理后续调用。 */
      errorType: MethodErrorType;
      detail: string;
      methodName: MethodName;
      clientId: ClientId;
    };

/** 整请求预检结果。 */
export type RequestGate =
  | { ok: true }
  | { ok: false; errorType: RequestErrorType; detail: string };
