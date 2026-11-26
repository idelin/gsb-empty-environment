/**
 * 公共类型定义：浏览上下文树、来源链、请求/重定向链、Cookie 与决策结果。
 */

/** 不透明身份（opaque origin）的标识。 */
export interface OpaqueIdentity {
  /** 固定标识：不透明来源。 */
  readonly opaque: true;
  /** 宿主侧不透明身份的唯一标签（浏览器内部 nonce 的抽象）。 */
  readonly identityTag: string;
}

/** 可序列化的来源描述。 */
export type Origin =
  | { readonly opaque: false; readonly scheme: string; readonly host: string; readonly port: number | null }
  | OpaqueIdentity;

/** 浏览上下文树中的一个框架节点。 */
export interface FrameNode {
  /** 上下文唯一标识。 */
  readonly id: string;
  /** 该框架当前生效的来源（sandbox 去源后为不透明身份）。 */
  readonly effectiveOrigin: Origin;
  /**
   * 是否带 sandbox 标记。
   * true 且未启用 allow-same-origin 时，effectiveOrigin 必须为不透明身份。
   */
  readonly sandboxed: boolean;
  /** sandbox 是否启用 allow-same-origin。 */
  readonly allowSameOrigin: boolean;
  /** 子框架。 */
  readonly children: readonly FrameNode[];
}

/** 祖先来源链的一帧（从顶层框架到当前发起框架）。 */
export interface AncestorEntry {
  readonly frameId: string;
  readonly effectiveOrigin: Origin;
  readonly sandboxed: boolean;
  readonly allowSameOrigin: boolean;
}

/** 重定向链中的一跳。 */
export interface RedirectHop {
  /** 响应状态码：301/302/307/308。 */
  readonly status: 301 | 302 | 303 | 307 | 308;
  /** Location 指向的绝对 URL。 */
  readonly location: string;
  /** 该跳响应到达时刻（毫秒，单调时钟，与请求同一时基）。 */
  readonly atMs: number;
}

/** 一次请求的输入快照（在发起时刻冻结）。 */
export interface RequestInput {
  /** 请求发起时刻（毫秒）。 */
  readonly initiatedAtMs: number;
  /** HTTP 方法（原始方法，重定向方法演变由引擎处理）。 */
  readonly method: string;
  /** 请求初始 URL。 */
  readonly url: string;
  /** 发起框架到顶层框架的祖先来源链（末项为发起框架）。 */
  readonly ancestorChain: readonly AncestorEntry[];
  /** 是否为顶层导航（地址栏/顶层框架导航）。 */
  readonly topLevelNavigation: boolean;
  /** 是否由用户手势触发（记录用，不放宽 Lax+POST）。 */
  readonly hasUserActivation: boolean;
  /** 重定向链（按发生顺序；空数组表示无重定向）。 */
  readonly redirects: readonly RedirectHop[];
  /** 发起时生效的策略版本（BFCache 在途请求不受后续升版影响）。 */
  readonly policyVersion: string;
}

/** 待判定的 Cookie。 */
export interface CookieInput {
  readonly name: string;
  readonly domain: string;
  readonly path: string;
  readonly sameSite: 'strict' | 'lax' | 'none';
  /** SameSite=None 是否带 Secure（不带 Secure 的 None Cookie 本身无效）。 */
  readonly secure: boolean;
  /** CHIPS 分区 Cookie（SameSite=None; Partitioned）。 */
  readonly partitioned: boolean;
  /** Cookie 创建时刻（毫秒），用于 LAX-allow-unsafe 宽限年龄计算。 */
  readonly createdAtMs: number;
  /** 分区 Cookie 写入时绑定的分区键（仅 partitioned=true 时有效）。 */
  readonly partitionKey?: SiteKey | undefined;
}

/** Schemeful 站点键（scheme + 可注册域）。 */
export interface SiteKey {
  readonly scheme: string;
  readonly registrableDomain: string;
}

/** 单条 Cookie 的携带决策。 */
export interface CookieDecision {
  readonly name: string;
  readonly send: boolean;
  /** 拒绝原因（send=true 时为 null）。 */
  readonly reason: DenyReason | null;
}

export type DenyReason =
  | 'missing-external-root'
  | 'untrusted-rules-root'
  | 'invalid-rules-proof'
  | 'domain-mismatch'
  | 'path-mismatch'
  | 'insecure-none-cookie'
  | 'same-site-strict-blocked'
  | 'lax-cross-site-blocked'
  | 'lax-unsafe-method-blocked'
  | 'lax-grace-expired'
  | 'cross-site-redirect-latch'
  | '303-reactivation-denied'
  | 'partition-key-mismatch'
  | 'opaque-partition'
  | 'sandbox-opaque-origin'
  | 'secure-context-required';

/** 一次请求的整体决策输出。 */
export interface CarryDecision {
  /** 请求发起时策略版本快照。 */
  readonly policyVersion: string;
  /** 最终实际发出的方法（303 后降级为 GET 等）。 */
  readonly effectiveMethod: string;
  /** 最终 URL。 */
  readonly finalUrl: string;
  /** 相对初始站点，链路是否曾经跨站（闩锁，单调）。 */
  readonly crossedSiteLatch: boolean;
  /** 当前请求是否按同站处理（跨站后永不洗白）。 */
  readonly sameSiteContext: boolean;
  /** 是否具备 Lax 安全方法导航资格（仅初始安全导航可获，303 不重新激活）。 */
  readonly laxNavigationEligible: boolean;
  /** 是否处于 LAX-allow-unsafe 宽限资格（仅初始请求时刻一次性评估）。 */
  readonly laxUnsafeGraceEligible: boolean;
  /** 生效分区键（不透明顶层时为 null）。 */
  readonly partitionKey: SiteKey | null;
  /** 规则核验诊断。 */
  readonly rulesVerification: RulesVerificationResult;
  /** 每跳诊断轨迹。 */
  readonly trace: HopTrace[];
  /** 逐 Cookie 决策。 */
  readonly cookies: CookieDecision[];
  /** 决策计算时刻（无缓存重算时每次不同，结果必须一致）。 */
  readonly computedAtMs: number;
}

export interface RulesVerificationResult {
  readonly accepted: boolean;
  readonly reason: DenyReason | null;
  /** 最终主机命中的 PSL 规则类型。 */
  readonly matchedRuleType: 'exact' | 'wildcard' | 'exception' | 'default' | null;
}

export interface HopTrace {
  readonly hop: number;
  readonly url: string;
  readonly method: string;
  readonly status: number | null;
  /** 该跳相对初始站点是否跨站。 */
  readonly crossSite: boolean;
  /** 截至该跳跨站闩锁是否已置位。 */
  readonly latch: boolean;
}
