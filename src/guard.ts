import { ManualClock, SystemClock, type Clock } from './clock.ts';
import { FailureStreakTracker } from './failure-streak.ts';
import { FastHasher, type PasswordHasher } from './hasher.ts';
import { LockRegistry } from './lock-registry.ts';
import { RotatingSourceDetector, type DetectorSignal } from './detector.ts';
import { SlidingWindowRateLimiter } from './rate-limiter.ts';
import { InMemoryUserStore, type UserStore } from './store.ts';
import { normalizeAccount, runInConstantTime } from './timing.ts';
import {
  AuthOutcome,
  DEFAULT_CONFIG,
  DenyScope,
  InternalReason,
  RuleId,
  type AuthRequest,
  type AuthResponse,
  type AuthResult,
  type Decision,
  type GuardConfig,
  type LockEvent,
} from './types.ts';

/** 供不存在账号做同工作量校验的固定占位哈希（惰性初始化）。 */
const DUMMY_PASSWORD = 'guard-dummy-credential';

/**
 * 认证入口防护组件。
 *
 * 一条认证请求会依次经过：
 *   全局锁定 → 来源锁定 → 来源限速 → 账号锁定 → 账号限速
 *   → 凭证校验（恒定耗时、同构响应）→ 失败聚合检测（低速填充 / 喷洒）。
 *
 * 整个流程被恒定耗时包裹，配合不存在账号的占位哈希校验，
 * 使「账号不存在」与「账号存在但密码错误」在响应结构与耗时上无法区分。
 */
export class AuthGuard {
  private readonly config: GuardConfig;
  private readonly clock: Clock;
  private readonly hasher: PasswordHasher;
  private readonly store: UserStore;
  private readonly sourceLimiter: SlidingWindowRateLimiter;
  private readonly accountLimiter: SlidingWindowRateLimiter;
  private readonly locks: LockRegistry;
  private readonly streaks: FailureStreakTracker;
  private readonly detector: RotatingSourceDetector;
  private readonly events: LockEvent[] = [];
  private dummyHash: string | null = null;

  constructor(options: {
    config?: Partial<GuardConfig>;
    clock?: Clock;
    hasher?: PasswordHasher;
    store?: UserStore;
  } = {}) {
    this.config = { ...DEFAULT_CONFIG, ...options.config };
    this.clock = options.clock ?? new SystemClock();
    this.hasher = options.hasher ?? new FastHasher();
    this.store = options.store ?? new InMemoryUserStore(this.hasher);
    this.sourceLimiter = new SlidingWindowRateLimiter(
      this.clock,
      this.config.sourceWindowMs,
      this.config.sourceMaxAttempts,
    );
    this.accountLimiter = new SlidingWindowRateLimiter(
      this.clock,
      this.config.accountWindowMs,
      this.config.accountMaxAttempts,
    );
    this.locks = new LockRegistry(this.clock);
    this.streaks = new FailureStreakTracker();
    this.detector = new RotatingSourceDetector(this.clock, this.config);
  }

  /** 便捷构造：测试中使用手动时钟与快速哈希。 */
  static createForTest(config?: Partial<GuardConfig>): {
    guard: AuthGuard;
    clock: ManualClock;
  } {
    const clock = new ManualClock(1_000);
    const hasher = new FastHasher();
    const guard = new AuthGuard({ config, clock, hasher, store: new InMemoryUserStore(hasher) });
    return { guard, clock };
  }

  get userStore(): UserStore {
    return this.store;
  }

  private async getDummyHash(): Promise<string> {
    if (this.dummyHash === null) {
      this.dummyHash = await this.hasher.hash(DUMMY_PASSWORD);
    }
    return this.dummyHash;
  }

  /** 认证入口：恒定耗时 + 统一对外结构。 */
  async authenticate(raw: AuthRequest): Promise<AuthResponse> {
    const request: AuthRequest = {
      account: normalizeAccount(raw.account),
      password: raw.password,
      source: raw.source,
    };
    return runInConstantTime(this.clock, this.config.fixedAuthDurationMs, () =>
      this.process(request),
    );
  }

