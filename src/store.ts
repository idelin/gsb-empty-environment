/**
 * tx-store: 事务化版本状态存储（无第三方响应式框架）。
 *
 * 核心保证：
 * - 事务原子提交，读者只能看到已提交状态，永远看不到事务中间态。
 * - 提交后按确定顺序（订阅 id 升序）通知订阅者快照。
 * - 通知回调内再次写入进入下一轮，不影响当前轮迭代。
 * - 通知期间新增/取消订阅不影响当前轮快照迭代，自取消只影响后续轮。
 * - 订阅选择器与相等策略可动态更换，下一轮生效。
 * - 背压合并：积存的多个已提交版本合并为一轮通知，并附版本范围证据。
 * - 订阅者抛错不阻断其他订阅者，全部通知完成后聚合报告。
 * - 递归更新受循环预算（maxRounds）保护。
 * - 操作日志可回放，可从初始状态重建任意最终状态。
 */

export type Selector<S, T> = (state: S) => T;
export type Equality<T> = (a: T, b: T) => boolean;
export type Updater<S> = (draft: S) => void;

export interface Notification<T = unknown> {
  /** 本轮选中值（来自 toVersion 对应的已提交状态）。 */
  readonly value: T;
  /** 上一次选中值。 */
  readonly previous: T;
  /** 本通知对应的已提交版本（=== toVersion）。 */
  readonly version: number;
  /** 背压合并证据：本轮合并的版本区间下界。 */
  readonly fromVersion: number;
  /** 背压合并证据：本轮合并的版本区间上界。 */
  readonly toVersion: number;
  /** 本轮合并的提交数量（>1 表示发生了背压合并）。 */
  readonly mergedCommits: number;
  /** 全局单调递增的通知轮次号。 */
  readonly round: number;
}

export type Listener<T = unknown> = (notification: Notification<T>) => void;

export interface SubscriberError {
  readonly subscriberId: number;
  readonly round: number;
  readonly error: unknown;
}

/** 一个或多个订阅者抛错后的聚合报告。 */
export class NotificationAggregateError extends Error {
  readonly errors: readonly SubscriberError[];
  constructor(errors: readonly SubscriberError[]) {
    const ids = errors.map((e) => `#${e.subscriberId}@round${e.round}`).join(', ');
    super(`${errors.length} subscriber(s) threw during notification: ${ids}`);
    this.name = 'NotificationAggregateError';
    this.errors = errors;
  }
}

/** 递归更新超出循环预算时抛出，附未处理版本的证据。 */
export class LoopBudgetExceededError extends Error {
  readonly maxRounds: number;
  readonly pendingFromVersion: number;
  readonly pendingToVersion: number;
  constructor(maxRounds: number, pendingFromVersion: number, pendingToVersion: number) {
    super(
      `notification loop budget exceeded: ${maxRounds} rounds, ` +
        `pending versions ${pendingFromVersion}..${pendingToVersion} remain undelivered`,
    );
    this.name = 'LoopBudgetExceededError';
    this.maxRounds = maxRounds;
    this.pendingFromVersion = pendingFromVersion;
    this.pendingToVersion = pendingToVersion;
  }
}

export interface RoundReport {
  readonly round: number;
  readonly fromVersion: number;
  readonly toVersion: number;
  readonly mergedCommits: number;
  /** 本轮实际收到通知的订阅者 id（按通知顺序）。 */
  readonly notified: readonly number[];
}

export interface FlushReport {
  readonly rounds: readonly RoundReport[];
  readonly errors: readonly SubscriberError[];
}

export interface Subscription {
  readonly id: number;
  /** 取消订阅；对当前正在迭代的轮次无影响，只影响后续轮。 */
  unsubscribe(): void;
  /** 动态更换选择器（可一并更换监听器），下一轮生效；立即用当前已提交状态重定基线。 */
  setSelector<T2>(selector: Selector<never, T2>, listener?: Listener<T2>): void;
  /** 动态更换相等策略，下一轮比较生效。 */
  setEquality(equality: Equality<never>): void;
  isActive(): boolean;
}

