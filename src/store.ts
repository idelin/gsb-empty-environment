/**
 * 可重入的状态观察库。
 *
 * 核心语义：
 * - 每次 `set` / `update` 提交一个新版本（version 严格递增），并进入待通知队列。
 * - 通知通过迭代式 drain 循环派发，观察器回调中再次修改状态只会向队列追加
 *   变更，不会递归调用 —— 因此任意深度的递归修改都不会栈溢出。
 * - 合并规则（明确约定）：
 *   1. 一轮 drain 处理队列中当前积压的全部变更，称为一个“批次”。
 *   2. 同一批次内，每名观察器只收到一次通知：notification.changes 按版本
 *      顺序包含该批次中所有它尚未见过的变更，state / version 为批次最新值。
 *   3. 回调期间提交的新变更进入下一批次，不并入正在派发的批次。
 *   4. 每名观察器只见到的版本严格递增（只投递 version > 其已见版本的变更）。
 *   5. 通知期间新增的观察器从订阅时刻的版本开始，收不到在途批次。
 *   6. 取消订阅立即生效：即使批次正在派发中，被移除的观察器也会被跳过。
 * - 异常隔离：单个观察器抛错不影响其他观察器；错误按 onError 上报，
 *   未提供 onError 时在 drain 结束后以 AggregateError 从顶层修改调用抛出。
 */

export interface Change<T> {
  /** 该变更提交的版本号 */
  readonly version: number;
  /** 变更前的状态 */
  readonly previous: T;
  /** 变更后的状态 */
  readonly next: T;
  /** 可选的动作标签，便于调试/日志 */
  readonly action?: string;
}

export interface Notification<T> {
  /** 本批次最新状态 */
  readonly state: T;
  /** 本批次最新版本号（对同一观察器严格递增） */
  readonly version: number;
  /** 该观察器上一次见到的版本号 */
  readonly sinceVersion: number;
  /** 本批次合并的全部变更，按版本升序 */
  readonly changes: readonly Change<T>[];
}

export type Listener<T> = (notification: Notification<T>) => void;

export type Unsubscribe = () => void;

export interface StoreOptions<T> {
  /** 观察器异常上报回调；不提供时错误会在 drain 结束后聚合抛出 */
  onError?: (error: unknown, listener: Listener<T>) => void;
}

interface ObserverEntry<T> {
  readonly listener: Listener<T>;
  /** 该观察器已投递到的版本号 */
  sinceVersion: number;
}

export class Store<T> {
  private state: T;
  private version = 0;
  private readonly observers = new Map<number, ObserverEntry<T>>();
  private nextObserverId = 1;
  /** 已提交但尚未派发的变更队列 */
  private pending: Change<T>[] = [];
  private draining = false;
  private batchDepth = 0;
  private drainErrors: { error: unknown; listener: Listener<T> }[] = [];
  private readonly onError?: (error: unknown, listener: Listener<T>) => void;

  constructor(initialState: T, options: StoreOptions<T> = {}) {
    this.state = initialState;
    this.onError = options.onError;
  }

  getState(): T {
    return this.state;
  }

  getVersion(): number {
    return this.version;
  }

  getObserverCount(): number {
    return this.observers.size;
  }

  /**
   * 订阅状态变化。新观察器从当前版本开始观察，不会收到订阅前的变更。
   * 返回取消订阅函数，调用后立即生效（含派发中的批次）。
   */
  subscribe(listener: Listener<T>): Unsubscribe {
    const id = this.nextObserverId++;
    this.observers.set(id, { listener, sinceVersion: this.version });
    return () => {
      this.observers.delete(id);
    };
  }

  /** 提交新状态，版本号 +1。 */
  set(next: T, action?: string): void {
    this.commit({ version: this.version + 1, previous: this.state, next, action });
  }

  /** 以 updater 函数提交新状态。 */
  update(updater: (state: T) => T, action?: string): void {
    this.set(updater(this.state), action);
  }

  /**
   * 显式批量修改：fn 内的所有提交合并为一个批次，
   * 每名观察器只收到一次合并通知。支持嵌套。
   */
  batch(fn: () => void): void {
    this.batchDepth++;
    try {
      fn();
    } finally {
      this.batchDepth--;
      if (this.batchDepth === 0) {
        this.flush();
      }
    }
  }

  private commit(change: Change<T>): void {
    this.state = change.next;
    this.version = change.version;
    this.pending.push(change);
    if (this.batchDepth === 0) {
      this.flush();
    }
  }

  /**
   * 顶层入口：驱动 drain 循环。若已在派发中（重入调用），
   * 变更已入队，直接返回，由外层循环继续处理 —— 这是避免栈溢出的关键。
   */
  private flush(): void {
    if (this.draining) {
      return;
    }
    this.draining = true;
    try {
      // 迭代而非递归：回调中的重入修改只是向 pending 追加元素。
      while (this.pending.length > 0) {
        const batchChanges = this.pending;
        this.pending = [];
        this.deliverBatch(batchChanges);
      }
    } finally {
      this.draining = false;
    }
    if (this.drainErrors.length > 0) {
      const errors = this.drainErrors;
      this.drainErrors = [];
      if (this.onError) {
        for (const { error, listener } of errors) {
          this.onError(error, listener);
        }
      } else {
        throw new AggregateError(
          errors.map((entry) => entry.error),
          `${errors.length} observer(s) threw during notification`,
        );
      }
    }
  }

  private deliverBatch(changes: Change<T>[]): void {
    const latest = changes[changes.length - 1];
    const latestVersion = latest.version;
    // 状态取自本批次最后一个变更，而非 this.state —— 派发期间的重入修改
    // 可能已推进全局状态，本批次观察器应看到与 version 一致的快照。
    const latestState = latest.next;
    // 快照当前观察器列表：派发期间新增的观察器不参与本批次。
    const entries = [...this.observers.entries()];
    for (const [id, entry] of entries) {
      // 取消订阅立即生效：派发中途被移除的观察器跳过。
      if (!this.observers.has(id)) {
        continue;
      }
      const unseen = changes.filter((c) => c.version > entry.sinceVersion);
      if (unseen.length === 0) {
        continue;
      }
      // 先推进水位再回调：保证该观察器所见版本严格递增，
      // 且其回调中的重入修改不会重复投递同一版本。
      const sinceVersion = entry.sinceVersion;
      entry.sinceVersion = latestVersion;
      const notification: Notification<T> = {
        state: latestState,
        version: latestVersion,
        sinceVersion,
        changes: unseen,
      };
      try {
        entry.listener(notification);
      } catch (error) {
        // 异常隔离：记录后继续通知其余观察器。
        this.drainErrors.push({ error, listener: entry.listener });
      }
    }
  }
}
