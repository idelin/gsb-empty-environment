// 零依赖的异步派生状态管理（不使用任何响应式框架）。
//
// 核心保证：
// 1. 派生值由异步任务计算，状态机为 pending / success / error。
// 2. 依赖变化会取消旧任务（AbortSignal），旧任务的结果按代际（generation）拒绝。
// 3. 取消（AbortError）不会被当作业务错误，不会进入 error 状态。
// 4. transaction() 两阶段提交：全部写入生效后才统一通知，读者看不到中间态。
// 5. 所有源单元写入记录为命令日志，Store.replay() 可回放重建最终状态。
// 6. shared() 按 key 共享派生任务，共享依赖不重复执行同一任务。

export type Status = 'pending' | 'success' | 'error';

export interface CellState<T> {
  readonly status: Status;
  readonly value: T | undefined;
  readonly error: unknown;
  /** 单调递增版本号，用于派生单元的依赖签名与代际判断。 */
  readonly version: number;
}

export type Listener<T> = (state: CellState<T>) => void;
export type Unsubscribe = () => void;

export interface Cell<T> {
  readonly id: string;
  get(): CellState<T>;
  /** 订阅后立即收到一次当前快照，之后每次状态变化都会收到通知。 */
  subscribe(listener: Listener<T>): Unsubscribe;
}

export interface SourceCell<T> extends Cell<T> {
  set(value: T): void;
  update(fn: (previous: T | undefined) => T): void;
}

export interface RunContext {
  /** 依赖变化、新一轮任务启动时触发 abort。 */
  readonly signal: AbortSignal;
  /** 本次运行的代际号，单调递增。 */
  readonly generation: number;
}

/** 一条可回放的状态操作命令。 */
export interface Command {
  readonly cell: string;
  readonly value: unknown;
}

export interface SnapshotEntry {
  readonly id: string;
  readonly kind: 'source' | 'derived';
  readonly status: Status;
  readonly value: unknown;
  readonly error: unknown;
  readonly version: number;
}

export function isAbortError(err: unknown): boolean {
  if (typeof err !== 'object' || err === null) return false;
  const { name, code } = err as { name?: unknown; code?: unknown };
  return name === 'AbortError' || code === 'ABORT_ERR';
}

/** 可取消的延时工具，signal 中止时以 AbortError 拒绝。 */
export function delay(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(signal.reason);
      return;
    }
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', onAbort);
      resolve();
    }, ms);
    const onAbort = () => {
      clearTimeout(timer);
      reject(signal!.reason);
    };
    signal?.addEventListener('abort', onAbort, { once: true });
  });
}

abstract class BaseCell<T> implements Cell<T> {
  readonly id: string;
  readonly store: Store;
  protected state: CellState<T>;
  private readonly listeners = new Set<Listener<T>>();

  constructor(store: Store, id: string, initial: CellState<T>) {
    this.store = store;
    this.id = id;
    this.state = initial;
  }

  get(): CellState<T> {
    return this.state;
  }

  subscribe(listener: Listener<T>): Unsubscribe {
    this.listeners.add(listener);
    listener(this.state);
    return () => {
      this.listeners.delete(listener);
    };
  }

  /** 提交阶段之后统一调用：此时所有写入均已生效，读者只能看到终态。 */
  emitChange(): void {
    for (const listener of [...this.listeners]) {
      try {
        listener(this.state);
      } catch (err) {
        console.error(`[store] 订阅者回调抛出异常（单元 "${this.id}"）:`, err);
      }
    }
  }

  protected setState(patch: Partial<Omit<CellState<T>, 'version'>>): void {
    this.state = { ...this.state, ...patch, version: this.state.version + 1 };
    this.store.markDependentsDirty(this);
    this.emitChange();
  }
}

class SourceCellImpl<T> extends BaseCell<T> implements SourceCell<T> {
  constructor(store: Store, id: string, initial: T) {
    super(store, id, { status: 'success', value: initial, error: undefined, version: 0 });
  }

  set(value: T): void {
    this.store.set(this, value);
  }

  update(fn: (previous: T | undefined) => T): void {
    this.set(fn(this.state.value));
  }

  /** 仅提交阶段调用：就地写值并递增版本，不发出任何通知。 */
  assign(value: T): void {
    this.state = { status: 'success', value, error: undefined, version: this.state.version + 1 };
  }
}

type Runner<T> = (ctx: RunContext, ...args: unknown[]) => Promise<T>;

class AsyncCellImpl<T> extends BaseCell<T> {
  private readonly deps: BaseCell<unknown>[];
  private readonly runner: Runner<T>;
  private generation = 0;
  private lastSignature: readonly number[] | null = null;
  private controller: AbortController | null = null;