export interface StoreOptions<S> {
  initialState: S;
  /** 单次 flush 允许的最大轮数，超出抛 LoopBudgetExceededError。默认 32。 */
  maxRounds?: number;
  /** 提交后是否立即同步通知。默认 true；false 时需手动 flush()，便于观察背压合并。 */
  autoFlush?: boolean;
  /** 设置后，聚合错误交给该回调而不是从 flush 抛出。 */
  onNotificationError?: (error: NotificationAggregateError) => void;
}

interface SubscriberRecord<S> {
  id: number;
  selector: Selector<S, unknown>;
  equality: Equality<unknown>;
  listener: Listener<unknown>;
  active: boolean;
  lastValue: unknown;
}

interface PendingCommit<S> {
  version: number;
  state: S;
  label?: string;
}

interface OpLogEntry<S> {
  version: number;
  label?: string;
  apply: Updater<S>;
}

function deepFreeze<T>(value: T, seen = new Set<object>()): T {
  if (value !== null && typeof value === 'object' && !seen.has(value)) {
    seen.add(value);
    if (!Object.isFrozen(value)) Object.freeze(value);
    for (const key of Object.keys(value)) {
      deepFreeze((value as Record<string, unknown>)[key], seen);
    }
  }
  return value;
}

export class Store<S> {
  #committed: S;
  #initial: S;
  #version = 0;
  #round = 0;
  #pending: PendingCommit<S>[] = [];
  #subscribers = new Map<number, SubscriberRecord<S>>();
  #nextSubId = 1;
  #flushing = false;
  #inTransaction = false;
  #opLog: OpLogEntry<S>[] = [];
  readonly maxRounds: number;
  readonly autoFlush: boolean;
  onNotificationError?: (error: NotificationAggregateError) => void;

  constructor(options: StoreOptions<S>) {
    this.#initial = structuredClone(options.initialState);
    this.#committed = deepFreeze(structuredClone(options.initialState));
    this.maxRounds = options.maxRounds ?? 32;
    this.autoFlush = options.autoFlush ?? true;
    this.onNotificationError = options.onNotificationError;
  }

  /** 当前已提交版本号（从 1 开始递增，无重复、无回退）。 */
  get version(): number {
    return this.#version;
  }

  /** 当前通知轮次号。 */
  get currentRound(): number {
    return this.#round;
  }

  /** 已提交但尚未通知的版本数（背压证据）。 */
  get pendingCount(): number {
    return this.#pending.length;
  }

  /** 只读访问已提交状态（深冻结）。事务进行中读取到的仍是上一个已提交状态。 */
  getState(): S {
    return this.#committed;
  }

