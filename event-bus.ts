/**
 * 类型安全的事件总线。
 *
 * 设计原则：
 * - 显式 dispose 是唯一可靠的释放途径；弱引用订阅只是清扫优化，
 *   正确性绝不依赖 GC 是否及时发生。
 * - 每次 emit 冻结当前订阅快照：本轮之前已 dispose 的跳过，
 *   本轮新注册的订阅从下一次 emit 才生效。
 * - once 订阅在调用前移除，同步重入不会再次触发。
 * - 监听器异常被逐个收集进返回值，既不吞掉也不阻断其他监听器。
 */

export type Listener<T> = (payload: T) => void;

/** 订阅返回的显式释放句柄。dispose 幂等。 */
export interface DisposeHandle {
  readonly disposed: boolean;
  dispose(): void;
}

export interface SubscribeOptions {
  /** 调用一次后自动移除（在调用前移除，防止重入再次触发）。 */
  once?: boolean;
  /** 关联 AbortSignal：signal 中止时自动 dispose。 */
  signal?: AbortSignal;
  /**
   * 以弱引用持有监听器，配合 sweep() 清扫已死的引用。
   * 注意：这只是内存优化，可靠释放仍由 dispose 负责。
   */
  weak?: boolean;
}

export interface EmitError<T> {
  listener: Listener<T>;
  error: unknown;
}

export interface EmitResult<T> {
  /** 本轮实际调用的监听器数量。 */
  invoked: number;
  /** 本轮快照中已 dispose 而被跳过的订阅数量。 */
  skipped: number;
  /** 监听器抛出的异常，按发生顺序收集。 */
  errors: EmitError<T>[];
}

/** 订阅作用域：批量管理订阅，关闭后拒绝新订阅。 */
export interface Scope<T> {
  readonly closed: boolean;
  /** 该 scope 下仍活动的订阅数。 */
  readonly size: number;
  subscribe(listener: Listener<T>, options?: SubscribeOptions): DisposeHandle;
  once(listener: Listener<T>, options?: Omit<SubscribeOptions, 'once'>): DisposeHandle;
  /** 关闭 scope 并 dispose 其全部订阅；幂等。 */
  close(): void;
}

interface Entry<T> {
  listener: Listener<T> | WeakRef<Listener<T>>;
  weak: boolean;
  once: boolean;
  disposed: boolean;
  scope: ScopeImpl<T> | null;
  signal: AbortSignal | null;
  onAbort: (() => void) | null;
}

function alreadyDisposedHandle(): DisposeHandle {
  return { disposed: true, dispose() {} };
}

export class EventBus<T> {
  /** Set 保持插入序，即注册顺序。 */
  private entries = new Set<Entry<T>>();

  /** 当前活动订阅数（不含已 dispose / 已清扫的）。 */
  get size(): number {
    return this.entries.size;
  }

  subscribe(listener: Listener<T>, options: SubscribeOptions = {}): DisposeHandle {
    return this.addEntry(listener, options, null);
  }

  once(listener: Listener<T>, options: Omit<SubscribeOptions, 'once'> = {}): DisposeHandle {
    return this.addEntry(listener, { ...options, once: true }, null);
  }

  /** 创建一个订阅作用域。 */
  scope(): Scope<T> {
    return new ScopeImpl<T>(this);
  }

  /**
   * 冻结当前订阅快照，按注册顺序逐个调用。
   * 支持同步重入（监听器内再次 emit）：内层 emit 是新一轮，重新快照。
   */
  emit(payload: T): EmitResult<T> {
    const snapshot = [...this.entries];
    const errors: EmitError<T>[] = [];
    let invoked = 0;
    let skipped = 0;

    for (const entry of snapshot) {
      // 本轮之前（或本轮中）已 dispose 的订阅跳过。
      if (entry.disposed) {
        skipped++;
        continue;
      }

      const listener = entry.weak
        ? (entry.listener as WeakRef<Listener<T>>).deref()
        : (entry.listener as Listener<T>);

      // 弱引用已死：惰性清扫，本轮不调用。
      if (listener === undefined) {
        this.removeEntry(entry);
        skipped++;
        continue;
      }

      // once 在调用前移除，防止重入再次触发。
      if (entry.once) {
        this.removeEntry(entry);
      }

      try {
        listener(payload);
        invoked++;
      } catch (error) {
        errors.push({ listener, error });
      }
    }

    return { invoked, skipped, errors };
  }

  /**
   * 清扫弱引用已死的订阅，返回清扫数量。
   * 仅为内存优化；正确性不依赖本方法，释放应走 dispose。
   */
  sweep(): number {
    let removed = 0;
    for (const entry of this.entries) {
      if (entry.weak && (entry.listener as WeakRef<Listener<T>>).deref() === undefined) {
        this.removeEntry(entry);
        removed++;
      }
    }
    return removed;
  }

  private addEntry(
    listener: Listener<T>,
    options: SubscribeOptions,
    scope: ScopeImpl<T> | null,
  ): DisposeHandle {
    const { once = false, weak = false, signal } = options;

    // signal 已取消：不注册，直接返回已 dispose 的句柄。
    if (signal?.aborted) {
      return alreadyDisposedHandle();
    }

    const entry: Entry<T> = {
      listener: weak ? new WeakRef(listener) : listener,
      weak,
      once,
      disposed: false,
      scope,
      signal: signal ?? null,
      onAbort: null,
    };

    if (signal) {
      const onAbort = () => this.removeEntry(entry);
      entry.onAbort = onAbort;
      signal.addEventListener('abort', onAbort, { once: true });
    }

    this.entries.add(entry);
    scope?.link(entry);

    return {
      get disposed() {
        return entry.disposed;
      },
      dispose: () => this.removeEntry(entry),
    };
  }

  private removeEntry(entry: Entry<T>): void {
    if (entry.disposed) return; // 重复 dispose 幂等
    entry.disposed = true;
    this.entries.delete(entry);
    if (entry.signal && entry.onAbort) {
      entry.signal.removeEventListener('abort', entry.onAbort);
      entry.signal = null;
      entry.onAbort = null;
    }
    entry.scope?.unlink(entry);
    entry.scope = null;
  }
}

class ScopeImpl<T> implements Scope<T> {
  private entries = new Set<Entry<T>>();
  private bus: EventBus<T>;
  closed = false;

  constructor(bus: EventBus<T>) {
    this.bus = bus;
  }

  get size(): number {
    return this.entries.size;
  }

  subscribe(listener: Listener<T>, options: SubscribeOptions = {}): DisposeHandle {
    this.assertOpen();
    return this.bus.addEntry(listener, options, this);
  }

  once(listener: Listener<T>, options: Omit<SubscribeOptions, 'once'> = {}): DisposeHandle {
    this.assertOpen();
    return this.bus.addEntry(listener, { ...options, once: true }, this);
  }

  close(): void {
    if (this.closed) return;
    this.closed = true;
    for (const entry of this.entries) {
      this.bus.removeEntry(entry);
    }
  }

  link(entry: Entry<T>): void {
    this.entries.add(entry);
  }

  unlink(entry: Entry<T>): void {
    this.entries.delete(entry);
  }

  private assertOpen(): void {
    if (this.closed) {
      throw new Error('Cannot subscribe: scope is already closed.');
    }
  }
}
