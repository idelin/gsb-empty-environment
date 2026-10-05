/**
 * KeyedExecutor — 按键串行、跨键共享有限并发额度的作业执行器。
 *
 * 语义：
 * - 相同 key 的作业严格串行（FIFO）；不同 key 的作业共享全局并发额度。
 * - 作业失败/异常不会阻塞同 key 的后继作业，也不会占住并发额度。
 * - 排队中（未开始）的作业可以取消；已运行或已结束的作业取消返回 false。
 * - 某个 key 既无运行中也无排队作业时，其内部状态立即被释放。
 * - close() 之后的新提交（包括运行中作业的递归提交）以 ExecutorClosedError 拒绝，
 *   不计入待处理数，因此关闭流程一定能收敛。
 */

export type JobState =
  | 'queued'
  | 'running'
  | 'completed'
  | 'failed'
  | 'cancelled'
  | 'rejected';

export class CancelledError extends Error {
  constructor(message = 'Job was cancelled while queued') {
    super(message);
    this.name = 'CancelledError';
  }
}

export class ExecutorClosedError extends Error {
  constructor(message = 'Executor is closed and no longer accepts jobs') {
    super(message);
    this.name = 'ExecutorClosedError';
  }
}

export interface JobHandle<T> {
  readonly key: string;
  readonly promise: Promise<T>;
  readonly state: JobState;
  /** 仅对排队中的作业有效；返回是否成功取消。 */
  cancel(): boolean;
}

export interface KeyedExecutorOptions {
  /** 全局并发额度（跨所有 key 共享），默认 4。 */
  maxConcurrency?: number;
}

interface QueueEntry {
  state: JobState;
  owner: KeyState | null;
  run: () => unknown;
  resolve: (value: any) => void;
  reject: (reason: any) => void;
}

class KeyState {
  readonly queue: QueueEntry[] = [];
  running = false;
  /** 是否已在就绪队列中（等待全局并发额度）。 */
  enqueued = false;
  readonly key: string;
  constructor(key: string) {
    this.key = key;
  }
}

class Handle<T> implements JobHandle<T> {
  readonly key: string;
  readonly promise: Promise<T>;
  private readonly entry: QueueEntry;
  private readonly executor: KeyedExecutor;
  constructor(key: string, promise: Promise<T>, entry: QueueEntry, executor: KeyedExecutor) {
    this.key = key;
    this.promise = promise;
    this.entry = entry;
    this.executor = executor;
  }
  get state(): JobState {
    return this.entry.state;
  }
  cancel(): boolean {
    return this.executor.cancelEntry(this.entry);
  }
}

export class KeyedExecutor {
  private readonly maxConcurrency: number;
  private readonly keys = new Map<string, KeyState>();
  /** 就绪 key 的 FIFO 队列：保证跨 key 的确定性轮转调度。 */
  private readonly ready: KeyState[] = [];
  private runningJobs = 0;
  private pendingJobs = 0;
  private pumping = false;
  private closedFlag = false;
  private closePromise: Promise<void> | null = null;
  private idleResolve: (() => void) | null = null;

  constructor(options: KeyedExecutorOptions = {}) {
    const max = options.maxConcurrency ?? 4;
    if (!Number.isInteger(max) || max < 1) {
      throw new RangeError('maxConcurrency must be a positive integer');
    }
    this.maxConcurrency = max;
  }

  get isClosed(): boolean {
    return this.closedFlag;
  }
  get runningCount(): number {
    return this.runningJobs;
  }
  get pendingCount(): number {
    return this.pendingJobs;
  }
  /** 当前仍持有状态的 key 数（用于验证空闲 key 状态已释放）。 */
  get keyCount(): number {
    return this.keys.size;
  }

