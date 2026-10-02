/**
 * 响应式核心：Signal / Computed / Effect
 * - 自动依赖追踪
 * - 批量事务（batch）：多次写入合并为一次通知 + 一次历史提交
 * - 提交钩子（onCommit）：供时间旅行控制器记录快照
 */

export type Unsubscribe = () => void;

/** 可被依赖追踪的节点（Signal 或 Computed） */
interface Trackable {
  subscribe(listener: () => void): Unsubscribe;
}

/** 依赖收集器：get() 时上报依赖 */
let activeCollector: ((dep: Trackable) => void) | null = null;

/** 批量事务深度与待通知队列 */
let batchDepth = 0;
let flushing = false;
const pendingNodes = new Set<{ emitNow(): void }>();
const pendingEffects = new Set<() => void>();
let pendingCommit = false;

type CommitListener = () => void;
const commitListeners = new Set<CommitListener>();

/** 订阅“一次状态提交”事件（每次 batch 结束或单次 set 后触发） */
export function onCommit(listener: CommitListener): Unsubscribe {
  commitListeners.add(listener);
  return () => commitListeners.delete(listener);
}

function flush(): void {
  if (batchDepth > 0 || flushing) return;
  flushing = true;
  try {
    while (pendingNodes.size > 0 || pendingEffects.size > 0) {
      // 先排空所有节点通知（含级联的 Computed 失效），再统一执行 effects，
      // 保证 effect 不会在中间态被触发
      while (pendingNodes.size > 0) {
        const nodes = [...pendingNodes];
        pendingNodes.clear();
        for (const node of nodes) node.emitNow();
      }
      while (pendingEffects.size > 0) {
        const effects = [...pendingEffects];
        pendingEffects.clear();
        for (const job of effects) job();
      }
    }
  } finally {
    flushing = false;
  }
  if (pendingCommit) {
    pendingCommit = false;
    for (const listener of [...commitListeners]) listener();
  }
}

function scheduleEmit(node: { emitNow(): void }): void {
  pendingNodes.add(node);
  flush();
}

/** 批量执行多次修改，合并为一次通知与一次历史快照 */
export function batch<T>(fn: () => T): T {
  batchDepth++;
  try {
    return fn();
  } finally {
    batchDepth--;
    flush();
  }
}

abstract class ReactiveNode implements Trackable {
  private listeners = new Set<() => void>();

  subscribe(listener: () => void): Unsubscribe {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  protected notify(): void {
    scheduleEmit(this);
  }

  emitNow(): void {
    for (const listener of [...this.listeners]) listener();
  }
}

export interface Signal<T> {
  readonly id: number;
  get(): T;
  /** 读取但不建立依赖（供快照/调试使用） */
  peek(): T;
  set(value: T): void;
  update(fn: (prev: T) => T): void;
  subscribe(listener: () => void): Unsubscribe;
}

let nextSignalId = 0;
const signalRegistry: Array<SignalImpl<unknown>> = [];

class SignalImpl<T> extends ReactiveNode implements Signal<T> {
  readonly id: number;
  private value: T;

  constructor(initial: T) {
    super();
    this.id = nextSignalId++;
    this.value = initial;
    signalRegistry.push(this as SignalImpl<unknown>);
  }

  get(): T {
    if (activeCollector) activeCollector(this);
    return this.value;
  }

  peek(): T {
    return this.value;
  }

  set(value: T): void {
    if (Object.is(this.value, value)) return;
    this.value = value;
    pendingCommit = true;
    this.notify();
  }

  update(fn: (prev: T) => T): void {
    this.set(fn(this.value));
  }
}

export function signal<T>(initial: T): Signal<T> {
  return new SignalImpl(initial);
}

export interface Computed<T> {
  get(): T;
  peek(): T;
  subscribe(listener: () => void): Unsubscribe;
}

class ComputedImpl<T> extends ReactiveNode implements Computed<T> {
  private dirty = true;
  private cached!: T;
  private depCleanups: Unsubscribe[] = [];
  private readonly compute: () => T;

  constructor(compute: () => T) {
    super();
    this.compute = compute;
  }

  get(): T {
    if (activeCollector) activeCollector(this);
    if (this.dirty) this.recompute();
    return this.cached;
  }

  peek(): T {
    return this.get();
  }

  private recompute(): void {
    for (const cleanup of this.depCleanups) cleanup();
    this.depCleanups = [];
    const deps = new Set<Trackable>();
    const prevCollector = activeCollector;
    activeCollector = (dep) => deps.add(dep);
    try {
      this.cached = this.compute();
    } finally {
      activeCollector = prevCollector;
    }
    for (const dep of deps) {
      this.depCleanups.push(dep.subscribe(() => this.invalidate()));
    }
    this.dirty = false;
  }

  private invalidate(): void {
    if (this.dirty) return;
    this.dirty = true;
    this.notify();
  }
}

export function computed<T>(fn: () => T): Computed<T> {
  return new ComputedImpl(fn);
}

/** 副作用：依赖变化时自动重跑（同一事务内去重，只重跑一次） */
export function effect(fn: () => void): Unsubscribe {
  let cleanups: Unsubscribe[] = [];
  let queued = false;
  let disposed = false;

  const run = (): void => {
    queued = false;
    if (disposed) return;
    for (const cleanup of cleanups) cleanup();
    cleanups = [];
    const deps = new Set<Trackable>();
    const prevCollector = activeCollector;
    activeCollector = (dep) => deps.add(dep);
    try {
      fn();
    } finally {
      activeCollector = prevCollector;
    }
    for (const dep of deps) cleanups.push(dep.subscribe(schedule));
  };

  const schedule = (): void => {
    if (queued || disposed) return;
    queued = true;
    pendingEffects.add(run);
    flush();
  };

  run();

  return () => {
    disposed = true;
    for (const cleanup of cleanups) cleanup();
    cleanups = [];
  };
}

/* ---- 内部接口：仅供历史控制器使用 ---- */

export interface SignalHandle {
  readonly id: number;
  peek(): unknown;
  set(value: unknown): void;
}

export function __listSignals(): readonly SignalHandle[] {
  return signalRegistry;
}
