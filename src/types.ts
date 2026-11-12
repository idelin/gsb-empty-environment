/**
 * 准入包的有限属性域与核心数据结构。
 *
 * 所有参与决策的属性都是有限、可枚举的联合类型，因此整个策略空间
 * 可以被完整枚举（见 src/matrix.ts 与 test/exhaustive.test.ts）。
 */

/** 结构化来源：scheme/host/port 完全相同即为同来源。 */
export interface StructuredOrigin {
  scheme: "https" | "http";
  host: string;
  port: number | null;
}

/** 来源关系：same-origin（同源）/ same-site（同站跨源）/ cross-site（跨站）。 */
export type OriginRelationship = "same-origin" | "same-site" | "cross-site";

/**
 * 资源模式（Fetch request mode 的有限子集）。
 * - navigate: 顶层导航
 * - cors: 带 CORS 的子资源请求
 * - no-cors: 不透明子资源请求（图片、脚本等）
 */
export type ResourceMode = "navigate" | "cors" | "no-cors";

/**
 * 凭据模式。
 * - include: 携带凭据（Cookie、TLS 客户端证书等）
 * - same-origin: 仅同源携带
 * - omit: 不携带
 */
export type CredentialsMode = "include" | "same-origin" | "omit";

/**
 * 文档嵌入策略（Cross-Origin-Embedder-Policy 的有限子集）。
 * - unsafe-none: 未启用隔离
 * - require-corp: 启用跨源隔离，子资源必须显式获许
 * - credentialless: 启用无凭据跨源隔离
 */
export type EmbeddingPolicy = "unsafe-none" | "require-corp" | "credentialless";

/**
 * 资源声明（来自响应头的有限属性）。
 * corp 对应 Cross-Origin-Resource-Policy：
 * - none: 未返回 CORP 头
 * - same-origin / same-site / cross-origin: 显式许可范围
 * corsAccess 表示响应是否带匹配的 Access-Control-Allow-Origin（CORS 校验通过）。
 */
export interface ResourceDeclaration {
  corp: "none" | "same-origin" | "same-site" | "cross-origin";
  corsAccess: boolean;
}

/** 决策结果。 */
export type Verdict = "allow" | "deny";

/**
 * 策略矩阵的查找键：决策所依赖的全部有限属性。
 * 来源关系由 StructuredOrigin 对（文档来源、资源来源）派生，
 * 其余五个字段对应任务要求的结构化来源、资源模式、凭据模式、
 * 嵌入策略与资源声明。
 */
export interface PolicyKey {
  relationship: OriginRelationship;
  resourceMode: ResourceMode;
  credentials: CredentialsMode;
  embedding: EmbeddingPolicy;
  corp: ResourceDeclaration["corp"];
  corsAccess: boolean;
}

/** 调用方提供的策略矩阵：对有限属性组合逐一给出 allow/deny。 */
export type PolicyMatrix = Readonly<Record<string, Verdict>>;

/** 一次准入评估的输入。 */
export interface AdmissionInput {
  /** 发起请求的文档来源。 */
  documentOrigin: StructuredOrigin;
  /** 当前这一跳的资源 URL（重定向后逐跳更新）。 */
  resourceUrl: string;
  resourceMode: ResourceMode;
  credentials: CredentialsMode;
  embedding: EmbeddingPolicy;
  /** 当前这一跳响应给出的资源声明。 */
  declaration: ResourceDeclaration;
}

/** 拒绝原因（用于可观测性，不参与矩阵判定）。 */
export type DenyReason =
  | "matrix-deny"
  | "invalid-origin"
  | "invalid-redirect";

/** 一次准入评估的结果。 */
export interface AdmissionDecision {
  verdict: Verdict;
  /** 实际用于矩阵查找的完整键（包含派生的来源关系）。 */
  key: PolicyKey;
  reason?: DenyReason;
}

/** 重定向链上的一跳。 */
export interface RedirectHop {
  /** 该跳请求的资源 URL（首跳即 initialUrl，其后为上一跳响应的 Location）。 */
  resourceUrl: string;
  declaration: ResourceDeclaration;
}

/** 重定向链评估输入。 */
export interface RedirectChainInput {
  documentOrigin: StructuredOrigin;
  resourceMode: ResourceMode;
  credentials: CredentialsMode;
  embedding: EmbeddingPolicy;
  /**
   * 按顺序给出每一跳；首跳 resourceUrl 即初始请求 URL，
   * 其后每跳的 resourceUrl 为上一跳响应的 Location。长度即跳数。
   * 每跳都针对“文档来源 → 当前资源来源”重新计算来源关系并重新评估。
   */
  hops: RedirectHop[];
}

/** 重定向链上某一跳的评估记录。 */
export interface HopEvaluation {
  hopIndex: number;
  resourceUrl: string;
  key: PolicyKey;
  verdict: Verdict;
}

/** 重定向链评估结果。 */
export interface RedirectChainDecision {
  verdict: Verdict;
  /** 逐跳记录，任一跳被拒即整体拒绝。 */
  evaluations: HopEvaluation[];
}

/**
 * 缓存条目：响应体绑定到其被获许时的完整安全上下文。
 * 任何一个安全上下文字段不同，缓存键即不同，不会命中同一缓存条目。
 */
export interface CacheEntry {
  resourceUrl: string;
  documentOrigin: StructuredOrigin;
  resourceMode: ResourceMode;
  credentials: CredentialsMode;
  embedding: EmbeddingPolicy;
  relationship: OriginRelationship;
  corp: ResourceDeclaration["corp"];
  corsAccess: boolean;
  verdict: Verdict;
}
