export type Observer<T> = (state: T, version: number) => void;

export interface ObserverErrorInfo<T> {
  observer: Observer<T>;
  state: T;
  version: number;
}

export interface StoreOptions<T> {
  onObserverError?: (error: unknown, info: ObserverErrorInfo<T>) => void;
  maxDispatchRounds?: number;
}

interface ObserverEntry<T> {
  fn: Observer<T>;
  lastSeen: number;
}

const DEFAULT_MAX_DISPATCH_ROUNDS = 100_000;

/**
 * 可重入的状态观察库。
 *
 * 核心语义:
 * - 每次提交的修改使版本号 +1;`batch` 内的多次修改合并为一次版本递增。
 * - 通知通过迭代式派发循环完成:观察器在回调中再次修改状态时,
 *   只是将新版本入队,由当前循环的后续轮次投递,不会递归调用,不会栈溢出。
 * - 每名观察器记录自己已见的版本号,只会收到严格递增的版本。
 * - 合并规则:若观察器落后多个版本,只投递最新的 (state, version),
 *   中间版本被跳过(折叠),同批修改按"后写覆盖先写"合并。
 * - 通知期间新增的观察器从当前版本起算,不会收到当前及历史版本的回放。
 * - 退订立即生效(包括派发中途),且幂等。
 * - 观察器抛出的异常被隔离,路由到 onObserverError,不影响其他观察器和派发流程。
 */
export class Store<T> {
  private state: T;
  private currentVersion = 0;
  private readonly observers = new Map<number, ObserverEntry<T>>();
  private nextObserverId = 0;
  private batchDepth = 0;
  private dirty = false;
  private dispatching = false;
  private readonly onObserverError: (error: unknown, info: ObserverErrorInfo<T>) => void;
  private readonly maxDispatchRounds: number;

  constructor(initialState: T, options: StoreOptions<T> = {}) {
    this.state = initialState;
    this.onObserverError =
      options.onObserverError ??
      ((error) => {
        console.error("[Store] observer threw:", error);
      });
    this.maxDispatchRounds = options.maxDispatchRounds ?? DEFAULT_MAX_DISPATCH_ROUNDS;
  }

  get(): T {
    return this.state;
  }

  getVersion(): number {
    return this.currentVersion;
  }

  get observerCount(): number {
    return this.observers.size;
  }

  set(next: T): void {
    this.state = next;
    if (this.batchDepth > 0) {
      this.dirty = true;
      return;
    }
    this.currentVersion += 1;
    this.dispatch();
  }

  update(updater: (state: T) => T): void {
    this.set(updater(this.state));
  }

  /**
   * 合并规则:batch 内的所有 set/update 直接作用于状态(后写覆盖先写,
   * update 基于最新状态组合),整个 batch 只产生一次版本递增和一轮通知。
   * 注意:batch 不是事务,fn 抛异常时已应用的修改仍会提交。
   */
  batch(fn: () => void): void {
    this.batchDepth += 1;
    try {
      fn();
    } finally {
      this.batchDepth -= 1;
      if (this.batchDepth === 0 && this.dirty) {
        this.dirty = false;
        this.currentVersion += 1;
        this.dispatch();
      }
    }
  }

  /**
   * 订阅状态变化。返回幂等的退订函数。
   * 新订阅者从当前版本起算,不回放历史版本。
   */
  subscribe(fn: Observer<T>): () => void {
    const id = this.nextObserverId++;
    this.observers.set(id, { fn, lastSeen: this.currentVersion });
    let active = true;
    return () => {
      if (!active) return;
      active = false;
      this.observers.delete(id);
    };
  }

  private dispatch(): void {
    if (this.dispatching) return; // 外层循环会拾取新版本
    this.dispatching = true;
    try {
      let rounds = 0;
      for (;;) {
        let delivered = false;
        for (const [id, entry] of [...this.observers]) {
          if (this.observers.get(id) !== entry) continue; // 派发中途已退订
          if (entry.lastSeen >= this.currentVersion) continue;
          entry.lastSeen = this.currentVersion;
          delivered = true;
          try {
            entry.fn(this.state, this.currentVersion);
          } catch (error) {
            this.onObserverError(error, {
              observer: entry.fn,
              state: this.state,
              version: this.currentVersion,
            });
          }
        }
        if (!delivered) return;
        rounds += 1;
        if (rounds >= this.maxDispatchRounds) {
          throw new RangeError(
            `[Store] exceeded maxDispatchRounds (${this.maxDispatchRounds}); ` +
              "an observer is probably mutating state unconditionally",
          );
        }
      }
    } finally {
      this.dispatching = false;
    }
  }
}
