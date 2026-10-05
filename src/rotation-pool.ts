/**
 * rotation-pool —— 生成器轮转池。
 *
 * 多个生成器共享一条输出通道：消费者每调用一次 `next()`，池子按固定轮转
 * 顺序从下一个“活跃”生成器取出一项交付。核心语义：
 *
 * - 消费者背压：纯拉取模型。没有消费者请求就不会触发生成器；每项交付后
 *   仅为该生成器预取一次，因此任意时刻每个生成器最多一个未完成拉取，
 *   每个生成器的缓冲最多一项。
 * - 暂停/恢复：暂停立即生效——不再向该生成器发起新拉取，轮转时跳过它；
 *   暂停瞬间已在途的拉取允许完成，结果被保留（缓冲）直到恢复后再交付，
 *   既不丢失也不会在暂停期间提前交付。
 * - 异常隔离：某个生成器抛错只会让它自身进入 failed 状态并触发 onError
 *   回调，其余生成器照常轮转，池子整体不受影响。
 * - 提前结束：close() 先在途拉取落定、再调用各生成器的 return()；
 *   正在等待的消费者收到 done。
 */

export type Source<T> =
  | AsyncIterable<T>
  | Iterable<T>
  | AsyncIterator<T>
  | Iterator<T>;

export type EntryState = 'active' | 'paused' | 'done' | 'failed';

export interface EntrySnapshot {
  id: string;
  state: EntryState;
  error: unknown;
}

export interface RotationPoolOptions {
  /** 某个生成器抛错被隔离时触发（同步调用，抛错会被忽略）。 */
  onError?: (id: string, error: unknown) => void;
}

interface Entry<T> {
  id: string;
  iterator: AsyncIterator<T>;
  state: EntryState;
  /** 在途拉取；非空时该生成器不会再发起新拉取。 */
  inflight: Promise<IteratorResult<T>> | null;
  /** 已落定但未交付的结果；暂停期间在途拉取完成后也暂存于此。 */
  buffered: IteratorResult<T> | null;
  error: unknown;
}

function toAsyncIterator<T>(source: Source<T>): AsyncIterator<T> {
  const anySource = source as Record<PropertyKey, unknown>;
  if (typeof anySource[Symbol.asyncIterator] === 'function') {
    return (source as AsyncIterable<T>)[Symbol.asyncIterator]();
  }
  if (typeof anySource[Symbol.iterator] === 'function') {
    const sync = (source as Iterable<T>)[Symbol.iterator]();
    return {
      next: () => Promise.resolve(sync.next()),
      return: sync.return
        ? (value?: unknown) => Promise.resolve(sync.return!(value))
        : undefined,
    };
  }
  if (typeof anySource.next === 'function') {
    const iterator = source as AsyncIterator<T> | Iterator<T>;
    return {
      next: () => Promise.resolve(iterator.next()),
      return: iterator.return
        ? (value?: unknown) => Promise.resolve(iterator.return!(value))
        : undefined,
    };
  }
  throw new TypeError('RotationPool: source is not iterable nor an iterator');
}

export class RotationPool<T> implements AsyncIterableIterator<T> {
  private readonly entries: Array<Entry<T>> = [];
  private readonly onError?: (id: string, error: unknown) => void;
  private cursor = 0;
  private closedFlag = false;
  private readonly waiters = new Set<() => void>();
  /** 串行化消费者的 next() 调用，保证轮转顺序。 */
  private chain: Promise<unknown> = Promise.resolve();

  constructor(options: RotationPoolOptions = {}) {
    this.onError = options.onError;
  }

  /** 注册一个生成器。可在消费过程中动态添加。 */
  add(id: string, source: Source<T>): this {
    if (this.closedFlag) {
      throw new Error(`RotationPool: cannot add "${id}" after close()`);
    }
    if (this.entries.some((entry) => entry.id === id)) {
      throw new Error(`RotationPool: duplicate id "${id}"`);
    }
    this.entries.push({
      id,
      iterator: toAsyncIterator(source),
      state: 'active',
      inflight: null,
      buffered: null,
      error: undefined,
    });
    this.notify();
    return this;
  }

  /**
   * 暂停一个生成器：不再向其发起新拉取，轮转时跳过。
   * 若此刻恰有在途拉取，允许其完成，结果保留到恢复后交付。
   */
  pause(id: string): void {
    const entry = this.require(id);
    if (entry.state !== 'active') return;
    entry.state = 'paused';
    this.notify();
  }

  /** 恢复一个已暂停的生成器；暂停期间缓冲的结果会优先交付。 */
  resume(id: string): void {
    const entry = this.require(id);
    if (entry.state !== 'paused') return;
    entry.state = 'active';
    this.notify();
  }