  constructor(store: Store, id: string, deps: BaseCell<unknown>[], runner: Runner<T>) {
    super(store, id, { status: 'pending', value: undefined, error: undefined, version: 0 });
    this.deps = deps;
    this.runner = runner;
    for (const dep of deps) store.registerDependent(dep, this);
    store.schedule(this);
  }

  recompute(): void {
    // 依赖签名去重：事务中的无效写入、重复标脏都不会导致重复执行。
    const signature = this.deps.map(dep => dep.get().version);
    if (this.lastSignature !== null && signature.every((v, i) => v === this.lastSignature![i])) {
      return;
    }
    this.lastSignature = signature;

    const generation = ++this.generation;
    this.controller?.abort(); // 依赖变化 → 取消旧任务
    const controller = new AbortController();
    this.controller = controller;

    if (this.state.status !== 'pending' || this.state.error !== undefined) {
      // 保留旧 value（stale-while-revalidate），仅切换状态。
      this.setState({ status: 'pending', error: undefined });
    }

    const args = this.deps.map(dep => dep.get().value);
    this.store.taskStarted();
    let result: Promise<T>;
    try {
      result = Promise.resolve(this.runner({ signal: controller.signal, generation }, ...args));
    } catch (err) {
      result = Promise.reject(err);
    }
    result.then(
      value => {
        if (this.generation !== generation) return; // 代际拒绝：旧结果不得回传
        this.setState({ status: 'success', value, error: undefined });
      },
      err => {
        if (this.generation !== generation) return; // 代际拒绝：过期错误一并丢弃
        if (isAbortError(err)) return; // 取消不是业务错误
        this.setState({ status: 'error', error: err });
      },
    ).finally(() => {
      this.store.taskEnded();
    });
  }
}

type CellValues<D extends readonly Cell<unknown>[]> = {
  [K in keyof D]: D[K] extends Cell<infer V> ? V : never;
};

interface PendingWrite {
  readonly cell: SourceCellImpl<unknown>;
  readonly value: unknown;
}

export class Store {
  private readonly cells = new Map<string, BaseCell<unknown>>();
  private readonly dependents = new Map<BaseCell<unknown>, Set<AsyncCellImpl<unknown>>>();
  private readonly dirty = new Set<AsyncCellImpl<unknown>>();
  private readonly sharedCells = new Map<string, Cell<unknown>>();
  private readonly commandLog: Command[] = [];
  private readonly idleWaiters: Array<() => void> = [];
  private flushScheduled = false;
  private scheduled = 0;
  private inflight = 0;
  private txBuffer: PendingWrite[] | null = null;

  /** 注册源单元（可写状态）。 */
  source<T>(id: string, initial: T): SourceCell<T> {
    this.assertNew(id);
    const cell = new SourceCellImpl<T>(this, id, initial);
    this.cells.set(id, cell as SourceCellImpl<unknown>);
    return cell;
  }

  /** 注册异步派生单元：deps 变化时取消旧任务并按最新依赖重跑。 */
  derived<D extends readonly Cell<unknown>[], T>(
    id: string,
    deps: readonly [...D],
    runner: (ctx: RunContext, ...args: CellValues<D>) => Promise<T>,
  ): Cell<T> {
    this.assertNew(id);
    const cell = new AsyncCellImpl<T>(
      this,
      id,
      deps as unknown as BaseCell<unknown>[],
      runner as Runner<T>,
    );
    this.cells.set(id, cell as AsyncCellImpl<unknown>);
    return cell;
  }

  /** 共享派生：同一 key 只注册并执行一次，多处消费同一任务。 */
  shared<D extends readonly Cell<unknown>[], T>(
    key: string,
    deps: readonly [...D],
    runner: (ctx: RunContext, ...args: CellValues<D>) => Promise<T>,
  ): Cell<T> {
    const existing = this.sharedCells.get(key);
    if (existing) return existing as Cell<T>;
    const cell = this.derived(`shared:${key}`, deps, runner);
    this.sharedCells.set(key, cell);
    return cell;
  }

  /** 写入源单元；事务中则缓冲，随事务一次性提交。 */
  set<T>(cell: SourceCell<T>, value: T): void {
    const impl = this.requireSource(cell);
    if (this.txBuffer) {
      this.txBuffer.push({ cell: impl, value });
      return;
    }
    this.commit([{ cell: impl, value }]);
  }

  /**
   * 事务：fn 中的全部写入先缓冲，fn 返回后一次性提交并统一通知。
   * 读者（订阅者 / 派生任务）只能看到提交后的终态，看不到中间态。
   * fn 抛错时全部缓冲写入被丢弃（回滚）。不支持嵌套。
   */
  transaction(fn: () => void): void {
    if (this.txBuffer) throw new Error('transaction() 不支持嵌套调用');
    this.txBuffer = [];
    try {
      fn();
    } catch (err) {
      this.txBuffer = null; // 回滚：丢弃缓冲的写入
      throw err;
    }
    const ops = this.txBuffer;
    this.txBuffer = null;
    this.commit(ops);
  }

