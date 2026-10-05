/**
 * callback-async-iterator
 *
 * 将回调式数据源适配为 AsyncIterableIterator，语义约定：
 *
 * 1. 缓冲上限：内部缓冲区最多缓存 `highWaterMark`（默认 8）条数据。
 *    缓冲达到上限时调用 `source.pause()` 施加反压；消费使缓冲降到
 *    上限以下时调用 `source.resume()` 恢复。pause/resume 是协作式
 *    信号，数据源应当在收到 pause 后停止推送。
 *
 * 2. 交付顺序：缓冲中的数据总是先于终止信号（错误或结束）交付。
 *    即使错误已经到达，也会先把已缓冲的数据按序交给消费者，
 *    缓冲排空后才交付终止信号。
 *
 * 3. 终止竞争：错误与结束通知可能相邻到达，采用"先到先赢"——
 *    第一个终止信号生效，之后到达的终止信号与数据一律忽略。
 *
 * 4. 唯一清理：`source.dispose()` 在整个生命周期内至多调用一次，
 *    触发点为：首个终止信号到达、消费者提前 `return()`/`throw()`、
 *    或显式 `cancel()`，三者 whichever 先发生。
 *
 * 5. 挂起读取：允许多个 `next()` 同时挂起。每条缓冲数据恰好满足
 *    一个挂起读取（FIFO）；终止信号会广播给所有挂起读取——
 *    错误使全部挂起读取 reject，结束使全部挂起读取 resolve done，
 *    不会漏掉任何一个挂起者。错误终止是"粘性"的：缓冲排空后，
 *    后续每次 `next()` 都会以同一错误 reject。
 */

export interface SourceHandlers<T> {
  onData(value: T): void;
  onError(error: unknown): void;
  onEnd(): void;
}

export interface CallbackSource<T> {
  start(handlers: SourceHandlers<T>): void;
  pause?(): void;
  resume?(): void;
  dispose?(): void;
}

export interface AdapterOptions {
  /** 缓冲区上限，默认 8。 */
  highWaterMark?: number;
}

type Terminal =
  | { readonly kind: "error"; readonly error: unknown }
  | { readonly kind: "end" };

interface PendingRead<T> {
  resolve(result: IteratorResult<T>): void;
  reject(error: unknown): void;
}

const DONE: IteratorResult<never> = { value: undefined, done: true };

export function fromCallbackSource<T>(
  source: CallbackSource<T>,
  options: AdapterOptions = {}
): AsyncIterableIterator<T> {
  const highWaterMark = options.highWaterMark ?? 8;
  if (!Number.isInteger(highWaterMark) || highWaterMark < 1) {
    throw new RangeError("highWaterMark must be a positive integer");
  }

  const buffer: T[] = [];
  const pending: PendingRead<T>[] = [];
  let terminal: Terminal | null = null;
  let paused = false;
  let cleanedUp = false;

  function cleanup(): void {
    if (cleanedUp) return;
    cleanedUp = true;
    try {
      source.dispose?.();
    } catch {
      // 清理失败不影响迭代器状态。
    }
  }

  function maybePause(): void {
    if (!paused && buffer.length >= highWaterMark) {
      paused = true;
      source.pause?.();
    }
  }

  function maybeResume(): void {
    if (paused && buffer.length < highWaterMark) {
      paused = false;
      source.resume?.();
    }
  }

  /** 把缓冲数据 / 终止信号交付给挂起的读取。 */
  function pump(): void {
    while (pending.length > 0) {
      if (buffer.length > 0) {
        const reader = pending.shift()!;
        reader.resolve({ value: buffer.shift()!, done: false });
        maybeResume();
      } else if (terminal !== null) {
        broadcastTerminal();
      } else {
        return;
      }
    }
  }

  /** 终止信号广播给全部挂起读取，保证没有挂起者被漏掉。 */
  function broadcastTerminal(): void {
    const readers = pending.splice(0);
    if (terminal!.kind === "error") {
      const error = (terminal as { kind: "error"; error: unknown }).error;
      for (const reader of readers) reader.reject(error);
    } else {
      for (const reader of readers) reader.resolve(DONE);
    }
  }

  function setTerminal(next: Terminal): void {
    // 终止竞争：先到先赢，后续终止信号忽略。
    if (terminal !== null || cleanedUp) return;
    terminal = next;
    cleanup();
    pump();
  }

  const handlers: SourceHandlers<T> = {
    onData(value: T): void {
      // 终止或清理之后到达的数据一律忽略。
      if (terminal !== null || cleanedUp) return;
      if (pending.length > 0) {
        pending.shift()!.resolve({ value, done: false });
        return;
      }
      buffer.push(value);
      maybePause();
    },
    onError(error: unknown): void {
      setTerminal({ kind: "error", error });
    },
    onEnd(): void {
      setTerminal({ kind: "end" });
    },
  };

  const iterator: AsyncIterableIterator<T> = {
    next(): Promise<IteratorResult<T>> {
      if (buffer.length > 0) {
        const value = buffer.shift()!;
        maybeResume();
        return Promise.resolve({ value, done: false });
      }
      if (terminal !== null) {
        if (terminal.kind === "error") return Promise.reject(terminal.error);
        return Promise.resolve(DONE);
      }
      if (cleanedUp) return Promise.resolve(DONE);
      return new Promise<IteratorResult<T>>((resolve, reject) => {
        pending.push({ resolve, reject });
      });
    },

    return(): Promise<IteratorResult<T>> {
      // 消费者提前结束：清理资源，丢弃缓冲，挂起读取全部以 done 收场。
      cleanup();
      buffer.length = 0;
      const readers = pending.splice(0);
      for (const reader of readers) reader.resolve(DONE);
      return Promise.resolve(DONE);
    },

    throw(error?: unknown): Promise<IteratorResult<T>> {
      cleanup();
      buffer.length = 0;
      const readers = pending.splice(0);
      for (const reader of readers) reader.resolve(DONE);
      return Promise.reject(error);
    },

    [Symbol.asyncIterator]() {
      return this;
    },
  };

  try {
    source.start(handlers);
  } catch (error) {
    setTerminal({ kind: "error", error });
  }

  return iterator;
}
