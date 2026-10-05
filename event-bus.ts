/**
 * event-bus.ts — 强类型事件总线
 *
 * 设计目标：
 * - 显式释放：订阅返回 dispose 句柄，可关联 AbortSignal；正确性不依赖 GC 是否及时发生。
 * - 快照语义：emit 冻结当前订阅列表，按注册顺序逐个调用；
 *   本轮之前（或本轮中）已 dispose 的订阅被跳过；本轮新注册的订阅从下一次 emit 生效。
 * - 重入安全：支持同步嵌套 emit；once 订阅在调用前移除，防止重入再次触发。
 * - 异常隔离：监听器异常被单独收集并作为返回值返回，不吞掉、不阻断其他监听器。
 * - 作用域：支持按 scope 批量注销；scope 关闭后拒绝新订阅。
 * - 可选弱引用订阅：提供 sweep() 清扫死亡弱引用，但可靠释放仍由 dispose 负责。
 */

export type Handler<Payload> = (payload: Payload) => void;

/** 订阅句柄：dispose 幂等，可重复调用。 */
export interface Subscription {
  readonly disposed: boolean;
  dispose(): void;
}

export interface SubscribeOptions {
  /** 调用一次后自动注销（在调用前移除，重入不会再次触发）。 */
  once?: boolean;
  /** 关联 AbortSignal；signal abort 时等同于 dispose。已 abort 的 signal 会得到一个已 dispose 的空句柄。 */
  signal?: AbortSignal;
  /** 以 WeakRef 持有 handler。总线不延长 handler 寿命；死亡引用由 sweep()/emit 顺带清扫。 */
  weak?: boolean;
  /** 关联 scope；scope.close() 时批量注销。scope 已关闭时订阅会抛错。 */
  scope?: EventScope;
}

interface ListenerRecord {
  readonly event: string | symbol;
  readonly once: boolean;
  readonly strongHandler: Handler<unknown> | null;
  readonly weakHandler: WeakRef<Handler<unknown>> | null;
  readonly scope: EventScope | null;
  readonly signal: AbortSignal | null;
  onAbort: (() => void) | null;
  disposed: boolean;
}

export class EventBus<Events = Record<string, unknown>> {
  private readonly lists = new Map<string | symbol, ListenerRecord[]>();
  private activeCount = 0;
  private readonly registry: FinalizationRegistry<ListenerRecord> | null;

  constructor() {
    // 尽力而为的自动清扫：GC 回收弱引用目标后顺带移除记录。
    // 注意：这只是优化，正确性完全不依赖它——可靠释放始终由 dispose 负责。
    this.registry =
      typeof FinalizationRegistry === "function"
        ? new FinalizationRegistry<ListenerRecord>((record) => {
            this.removeRecord(record);
          })
        : null;
  }

  /** 当前活动订阅总数。 */
  get size(): number {
    return this.activeCount;
  }

  /** 指定事件（或不传参时为全部）的活动订阅数。 */
  listenerCount(event?: keyof Events): number {
    if (event === undefined) return this.activeCount;
    return this.lists.get(event as string | symbol)?.length ?? 0;
  }

  /** 创建一个注销作用域。 */
  createScope(): EventScope {
    return new EventScope(this);
  }

  on<K extends keyof Events>(
    event: K,
    handler: Handler<Events[K]>,
    options: SubscribeOptions = {},
  ): Subscription {
    if (typeof handler !== "function") {
      throw new TypeError("handler must be a function");
    }
    const scope = options.scope ?? null;
    scope?.assertUsableBy(this); // scope 已关闭或不属于本总线时抛错

    const signal = options.signal ?? null;
    const weak = options.weak === true;
    const record: ListenerRecord = {
      event: event as string | symbol,
      once: options.once === true,
      strongHandler: weak ? null : (handler as Handler<unknown>),
      weakHandler: weak ? new WeakRef(handler as Handler<unknown>) : null,
      scope,
      signal,
      onAbort: null,
      disposed: false,
    };

    if (signal) {
      if (signal.aborted) {
        // 已取消的 signal：返回一个已 dispose 的空句柄，不产生任何计数。
        record.disposed = true;
        return this.makeHandle(record);
      }
      const onAbort = () => this.removeRecord(record);
      record.onAbort = onAbort;
      signal.addEventListener("abort", onAbort, { once: true });
    }

    let list = this.lists.get(record.event);
    if (!list) {
      list = [];
      this.lists.set(record.event, list);
    }
    list.push(record);
    this.activeCount++;
    scope?.attach(record);
    if (record.weakHandler && this.registry) {
      this.registry.register(handler as Handler<unknown>, record, record);
    }
    return this.makeHandle(record);
  }

