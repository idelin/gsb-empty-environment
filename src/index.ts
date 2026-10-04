/**
 * callback-async-iter
 *
 * 将回调式数据源适配为 AsyncIterableIterator。
 *
 * 交付顺序约定（本库对外承诺的语义）：
 * 1. 数据按 onData 到达顺序 FIFO 交付；
 * 2. 终止信号（error / end）不会插队：缓冲区中已缓存的数据总是先于终止信号交付；
 * 3. error 与 end 相邻到达时，"先到者胜"：第一个终止通知决定迭代器的终止形态，
 *    之后到达的终止通知（以及任何数据）一律忽略；
 * 4. 终止生效（缓冲区排空）后，所有挂起的 next() 一次性收到终止通知：
 *    error 终止时全部 reject 同一个错误，end 终止时全部 resolve { done: true }；
 * 5. 清理（controller.stop()）恰好执行一次，无论是正常结束、出错还是消费者提前 return()。
 */

/** 数据源事件回调。 */
export interface SourceHandlers<T> {
  onData(value: T): void;
  onError(error: unknown): void;
  onEnd(): void;
}

/** start() 返回的控制器，用于反压与清理。所有方法均可选。 */
export interface SourceController {
  /** 缓冲区满时调用，请求数据源暂停推送。 */
  pause?(): void;
  /** 缓冲区腾出空间时调用，请求数据源恢复推送。 */
  resume?(): void;
  /** 迭代终止时恰好调用一次，用于释放资源。 */
  stop?(): void;
}

/** 启动数据源的函数。允许在 start 期间同步触发回调。 */
export type SourceStarter<T> = (
  handlers: SourceHandlers<T>
) => SourceController | undefined | void;

export interface AdapterOptions {
  /** 内部缓冲上限，默认 8。达到上限即 pause，跌破上限即 resume。 */
  bufferSize?: number;
}

const DEFAULT_BUFFER_SIZE = 8;

type Terminal = { readonly kind: "error"; readonly error: unknown } | { readonly kind: "end" };

interface PendingRead<T> {
  resolve(result: IteratorResult<T>): void;
  reject(error: unknown): void;
}

/** 固定容量环形缓冲，避免数组 shift 的 O(n) 搬运。 */
class RingBuffer<T> {
  private readonly slots: (T | undefined)[];
  private head = 0;
  private tail = 0;
  private count = 0;

  constructor(readonly capacity: number) {
    this.slots = new Array<T | undefined>(capacity);
  }

  get size(): number {
    return this.count;
  }

  get isFull(): boolean {
    return this.count === this.capacity;
  }

  push(value: T): void {
    this.slots[this.tail] = value;
    this.tail = (this.tail + 1) % this.capacity;
    this.count += 1;
  }

  shift(): T | undefined {
    if (this.count === 0) return undefined;
    const value = this.slots[this.head];
    this.slots[this.head] = undefined;
    this.head = (this.head + 1) % this.capacity;
    this.count -= 1;
    return value;
  }

  clear(): void {
    this.slots.fill(undefined);
    this.head = 0;
    this.tail = 0;
    this.count = 0;
  }
}

export function fromCallbackSource<T>(
  start: SourceStarter<T>,
  options: AdapterOptions = {}
): AsyncIterableIterator<T> {
  const bufferSize = options.bufferSize ?? DEFAULT_BUFFER_SIZE;
  if (!Number.isInteger(bufferSize) || bufferSize < 1) {
    throw new RangeError(`bufferSize must be a positive integer, got ${bufferSize}`);
  }

  const buffer = new RingBuffer<T>(bufferSize);
  const pending: PendingRead<T>[] = [];

  let terminal: Terminal | null = null;
  let cleanedUp = false;
  let paused = false;
  let controller: SourceController | undefined;

  /** 唯一清理入口：幂等，保证 stop() 恰好调用一次。 */
  function cleanup(): void {
    if (cleanedUp) return;
    cleanedUp = true;
    if (controller && controller.stop) {
      try {
        controller.stop();
      } catch {
        // 清理失败不阻断迭代器收尾
      }
    }
  }

  function maybePause(): void {
    if (!paused && !terminal && !cleanedUp && buffer.isFull) {
      paused = true;
      controller?.pause?.();
    }
  }

  function maybeResume(): void {
    if (paused && !buffer.isFull) {
      paused = false;
      controller?.resume?.();
    }
  }

  /**
   * 结算挂起的读取：先把缓存数据按序交给等待中的读者；
   * 若已终止且缓存排空，则把终止通知广播给所有剩余读者。
   */
  function settle(): void {
    while (pending.length > 0 && buffer.size > 0) {
      const reader = pending.shift()!;
      reader.resolve({ value: buffer.shift() as T, done: false });
    }
    maybeResume();
    if (terminal !== null && buffer.size === 0) {
      const settled = terminal;
      cleanup();
      while (pending.length > 0) {
        const reader = pending.shift()!;
        if (settled.kind === "error") {
          reader.reject(settled.error);
        } else {
          reader.resolve({ value: undefined, done: true });
        }
      }
    }
  }

  const handlers: SourceHandlers<T> = {
    onData(value: T): void {
      // 终止或清理之后到达的数据一律丢弃
      if (terminal !== null || cleanedUp) return;
      buffer.push(value);
      settle();
      maybePause();
    },
    onError(error: unknown): void {
      // 先到者胜：已终止或已清理时忽略
      if (terminal !== null || cleanedUp) return;
      terminal = { kind: "error", error };
      settle();
    },
    onEnd(): void {
      if (terminal !== null || cleanedUp) return;
      terminal = { kind: "end" };
      settle();
    },
  };

  const iterator: AsyncIterableIterator<T> = {
    next(): Promise<IteratorResult<T>> {
      return new Promise<IteratorResult<T>>((resolve, reject) => {
        if (buffer.size > 0) {
          const value = buffer.shift() as T;
          maybeResume();
          resolve({ value, done: false });
          // 缓存排空后若已终止，需要把终止通知广播给其余挂起的读者
          settle();
          return;
        }
        if (terminal !== null) {
          cleanup();
          if (terminal.kind === "error") {
            reject(terminal.error);
          } else {
            resolve({ value: undefined, done: true });
          }
          return;
        }
        if (cleanedUp) {
          resolve({ value: undefined, done: true });
          return;
        }
        pending.push({ resolve, reject });
      });
    },

    return(): Promise<IteratorResult<T>> {
      // 消费者提前结束：丢弃缓存，广播 done，执行唯一清理
      buffer.clear();
      cleanup();
      while (pending.length > 0) {
        pending.shift()!.resolve({ value: undefined, done: true });
      }
      return Promise.resolve({ value: undefined, done: true });
    },

    throw(error?: unknown): Promise<IteratorResult<T>> {
      buffer.clear();
      cleanup();
      while (pending.length > 0) {
        pending.shift()!.reject(error);
      }
      return Promise.reject(error);
    },

    [Symbol.asyncIterator](): AsyncIterableIterator<T> {
      return this;
    },
  };

  // 状态全部就位后再启动数据源；start 期间允许同步回调。
  controller = start(handlers) ?? undefined;
  if (cleanedUp) {
    // 数据源在 start() 内同步终止：补上控制器的清理
    if (controller && controller.stop) {
      try {
        controller.stop();
      } catch {
        // 忽略
      }
    }
  } else {
    // start() 内同步灌满了缓冲：补一次反压
    maybePause();
  }

  return iterator;
}