  private async process(request: AuthRequest): Promise<AuthResponse> {
    const { account, source } = request;

    const globalLock = this.locks.get(DenyScope.Global, '*');
    if (globalLock) {
      return this.respond(
        this.lockedDecision(InternalReason.GlobalLocked, globalLock),
        'service_locked',
        '认证服务暂时不可用',
      );
    }

    const sourceLock = this.locks.get(DenyScope.Source, source);
    if (sourceLock) {
      return this.respond(
        this.lockedDecision(InternalReason.Locked, sourceLock),
        'rate_limited',
        '请求过于频繁，请稍后再试',
      );
    }

    const sourceCheck = this.sourceLimiter.checkAndRecord(source);
    if (!sourceCheck.allowed) {
      const entry = this.locks.lock(
        DenyScope.Source,
        source,
        this.config.sourceDenyTtlMs,
        RuleId.SourceRateLimit,
        '来源维度滑动窗口限速触发',
        sourceCheck.evidence,
      );
      this.recordEvent(entry);
      return this.respond(
        {
          outcome: AuthOutcome.Rejected,
          reason: InternalReason.RateLimited,
          rule: RuleId.SourceRateLimit,
          scope: DenyScope.Source,
          subject: source,
          evidence: sourceCheck.evidence,
          retryAfterSeconds: Math.ceil(this.config.sourceDenyTtlMs / 1000),
          lockedUntil: entry.lockedUntil,
        },
        'rate_limited',
        '请求过于频繁，请稍后再试',
      );
    }

    const accountLock = this.locks.get(DenyScope.Account, account);
    if (accountLock) {
      return this.respond(
        this.lockedDecision(InternalReason.Locked, accountLock),
        'account_locked',
        '账号暂时无法登录，请稍后再试',
      );
    }

    const accountCheck = this.accountLimiter.checkAndRecord(account);
    if (!accountCheck.allowed) {
      const entry = this.locks.lock(
        DenyScope.Account,
        account,
        this.config.accountLockTtlMs,
        RuleId.AccountRateLimit,
        '账号维度滑动窗口限速触发',
        accountCheck.evidence,
      );
      this.recordEvent(entry);
      return this.respond(
        {
          outcome: AuthOutcome.Rejected,
          reason: InternalReason.RateLimited,
          rule: RuleId.AccountRateLimit,
          scope: DenyScope.Account,
          subject: account,
          evidence: accountCheck.evidence,
          retryAfterSeconds: Math.ceil(this.config.accountLockTtlMs / 1000),
          lockedUntil: entry.lockedUntil,
        },
        'account_locked',
        '账号暂时无法登录，请稍后再试',
      );
    }

    return this.verifyCredentials(request);
  }

  private async verifyCredentials(request: AuthRequest): Promise<AuthResponse> {
    const { account, password, source } = request;
    const storedHash = await this.store.getPasswordHash(account);
    const accountExists = storedHash !== null;

    // 账号不存在时，对一个固定占位哈希执行同样成本的校验，
    // 使该分支与「账号存在但密码错误」的工作量、耗时保持一致。
    const hashToCheck = accountExists ? storedHash : await this.getDummyHash();
    const passwordOk = await this.hasher.verify(password, hashToCheck);

    if (accountExists && passwordOk) {
      this.streaks.reset(account);
      return this.respond(
        { outcome: AuthOutcome.Success, reason: InternalReason.Authenticated },
        'ok',
        '认证成功',
      );
    }

    // 凭证错误：对存在 / 不存在账号返回完全相同的对外结构。
    const reason = accountExists
      ? InternalReason.BadCredentials
      : InternalReason.NoSuchAccount;
    const baseDecision: Decision = {
      outcome: AuthOutcome.Rejected,
      reason,
      evidence: { accountExists },
    };

    if (accountExists) {
      const { streak, thresholdReached } = this.streaks.recordFailure(
        account,
        this.config.accountLockoutThreshold,
      );
      if (thresholdReached) {
        const entry = this.locks.lock(
          DenyScope.Account,
          account,
          this.config.accountLockTtlMs,
          RuleId.AccountLockout,
          '账号连续失败达到阈值',
          {
            account,
            consecutiveFailures: streak,
            threshold: this.config.accountLockoutThreshold,
          },
        );
        this.recordEvent(entry);
        return this.respond(
          {
            outcome: AuthOutcome.Rejected,
            reason: InternalReason.Locked,
            rule: RuleId.AccountLockout,
            scope: DenyScope.Account,
            subject: account,
            evidence: entry.evidence,
            retryAfterSeconds: Math.ceil(this.config.accountLockTtlMs / 1000),
            lockedUntil: entry.lockedUntil,
          },
          'account_locked',
          '账号暂时无法登录，请稍后再试',
        );
      }
    }

    // 失败聚合：识别轮换来源的低速填充与单账号多来源喷洒。
    const signals = this.detector.observe(account, source, accountExists);
    for (const signal of signals) {
      this.applySignal(signal);
    }

    const postSignal = this.postDetectionDecision(signals, baseDecision);
    if (postSignal.reason === InternalReason.GlobalLocked) {
      return this.respond(postSignal, 'service_locked', '认证服务暂时不可用');
    }
    if (postSignal.reason === InternalReason.Locked) {
      return this.respond(postSignal, 'account_locked', '账号暂时无法登录，请稍后再试');
    }
    return this.respond(postSignal, 'invalid_credentials', '账号或密码错误');
  }

