/**
 * 认证防护组件的共享类型定义。
 *
 * 设计原则：
 * - 对外响应（AuthResult）对「账号不存在」与「密码错误」保持同构，避免账号枚举；
 * - 真正的判定依据放在内部 Decision / LockEvent 中，仅用于日志与可观测性。
 */

/** 触发防护的规则标识。 */
export enum RuleId {
  /** 来源（IP 等）维度滑动窗口限速。 */
  SourceRateLimit = 'source_rate_limit',
  /** 账号维度滑动窗口限速。 */
  AccountRateLimit = 'account_rate_limit',
  /** 账号连续失败次数达到阈值。 */
  AccountLockout = 'account_lockout',
  /** 轮换来源的低速填充：全局唯一来源数 + 失败量达到阈值。 */
  RotatingSourceLowSlowFill = 'rotating_source_low_slow_fill',
  /** 单账号被多个不同来源尝试（口令喷洒 / 撞库）。 */
  AccountSourceSpray = 'account_source_spray',
}

/** 锁定的作用范围。 */
export enum DenyScope {
  Source = 'source',
  Account = 'account',
  Global = 'global',
}

/** 对外返回的认证结论，不区分账号是否存在。 */
export enum AuthOutcome {
  Success = 'success',
  Rejected = 'rejected',
}

/** 内部判定规则（不随对外响应直接返回）。 */
export enum InternalReason {
  /** 认证成功。 */
  Authenticated = 'authenticated',
  NoSuchAccount = 'no_such_account',
  BadCredentials = 'bad_credentials',
  Locked = 'locked',
  RateLimited = 'rate_limited',
  GlobalLocked = 'global_locked',
}

/** 证据字段：任意可序列化的计数、阈值与窗口信息。 */
export type Evidence = Record<string, string | number | boolean>;

/** 一次锁定 / 拦截事件的可观测记录。 */
export interface LockEvent {
  rule: RuleId;
  scope: DenyScope;
  /** 被锁定的主体：来源标识、账号，或全局标记 '*'。 */
  subject: string;
  reason: string;
  evidence: Evidence;
  /** 事件产生时间（毫秒）。 */
  at: number;
  /** 锁定截止时间（毫秒）；0 表示无显式截止。 */
  lockedUntil: number;
}

/** 进入认证入口的请求。 */
export interface AuthRequest {
  /** 账号（内部会做 trim + 小写归一化）。 */
  account: string;
  password: string;
  /** 来源标识，例如客户端 IP。 */
  source: string;
}

/**
 * 对外认证结果。
 * 账号不存在与密码错误都返回同构的 rejected/invalid_credentials。
 */
export interface AuthResult {
  outcome: AuthOutcome;
  code: 'ok' | 'invalid_credentials' | 'rate_limited' | 'account_locked' | 'service_locked';
  /** 命中防护时附带的可读原因；凭证错误时保持通用文案。 */
  message: string;
}

/** 内部判定详情，供日志 / 指标采集使用。 */
export interface Decision {
  outcome: AuthOutcome;
  reason: InternalReason;
  rule?: RuleId;
  scope?: DenyScope;
  subject?: string;
  evidence?: Evidence;
  retryAfterSeconds?: number;
  lockedUntil?: number;
}

/** 认证响应：对外结果 + 内部判定。 */
export interface AuthResponse {
  result: AuthResult;
  decision: Decision;
}

/** 防护组件配置。 */
export interface GuardConfig {
  /** 来源维度：窗口内最大尝试次数。 */
  sourceMaxAttempts: number;
  /** 来源维度：滑动窗口长度（毫秒）。 */
  sourceWindowMs: number;
  /** 账号维度：窗口内最大尝试次数。 */
  accountMaxAttempts: number;
  /** 账号维度：滑动窗口长度（毫秒）。 */
  accountWindowMs: number;
  /** 账号连续失败多少次后锁定。 */
  accountLockoutThreshold: number;
  /** 账号锁定时长（毫秒）。 */
  accountLockTtlMs: number;
  /** 来源被限速后加入拒绝清单的时长（毫秒）。 */
  sourceDenyTtlMs: number;
  /** 低速填充检测窗口（毫秒）。 */
  fillWindowMs: number;
  /** 窗口内唯一来源数达到该值即视为轮换来源。 */
  fillDistinctSources: number;
  /** 窗口内失败次数达到该值视为填充行为。 */
  fillMinFailures: number;
  /** 触发全局锁定后的持续时间（毫秒）。 */
  globalLockTtlMs: number;
  /** 单账号被多少个不同来源失败尝试后锁定账号。 */
  accountSprayDistinctSources: number;
  /** 凭证校验路径的固定耗时基线（毫秒），用于同构耗时。 */
  fixedAuthDurationMs: number;
}

export const DEFAULT_CONFIG: GuardConfig = {
  sourceMaxAttempts: 10,
  sourceWindowMs: 60_000,
  accountMaxAttempts: 5,
  accountWindowMs: 60_000,
  accountLockoutThreshold: 8,
  accountLockTtlMs: 15 * 60_000,
  sourceDenyTtlMs: 5 * 60_000,
  fillWindowMs: 10 * 60_000,
  fillDistinctSources: 20,
  fillMinFailures: 30,
  globalLockTtlMs: 30 * 60_000,
  accountSprayDistinctSources: 8,
  fixedAuthDurationMs: 120,
};
