/**
 * 生成器轮转库：多个生成器共享一条输出通道，每轮各取一项。
 *
 * 语义：
 * - 轮转：每一轮从每个“活跃且未暂停”的源各取恰好一项，按加入顺序交错输出。
 * - 背压：纯拉模式。消费者不调用 next()，就不会对任何源发起新的拉取；
 *   任意时刻每个源最多只有一个未完成的拉取（本轮预取的那一次）。
 * - 暂停/恢复：暂停立即生效。若暂停时该源有一个进行中的拉取，拉取结果会被
 *   缓冲而不是丢弃，恢复后在下一轮优先发出（期间不会发起新拉取）。
 * - 异常隔离：某个源抛错只影响它自己——该源被移除，错误经 onSourceError 上报，
 *   其余源继续轮转。
 * - 提前结束：消费者 break / return() / throw() 时，通道等待所有未完成拉取
 *   落定，并调用每个源的 return() 做清理。
 */

export type SourceInput<T> =
  | AsyncIterable<T>
  | Iterable<T>
  | AsyncIterator<T>
  | Iterator<T>;

export interface RoundRobinChannelOptions {
  /** 某个源抛错时回调（错误已被隔离，不会影响其他源）。 */
  onSourceError?: (error: unknown, sourceId: number) => void;
}

/** 一次拉取的落定结果。内部把拉取 Promise 规范化，保证永不 reject。 */
type PullOutcome<T> =
  | { ok: true; result: IteratorResult<T> }
  | { ok: false; error: unknown };

interface SourceState<T> {
  id: number;
  iterator: AsyncIterator<T>;
  /** 进行中的拉取；不变量：任意时刻最多一个，且与 buffered 互斥。 */
  pending: Promise<PullOutcome<T>> | null;
  /** 暂停期间完成的拉取结果，恢复后优先发出。 */
  buffered: PullOutcome<T> | null;
  paused: boolean;
  done: boolean;
}

function toAsyncIterator<T>(source: SourceInput<T>): AsyncIterator<T> {
  const asAsyncIterable = source as AsyncIterable<T>;
  if (typeof asAsyncIterable[Symbol.asyncIterator] === "function") {
    return asAsyncIterable[Symbol.asyncIterator]();
  }
  const asIterable = source as Iterable<T>;
  if (typeof asIterable[Symbol.iterator] === "function") {
    const iterator = asIterable[Symbol.iterator]();
    return {
      next: (...args: unknown[]) =>
        Promise.resolve(iterator.next(...(args as []))),
      ...(typeof iterator.return === "function"
        ? {
            return: (value?: unknown) =>
              Promise.resolve(iterator.return!(value)),
          }
        : {}),
    };
  }
  const asIterator = source as AsyncIterator<T>;
  if (typeof asIterator.next === "function") {
    return {
      next: (...args: unknown[]) =>
        Promise.resolve(asIterator.next(...(args as []))),
      ...(typeof asIterator.return === "function"
        ? {
            return: (value?: unknown) =>
              Promise.resolve(asIterator.return!(value)),
          }
        : {}),
    };
  }
  throw new TypeError("RoundRobinChannel.add: source is not iterable");
}

export class RoundRobinChannel<T> implements AsyncIterable<T> {
  private readonly sources: SourceState<T>[] = [];
  private readonly onSourceError?: (error: unknown, sourceId: number) => void;
  private wakeWaiters: Array<() => void> = [];
  private streaming = false;
  private nextId = 0;

  constructor(options: RoundRobinChannelOptions = {}) {
    this.onSourceError = options.onSourceError;
  }

  /** 加入一个源，返回其 id。可在消费开始前或进行中加入。 */
  add(source: SourceInput<T>): number {
    const state: SourceState<T> = {
      id: this.nextId++,
      iterator: toAsyncIterator(source),
      pending: null,
      buffered: null,
      paused: false,
      done: false,
    };
    this.sources.push(state);
    this.wake();
    return state.id;
  }

  /** 暂停一个源。进行中的拉取不会被取消，其结果会被缓冲到恢复为止。 */
  pause(id: number): boolean {
    const state = this.find(id);
    if (!state || state.done || state.paused) return false;
    state.paused = true;
    return true;
  }