  /** 提前结束：在途拉取落定后调用每个生成器的 return()，等待中的消费者收到 done。 */
  async close(): Promise<void> {
    if (this.closedFlag) return;
    this.closedFlag = true;
    this.notify();
    await Promise.all(
      this.entries.map(async (entry) => {
        if (entry.state === 'done') return;
        try {
          if (entry.inflight) await entry.inflight.catch(() => undefined);
          await entry.iterator.return?.();
        } catch {
          // 结束阶段的异常同样隔离，不影响其他生成器的清理。
        }
        if (entry.state !== 'failed') entry.state = 'done';
      }),
    );
    this.notify();
  }

  get closed(): boolean {
    return this.closedFlag;
  }

  get size(): number {
    return this.entries.length;
  }

  snapshot(): EntrySnapshot[] {
    return this.entries.map((entry) => ({
      id: entry.id,
      state: entry.state,
      error: entry.error,
    }));
  }

  next(): Promise<IteratorResult<T>> {
    const result = this.chain.then(() => this.nextInternal());
    this.chain = result.then(
      () => undefined,
      () => undefined,
    );
    return result;
  }

  async return(): Promise<IteratorResult<T>> {
    await this.close();
    return { done: true, value: undefined };
  }

  [Symbol.asyncIterator](): AsyncIterableIterator<T> {
    return this;
  }

  private async nextInternal(): Promise<IteratorResult<T>> {
    for (;;) {
      if (this.closedFlag) return { done: true, value: undefined };

      const entry = this.pickNext();
      if (!entry) {
        if (this.allSettled()) return { done: true, value: undefined };
        // 存在被暂停的生成器：等待 resume / add / close 唤醒。
        await this.waitForChange();
        continue;
      }

      if (entry.buffered) {
        const result = entry.buffered;
        entry.buffered = null;
        if (result.done) {
          entry.state = 'done';
          this.notify();
          continue;
        }
        this.cursor = (this.entries.indexOf(entry) + 1) % this.entries.length;
        // 有界预取：交付后再补一发，保证任意时刻最多一个在途拉取。
        this.issuePull(entry);
        return { done: false, value: result.value as T };
      }

      if (!entry.inflight) this.issuePull(entry);
      // 等待该生成器的拉取落定；若期间被暂停/失败/关闭则退出等待重新调度。
      await this.waitForEntry(entry);
    }
  }

  private pickNext(): Entry<T> | undefined {
    const count = this.entries.length;
    for (let offset = 0; offset < count; offset++) {
      const entry = this.entries[(this.cursor + offset) % count];
      if (entry.state === 'active') return entry;
    }
    return undefined;
  }

  private allSettled(): boolean {
    return this.entries.every(
      (entry) => entry.state === 'done' || entry.state === 'failed',
    );
  }

  private issuePull(entry: Entry<T>): void {
    if (entry.state !== 'active' || entry.inflight || entry.buffered) return;
    let pull: Promise<IteratorResult<T>>;
    try {
      pull = Promise.resolve(entry.iterator.next());
    } catch (error) {
      this.fail(entry, error);
      return;
    }
    entry.inflight = pull;
    pull.then(
      (result) => {
        if (entry.inflight !== pull) return;
        entry.inflight = null;
        // 即使期间被暂停也先缓冲，恢复后再交付。
        entry.buffered = result;
        this.notify();
      },
      (error) => {
        if (entry.inflight !== pull) return;
        entry.inflight = null;
        this.fail(entry, error);
      },
    );
  }

  private fail(entry: Entry<T>, error: unknown): void {
    if (entry.state === 'failed') return;
    entry.state = 'failed';
    entry.error = error;
    entry.buffered = null;
    try {
      this.onError?.(entry.id, error);
    } catch {
      // 监听器异常不影响池子。
    }
    this.notify();
  }

  private async waitForEntry(entry: Entry<T>): Promise<void> {
    while (
      !this.closedFlag &&
      entry.state === 'active' &&
      entry.buffered === null
    ) {
      await this.waitForChange();
    }
  }

  private waitForChange(): Promise<void> {
    return new Promise((resolve) => {
      this.waiters.add(resolve);
    });
  }

  private notify(): void {
    const waiters = [...this.waiters];
    this.waiters.clear();
    for (const resolve of waiters) resolve();
  }

  private require(id: string): Entry<T> {
    const entry = this.entries.find((candidate) => candidate.id === id);
    if (!entry) throw new Error(`RotationPool: unknown generator "${id}"`);
    return entry;
  }
}