  /** 等待所有已调度与进行中的派生任务落定。 */
  whenSettled(): Promise<void> {
    if (this.isIdle()) return Promise.resolve();
    return new Promise(resolve => this.idleWaiters.push(resolve));
  }

  /** 导出命令日志（深拷贝），可用于 Store.replay()。 */
  getReplayLog(): Command[] {
    return this.commandLog.map(cmd => ({ cell: cmd.cell, value: safeClone(cmd.value) }));
  }

  /** 导出全部单元的状态快照。 */
  snapshot(): SnapshotEntry[] {
    return [...this.cells.values()].map(cell => {
      const state = cell.get();
      return {
        id: cell.id,
        kind: cell instanceof SourceCellImpl ? ('source' as const) : ('derived' as const),
        status: state.status,
        value: safeClone(state.value),
        error: state.error instanceof Error ? state.error.message : state.error,
        version: state.version,
      };
    });
  }

  getCell(id: string): Cell<unknown> | undefined {
    return this.cells.get(id);
  }

  /**
   * 回放命令日志：在全新仓库上（先由 build 注册同样的单元）
   * 依次重放全部状态操作，重建出一致的最终状态。
   */
  static replay(commands: readonly Command[], build: (store: Store) => unknown = () => {}): Store {
    const store = new Store();
    build(store);
    for (const cmd of commands) {
      const cell = store.cells.get(cmd.cell);
      if (!(cell instanceof SourceCellImpl)) {
        throw new Error(`replay: 找不到源单元 "${cmd.cell}"，请在 build 回调中注册`);
      }
      store.commit([{ cell, value: cmd.value }]);
    }
    return store;
  }

  /** @internal 派生单元构造时登记依赖关系。 */
  registerDependent(dep: BaseCell<unknown>, cell: AsyncCellImpl<unknown>): void {
    let set = this.dependents.get(dep);
    if (!set) {
      set = new Set();
      this.dependents.set(dep, set);
    }
    set.add(cell);
  }

  /** @internal 状态变化后，把依赖它的派生单元标脏并调度重算。 */
  markDependentsDirty(cell: BaseCell<unknown>): void {
    const set = this.dependents.get(cell);
    if (!set) return;
    for (const dependent of set) this.schedule(dependent);
  }

  /** @internal */
  schedule(cell: AsyncCellImpl<unknown>): void {
    if (this.dirty.has(cell)) return;
    this.dirty.add(cell);
    this.scheduled++;
    this.ensureFlush();
  }

  /** @internal */
  taskStarted(): void {
    this.inflight++;
  }

  /** @internal */
  taskEnded(): void {
    this.inflight--;
    this.checkIdle();
  }

  private commit(ops: readonly PendingWrite[]): void {
    // 第一阶段：应用全部写入。此阶段不发任何通知，读者仍看到旧快照。
    const changed: SourceCellImpl<unknown>[] = [];
    for (const { cell, value } of ops) {
      if (Object.is(cell.get().value, value)) continue; // 无效写入不产生新代际
      cell.assign(value);
      changed.push(cell);
    }
    if (changed.length === 0) return;
    // 记录命令日志（可回放）。
    for (const cell of changed) {
      this.commandLog.push({ cell: cell.id, value: safeClone(cell.get().value) });
    }
    // 第二阶段：全部写入生效后，统一标脏并通知 —— 通知里读到的都是终态。
    for (const cell of changed) this.markDependentsDirty(cell);
    for (const cell of changed) cell.emitChange();
  }

  private ensureFlush(): void {
    if (this.flushScheduled) return;
    this.flushScheduled = true;
    queueMicrotask(() => this.flush());
  }

  private flush(): void {
    this.flushScheduled = false;
    const pending = [...this.dirty];
    this.dirty.clear();
    for (const cell of pending) {
      this.scheduled--;
      cell.recompute();
    }
    this.checkIdle();
  }

  private isIdle(): boolean {
    return this.inflight === 0 && this.scheduled === 0 && !this.flushScheduled;
  }

  private checkIdle(): void {
    if (!this.isIdle()) return;
    const waiters = this.idleWaiters.splice(0);
    for (const resolve of waiters) resolve();
  }

  private assertNew(id: string): void {
    if (this.cells.has(id)) throw new Error(`单元 id 重复: "${id}"`);
  }

  private requireSource(cell: SourceCell<unknown>): SourceCellImpl<unknown> {
    if (!(cell instanceof SourceCellImpl) || cell.store !== this) {
      throw new Error('set() 只能作用于本仓库的 source 单元');
    }
    return cell;
  }
}

function safeClone(value: unknown): unknown {
  if (value === null || typeof value !== 'object') return value;
  try {
    return structuredClone(value);
  } catch {
    return value; // 不可克隆的值（函数等）按引用记录
  }
}