  /** 恢复一个暂停的源。若有缓冲的拉取结果，将在下一轮优先发出。 */
  resume(id: number): boolean {
    const state = this.find(id);
    if (!state || state.done || !state.paused) return false;
    state.paused = false;
    this.wake();
    return true;
  }

  isPaused(id: number): boolean {
    return this.find(id)?.paused ?? false;
  }

  /** 尚未结束（done）的源数量。 */
  get activeCount(): number {
    return this.sources.filter((s) => !s.done).length;
  }

  /**
   * 消费通道。只能消费一次；提前结束（break/return/throw）会清理所有源。
   */
  async *stream(): AsyncGenerator<T, void, unknown> {
    if (this.streaming) {
      throw new Error("RoundRobinChannel can only be streamed once");
    }
    this.streaming = true;
    try {
      for (;;) {
        const round = this.sources.filter((s) => !s.done && !s.paused);
        if (round.length === 0) {
          if (this.sources.every((s) => s.done)) return;
          // 全部源都处于暂停状态：挂起等待 resume()/add()，而不是空转。
          await this.waitForWake();
          continue;
        }
        // 本轮开始：为每个参与的源发起恰好一个拉取（背压点——只有消费者
        // 走到这里才会拉取，且每个源最多一个未完成拉取）。
        for (const state of round) {
          if (state.pending === null && state.buffered === null) {
            this.startPull(state);
          }
        }
        for (const state of round) {
          const outcome =
            state.buffered !== null ? state.buffered : await state.pending!;
          state.buffered = null;
          state.pending = null;
          if (state.paused) {
            // 拉取完成前/后源被暂停：把结果缓冲起来，恢复后再发。
            state.buffered = outcome;
            continue;
          }
          if (!outcome.ok) {
            this.failSource(state, outcome.error);
            continue;
          }
          if (outcome.result.done) {
            state.done = true;
            continue;
          }
          yield outcome.result.value;
        }
      }
    } finally {
      await this.closeAll();
    }
  }

  [Symbol.asyncIterator](): AsyncGenerator<T, void, unknown> {
    return this.stream();
  }

  /** 主动关闭通道（不经过消费者）：清理所有源并唤醒等待中的消费循环。 */
  async close(): Promise<void> {
    for (const state of this.sources) state.done = true;
    this.wake();
    await this.closeAll();
  }

  private find(id: number): SourceState<T> | undefined {
    return this.sources.find((s) => s.id === id);
  }

  private startPull(state: SourceState<T>): void {
    let pull: Promise<IteratorResult<T>>;
    try {
      pull = Promise.resolve(state.iterator.next());
    } catch (error) {
      pull = Promise.reject(error);
    }
    // 规范化为永不 reject 的 Promise，避免暂停期间无人 await 时产生
    // unhandled rejection。
    state.pending = pull.then<PullOutcome<T>>(
      (result) => ({ ok: true, result }),
      (error) => ({ ok: false, error }),
    );
  }

  private failSource(state: SourceState<T>, error: unknown): void {
    state.done = true;
    this.onSourceError?.(error, state.id);
    if (typeof state.iterator.return === "function") {
      Promise.resolve(state.iterator.return()).catch(() => {});
    }
  }

  private waitForWake(): Promise<void> {
    return new Promise((resolve) => this.wakeWaiters.push(resolve));
  }

  private wake(): void {
    const waiters = this.wakeWaiters.splice(0);
    for (const resolve of waiters) resolve();
  }

  private async closeAll(): Promise<void> {
    await Promise.all(
      this.sources.map(async (state) => {
        state.done = true;
        if (state.pending !== null) {
          // 已规范化，永不 reject；等它落定，避免悬挂的拉取。
          await state.pending;
          state.pending = null;
        }
        state.buffered = null;
        if (typeof state.iterator.return === "function") {
          try {
            await state.iterator.return();
          } catch {
            // 清理阶段的错误不再上抛。
          }
        }
      }),
    );
    this.wake();
  }
}

/** 便捷工厂：创建通道并一次性加入所有源。 */
export function roundRobin<T>(
  sources: ReadonlyArray<SourceInput<T>>,
  options?: RoundRobinChannelOptions,
): RoundRobinChannel<T> {
  const channel = new RoundRobinChannel<T>(options);
  for (const source of sources) channel.add(source);
  return channel;
}