  /** 将检测信号落地为锁定（已锁定则不重复产生事件）。 */
  private applySignal(signal: DetectorSignal): void {
    const ttl =
      signal.scope === DenyScope.Global
        ? this.config.globalLockTtlMs
        : this.config.accountLockTtlMs;
    if (this.locks.get(signal.scope, signal.subject)) {
      return;
    }
    const entry = this.locks.lock(
      signal.scope,
      signal.subject,
      ttl,
      signal.rule,
      signal.reason,
      signal.evidence,
    );
    this.recordEvent(entry);
  }

  /** 若本次失败触发了全局 / 账号锁定，把内部判定升级为对应原因。 */
  private postDetectionDecision(signals: DetectorSignal[], base: Decision): Decision {
    const global = signals.find((signal) => signal.scope === DenyScope.Global);
    if (global) {
      const lock = this.locks.get(DenyScope.Global, '*');
      return {
        outcome: AuthOutcome.Rejected,
        reason: InternalReason.GlobalLocked,
        rule: global.rule,
        scope: DenyScope.Global,
        subject: '*',
        evidence: global.evidence,
        lockedUntil: lock?.lockedUntil,
      };
    }
    const account = signals.find((signal) => signal.scope === DenyScope.Account);
    if (account) {
      const lock = this.locks.get(DenyScope.Account, account.subject);
      return {
        outcome: AuthOutcome.Rejected,
        reason: InternalReason.Locked,
        rule: account.rule,
        scope: DenyScope.Account,
        subject: account.subject,
        evidence: account.evidence,
        lockedUntil: lock?.lockedUntil,
      };
    }
    return base;
  }

  private lockedDecision(
    reason: InternalReason,
    lock: ReturnType<LockRegistry['get']>,
  ): Decision {
    if (!lock) {
      throw new Error('lockedDecision requires an active lock entry');
    }
    const now = this.clock.now();
    return {
      outcome: AuthOutcome.Rejected,
      reason,
      rule: lock.rule,
      scope: lock.scope,
      subject: lock.subject,
      evidence: lock.evidence,
      retryAfterSeconds: Math.max(0, Math.ceil((lock.lockedUntil - now) / 1000)),
      lockedUntil: lock.lockedUntil,
    };
  }

  private recordEvent(entry: NonNullable<ReturnType<LockRegistry['get']>>): void {
    this.events.push({
      rule: entry.rule,
      scope: entry.scope,
      subject: entry.subject,
      reason: entry.reason,
      evidence: entry.evidence,
      at: entry.at,
      lockedUntil: entry.lockedUntil,
    });
  }

  private respond(decision: Decision, code: AuthResult['code'], message: string): AuthResponse {
    return {
      result: { outcome: decision.outcome, code, message },
      decision,
    };
  }

  // ---- 可观测性接口 ----

  /** 所有锁定事件（含已过期的历史事件），用于审计与告警。 */
  listLockEvents(): LockEvent[] {
    return this.events.map((event) => ({ ...event, evidence: { ...event.evidence } }));
  }

  /** 当前生效的锁定。 */
  activeLocks() {
    return this.locks.snapshot();
  }

  /** 防护组件整体观测快照。 */
  snapshot() {
    return {
      config: { ...this.config },
      activeLocks: this.locks.snapshot(),
      lockEvents: this.listLockEvents(),
      fillDetection: {
        global: this.detector.globalSnapshot(),
        accounts: this.detector.accountSnapshots(),
      },
    };
  }

  /** 手动解除某条锁定（运维使用）。 */
  unlock(scope: DenyScope, subject: string): void {
    this.locks.unlock(scope, subject);
  }

  /** 仅供测试读取的配置。 */
  getConfig(): GuardConfig {
    return { ...this.config };
  }
}