  /**
   * 原子事务：在草稿上执行 updater，全部成功后一次性提交并递增版本；
   * updater 抛错则整体丢弃，版本不变、无通知。事务内不允许嵌套 commit。
   */
  commit(updater: Updater<S>): { version: number };
  commit(label: string, updater: Updater<S>): { version: number };
  commit(labelOrUpdater: string | Updater<S>, maybeUpdater?: Updater<S>): { version: number } {
    if (this.#inTransaction) {
      throw new Error('nested commit inside a transaction updater is not allowed');
    }
    const label = typeof labelOrUpdater === 'string' ? labelOrUpdater : undefined;
    const updater = (typeof labelOrUpdater === 'function' ? labelOrUpdater : maybeUpdater)!;
    this.#inTransaction = true;
    let draft: S;
    try {
      draft = structuredClone(this.#committed);
      updater(draft);
    } catch (error) {
      this.#inTransaction = false;
      throw error; // 原子回滚：无版本、无通知
    }
    this.#inTransaction = false;
    const committed = deepFreeze(draft);
    this.#committed = committed;
    this.#version += 1;
    const version = this.#version;
    this.#opLog.push({ version, label, apply: updater });
    this.#pending.push({ version, state: committed, label });
    if (this.autoFlush && !this.#flushing) {
      this.flush();
    }
    return { version };
  }

  /**
   * 订阅。返回句柄可取消、可动态更换选择器/相等策略。
   * 通知顺序按订阅 id 升序，确定可复现。
   */
  subscribe<T>(
    selector: Selector<S, T>,
    listener: Listener<T>,
    options: { equality?: Equality<T> } = {},
  ): Subscription {
    const id = this.#nextSubId++;
    const record: SubscriberRecord<S> = {
      id,
      selector: selector as Selector<S, unknown>,
      equality: (options.equality ?? Object.is) as Equality<unknown>,
      listener: listener as Listener<unknown>,
      active: true,
      lastValue: selector(this.#committed), // 基线：订阅时刻的已提交状态
    };
    this.#subscribers.set(id, record);
    const store = this;
    return {
      id,
      unsubscribe() {
        store.#subscribers.delete(id);
      },
      setSelector<T2>(next: Selector<never, T2>, nextListener?: Listener<T2>) {
        const rec = store.#subscribers.get(id);
        if (!rec) return;
        const rebased = (next as Selector<S, T2>)(store.#committed); // 重定基线，避免伪变化
        rec.selector = next as Selector<S, unknown>;
        if (nextListener) rec.listener = nextListener as Listener<unknown>;
        rec.lastValue = rebased;
      },
      setEquality(next: Equality<never>) {
        const rec = store.#subscribers.get(id);
        if (!rec) return;
        rec.equality = next as Equality<unknown>;
      },
      isActive() {
        return store.#subscribers.has(id);
      },
    };
  }

  /**
   * 处理所有待通知版本。积存的多个提交合并为一轮（背压合并），
   * 通知回调内的新提交进入下一轮。全部轮次完成后若有订阅者错误则聚合报告。
   */
  flush(): FlushReport {
    if (this.#flushing) {
      return { rounds: [], errors: [] }; // 重入 flush：由外层循环继续
    }
    this.#flushing = true;
    const rounds: RoundReport[] = [];
    const errors: SubscriberError[] = [];
    try {
      while (this.#pending.length > 0) {
        if (rounds.length >= this.maxRounds) {
          throw new LoopBudgetExceededError(
            this.maxRounds,
            this.#pending[0].version,
            this.#pending[this.#pending.length - 1].version,
          );
        }
        const batch = this.#pending.splice(0); // 背压合并：一轮吃掉全部积存版本
        this.#round += 1;
        const roundNo = this.#round;
        const state = batch[batch.length - 1].state;
        const fromVersion = batch[0].version;
        const toVersion = batch[batch.length - 1].version;
        // 当前轮快照：通知期间的新增/取消不影响本轮迭代
        const snapshot = [...this.#subscribers.values()].sort((a, b) => a.id - b.id);
        const notified: number[] = [];
        for (const sub of snapshot) {
          let value: unknown;
          try {
            value = sub.selector(state);
          } catch (error) {
            errors.push({ subscriberId: sub.id, round: roundNo, error });
            continue;
          }
          const previous = sub.lastValue;
          if (sub.equality(previous, value)) continue; // 相等策略抑制伪变化
          sub.lastValue = value; // 先更新基线：抛错的监听器不会被无限重试
          notified.push(sub.id);
          try {
            sub.listener({ value, previous, version: toVersion, fromVersion, toVersion, mergedCommits: batch.length, round: roundNo });
          } catch (error) {
            errors.push({ subscriberId: sub.id, round: roundNo, error }); // 不阻断其他订阅者
          }
        }
        rounds.push({ round: roundNo, fromVersion, toVersion, mergedCommits: batch.length, notified });
      }
    } finally {
      this.#flushing = false;
    }
    const report: FlushReport = { rounds, errors };
    if (errors.length > 0) {
      const aggregate = new NotificationAggregateError(errors);
      if (this.onNotificationError) {
        this.onNotificationError(aggregate);
      } else {
        throw aggregate;
      }
    }
    return report;
  }

  /** 从初始状态回放全部操作日志，重建最终状态（用于核验与审计）。 */
  replay(): S {
    let state = structuredClone(this.#initial);
    for (const entry of this.#opLog) {
      entry.apply(state);
    }
    return state;
  }

  /** 操作日志证据（版本号与标签，不含函数）。 */
  opLog(): ReadonlyArray<{ version: number; label?: string }> {
    return this.#opLog.map(({ version, label }) => ({ version, label }));
  }
}