  once<K extends keyof Events>(
    event: K,
    handler: Handler<Events[K]>,
    options: Omit<SubscribeOptions, "once"> = {},
  ): Subscription {
    return this.on(event, handler, { ...options, once: true });
  }

  /**
   * 同步派发事件。冻结当前订阅列表后按注册顺序调用。
   * 返回本轮所有监听器抛出的异常（按触发顺序）；无异常时返回空数组。
   */
  emit<K extends keyof Events>(event: K, payload: Events[K]): unknown[] {
    const list = this.lists.get(event as string | symbol);
    if (!list || list.length === 0) return [];

    // 冻结快照：本轮新注册的订阅不在其中，从下一次 emit 生效。
    const snapshot: readonly ListenerRecord[] = Object.freeze(list.slice());
    const errors: unknown[] = [];

    for (const record of snapshot) {
      if (record.disposed) continue; // 本轮之前/之中已 dispose：跳过

      const handler =
        record.strongHandler ?? record.weakHandler?.deref() ?? null;
      if (handler === null) {
        // 弱引用目标已被 GC：顺带清扫（尽力而为，不影响正确性）。
        this.removeRecord(record);
        continue;
      }

      // once 在调用前移除：即使处理器内同步重入 emit 同一事件，也不会再次触发。
      if (record.once) this.removeRecord(record);

      try {
        handler(payload);
      } catch (error) {
        errors.push(error); // 单独收集，不吞掉、不阻断后续监听器
      }
    }
    return errors;
  }

  /**
   * 清扫死亡的弱引用订阅，返回移除数量。
   * 可靠释放仍由 dispose 负责；本方法只是确定性清理的辅助手段。
   */
  sweep(): number {
    let removed = 0;
    for (const list of [...this.lists.values()]) {
      for (const record of [...list]) {
        if (record.weakHandler !== null && record.weakHandler.deref() === undefined) {
          this.removeRecord(record);
          removed++;
        }
      }
    }
    return removed;
  }

  /** @internal 供 Subscription / EventScope / FinalizationRegistry 调用。幂等。 */
  removeRecord(record: ListenerRecord): void {
    if (record.disposed) return;
    record.disposed = true;
    this.activeCount--;

    if (record.signal && record.onAbort) {
      record.signal.removeEventListener("abort", record.onAbort);
      record.onAbort = null;
    }
    record.scope?.detach(record);

    const list = this.lists.get(record.event);
    if (list) {
      const index = list.indexOf(record);
      if (index !== -1) list.splice(index, 1);
      if (list.length === 0) this.lists.delete(record.event);
    }
    this.registry?.unregister(record);
  }

  private makeHandle(record: ListenerRecord): Subscription {
    return {
      get disposed() {
        return record.disposed;
      },
      dispose: () => this.removeRecord(record),
    };
  }
}

/** 注销作用域：close() 批量注销其下所有订阅；关闭后拒绝新订阅。 */
export class EventScope {
  private readonly owner: EventBus<any>;
  private readonly records = new Set<ListenerRecord>();
  private _closed = false;

  /** @internal 请使用 bus.createScope()。 */
  constructor(owner: EventBus<any>) {
    this.owner = owner;
  }

  get closed(): boolean {
    return this._closed;
  }

  /** 该 scope 下仍活动的订阅数。 */
  get size(): number {
    return this.records.size;
  }

  /** 批量注销全部订阅。幂等，可重复调用。 */
  close(): void {
    if (this._closed) return;
    this._closed = true;
    for (const record of [...this.records]) {
      this.owner.removeRecord(record); // removeRecord 内部会 detach
    }
    this.records.clear();
  }

  /** @internal */
  assertUsableBy(bus: EventBus<any>): void {
    if (bus !== this.owner) {
      throw new Error("scope belongs to a different EventBus");
    }
    if (this._closed) {
      throw new Error("cannot subscribe: scope is closed");
    }
  }

  /** @internal */
  attach(record: ListenerRecord): void {
    this.records.add(record);
  }

  /** @internal */
  detach(record: ListenerRecord): void {
    this.records.delete(record);
  }
}