  submit<T>(key: string, job: () => T | Promise<T>): JobHandle<T> {
    let resolve!: (value: T | PromiseLike<T>) => void;
    let reject!: (reason?: any) => void;
    const promise = new Promise<T>((res, rej) => {
      resolve = res;
      reject = rej;
    });
    // 内部挂一个空拒绝处理器，避免 fire-and-forget 场景触发 unhandledRejection；
    // 调用方仍可通过 handle.promise 正常观察到拒绝。
    promise.catch(() => {});

    const entry: QueueEntry = { state: 'queued', owner: null, run: job, resolve, reject };

    if (this.closedFlag) {
      // 关闭期间（含运行中作业的递归提交）一律拒绝，且不计入 pending，
      // 保证 close() 的排空条件必然可达。
      entry.state = 'rejected';
      reject(new ExecutorClosedError());
      return new Handle(key, promise, entry, this);
    }

    let state = this.keys.get(key);
    if (!state) {
      state = new KeyState(key);
      this.keys.set(key, state);
    }
    entry.owner = state;
    state.queue.push(entry);
    this.pendingJobs++;
    this.markReady(state);
    this.pump();
    return new Handle(key, promise, entry, this);
  }

  /**
   * 停止接收新作业，等待已排队与运行中的作业全部结束后 resolve。
   * 重复调用返回同一个 Promise。
   */
  close(): Promise<void> {
    if (!this.closePromise) {
      this.closedFlag = true;
      this.closePromise = new Promise<void>((resolve) => {
        if (this.pendingJobs === 0) {
          resolve();
        } else {
          this.idleResolve = resolve;
        }
      });
    }
    return this.closePromise;
  }

  /** @internal 由 JobHandle.cancel 调用。 */
  cancelEntry(entry: QueueEntry): boolean {
    if (entry.state !== 'queued') return false;
    entry.state = 'cancelled';
    const state = entry.owner;
    if (state) {
      const index = state.queue.indexOf(entry);
      if (index >= 0) state.queue.splice(index, 1);
      this.releaseKeyIfIdle(state);
    }
    this.pendingJobs--;
    entry.reject(new CancelledError());
    this.signalIdleIfDrained();
    return true;
  }

  private markReady(state: KeyState): void {
    if (!state.running && !state.enqueued && state.queue.length > 0) {
      state.enqueued = true;
      this.ready.push(state);
    }
  }

  private pump(): void {
    if (this.pumping) return; // 重入保护：外层循环会拾取新就绪的 key
    this.pumping = true;
    try {
      while (this.runningJobs < this.maxConcurrency && this.ready.length > 0) {
        const state = this.ready.shift()!;
        state.enqueued = false;
        if (state.running || state.queue.length === 0) continue;
        const entry = state.queue.shift()!;
        this.startJob(state, entry);
      }
    } finally {
      this.pumping = false;
    }
  }

  private startJob(state: KeyState, entry: QueueEntry): void {
    entry.state = 'running';
    state.running = true;
    this.runningJobs++;
    let result: Promise<unknown>;
    try {
      // 同步执行的 job 内部若再 submit 同 key 作业，只会入队（running=true），
      // 不会与本作业并发，也不会造成无限递归。
      result = Promise.resolve(entry.run());
    } catch (error) {
      result = Promise.reject(error);
    }
    result.then(
      (value) => this.settle(state, entry, true, value),
      (error) => this.settle(state, entry, false, error),
    );
  }

  private settle(state: KeyState, entry: QueueEntry, ok: boolean, payload: unknown): void {
    state.running = false;
    this.runningJobs--;
    if (ok) {
      entry.state = 'completed';
      entry.resolve(payload);
    } else {
      entry.state = 'failed';
      entry.reject(payload);
    }
    // 同步完成清理：用户的 then 回调（微任务）运行时，空闲 key 状态必然已释放，
    // 回调里对同 key 的新提交会看到干净的 Map 并重建状态，不会挂在僵尸状态上。
    // 队列仍有后继作业时，把 key 重新挂回就绪队列尾部（跨 key 轮转）。
    this.markReady(state);
    this.releaseKeyIfIdle(state);
    this.pendingJobs--;
    this.pump();
    this.signalIdleIfDrained();
  }

  private releaseKeyIfIdle(state: KeyState): void {
    if (!state.running && state.queue.length === 0) {
      this.keys.delete(state.key);
    }
  }

  private signalIdleIfDrained(): void {
    if (this.pendingJobs === 0 && this.idleResolve) {
      const resolve = this.idleResolve;
      this.idleResolve = null;
      resolve();
    }
  }
}
