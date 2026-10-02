/**
 * 细粒度响应式核心：Signal / Computed / Effect
 *
 * 防钻石依赖（glitch-free）机制：
 *
 * 1. 拓扑染色（topological coloring）
 *    每个节点携带拓扑高度 height：Signal 为 0，
 *    Computed 为 1 + max(依赖高度)，Effect 视为 +∞（永远是汇）。
 *    依赖变更沿订阅边扩散时，下游节点被"染色"（标记 dirty）并按高度入队。
 *
 * 2. 两阶段更新（two-phase update）
 *    - 阶段一（标记）：源信号写入后，沿订阅边 DFS，把全部下游 Computed
 *      标记为 dirty 并按高度入优先队列，Effect 进入待执行集合。
 *      此阶段不执行任何用户计算，因此不会读到半更新状态。
 *    - 阶段二（更新）：按高度从小到大依次出队重算。由于依赖的高度恒小于
 *      当前节点，任何 Computed 重算时其全部依赖必然已结算为最新值。
 *      钻石汇合点 D 只入队一次、只计算一次，且读不到中间瞬态脏值。
 *    - 阶段三（副作用）：图完全一致后才运行 Effect。
 *
 * 另配版本号（version）校验：节点仅在某个依赖的版本号真实前进时才重算，
 * 因此"被染色但值未变"的分支不会引发无谓的下游重算。
 */

let globalVersion = 0;

/** 依赖节点（可被订阅）：Signal 或 Computed */
class ReactiveNode {
  /** 订阅了本节点的下游节点 */
  readonly downstream = new Set<Computed<unknown> | Effect>();
  /** 拓扑高度 */
  height = 0;
  /** 当前值的版本号（值变化时单调递增） */
  get version(): number {
    return 0;
  }
}

// ---------------------------------------------------------------------------
// 依赖收集
// ---------------------------------------------------------------------------

interface Collector {
  collect(dep: ReactiveNode): void;
}

let activeCollector: Collector | null = null;

function trackRead(dep: ReactiveNode): void {
  if (activeCollector !== null) activeCollector.collect(dep);
}

// ---------------------------------------------------------------------------
// 调度器：染色队列（按拓扑高度有序）+ 待执行副作用
// ---------------------------------------------------------------------------

const updateQueue: Computed<unknown>[] = [];
const pendingEffects = new Set<Effect>();
let flushing = false;
let batchDepth = 0;

/** 按拓扑高度二分插入，保证出队顺序为拓扑序 */
function enqueue(node: Computed<unknown>): void {
  let lo = 0;
  let hi = updateQueue.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (updateQueue[mid].height <= node.height) lo = mid + 1;
    else hi = mid;
  }
  updateQueue.splice(lo, 0, node);
}

/** 阶段一：沿订阅边 DFS 染色，标记全部下游节点 */
function markDownstream(subs: Set<Computed<unknown> | Effect>): void {
  const stack = [...subs];
  while (stack.length > 0) {
    const node = stack.pop() as Computed<unknown> | Effect;
    if (node instanceof Effect) {
      node._schedule();
      continue;
    }
    // 首次染色才继续向下扩散，避免钻石汇合点被重复入队
    if (node._markDirty()) {
      for (const next of node.downstream) stack.push(next);
    }
  }
}

/** 阶段二 + 阶段三：按拓扑序结算 Computed，图一致后运行 Effect */
function flush(): void {
  if (flushing || batchDepth > 0) return;
  flushing = true;
  try {
    while (updateQueue.length > 0 || pendingEffects.size > 0) {
      while (updateQueue.length > 0) {
        const node = updateQueue.shift() as Computed<unknown>;
        node._updateIfNeeded();
      }
      if (pendingEffects.size > 0) {
        const effects = [...pendingEffects];
        pendingEffects.clear();
        for (const e of effects) e._runIfStale();
      }
    }
  } finally {
    flushing = false;
  }
}

function onSourceWrite(subs: Set<Computed<unknown> | Effect>): void {
  markDownstream(subs);
  flush();
}

/** 将多次写入合并为一次传播 */
export function batch(fn: () => void): void {
  batchDepth++;
  try {
    fn();
  } finally {
    batchDepth--;
    if (batchDepth === 0) flush();
  }
}

// ---------------------------------------------------------------------------
// Signal
// ---------------------------------------------------------------------------

export class Signal<T> extends ReactiveNode {
  #value: T;
  #version = 0;

  constructor(initial: T) {
    super();
    this.height = 0;
    this.#value = initial;
  }

  get value(): T {
    trackRead(this);
    return this.#value;
  }

  set value(next: T) {
    if (Object.is(next, this.#value)) return;
    this.#value = next;
    this.#version = ++globalVersion;
    onSourceWrite(this.downstream);
  }

  /** 不触发依赖收集的读取 */
  peek(): T {
    return this.#value;
  }

  override get version(): number {
    return this.#version;
  }
}

// ---------------------------------------------------------------------------
// Computed
// ---------------------------------------------------------------------------

export class Computed<T> extends ReactiveNode {
  #fn: () => T;
  #value!: T;
  #version = 0;
  #dirty = false;
  #deps: ReactiveNode[] = [];
  #depVersions: number[] = [];
  #pendingDeps: ReactiveNode[] | null = null;
  /** 观测用：计算函数实际执行次数 */
  computeCount = 0;

  constructor(fn: () => T) {
    super();
    this.height = 1;
    this.#fn = fn;
  }

  get value(): T {
    trackRead(this);
    if (this.#version === 0) {
      this.#recompute();
    } else if (this.#dirty) {
      // batch 期间被读取：先自底向上结算依赖，保证不吐脏值
      this.#refreshSync();
    }
    return this.#value;
  }

  peek(): T {
    if (this.#version === 0) this.#recompute();
    else if (this.#dirty) this.#refreshSync();
    return this.#value;
  }

  override get version(): number {
    return this.#version;
  }

  collect(dep: ReactiveNode): void {
    const pending = this.#pendingDeps as ReactiveNode[];
    if (!pending.includes(dep)) pending.push(dep);
  }

  /** 阶段一：染色。返回是否为首次染色（决定是否继续扩散） */
  _markDirty(): boolean {
    if (this.#dirty) return false;
    this.#dirty = true;
    enqueue(this as Computed<unknown>);
    return true;
  }

  /** 阶段二：仅当某个依赖的版本号真实前进时才重算 */
  _updateIfNeeded(): void {
    if (!this.#dirty) return;
    this.#dirty = false;
    let stale = this.#version === 0;
    if (!stale) {
      for (let i = 0; i < this.#deps.length; i++) {
        if (this.#deps[i].version !== this.#depVersions[i]) {
          stale = true;
          break;
        }
      }
    }
    if (stale) this.#recompute();
  }

  /** batch 期间的同步拉取刷新：先递归结算依赖，再结算自己 */
  #refreshSync(): void {
    for (const dep of this.#deps) {
      if (dep instanceof Computed && dep.#dirty) dep.#refreshSync();
    }
    this.#dirty = true;
    this._updateIfNeeded();
  }

  #recompute(): void {
    const prevCollector = activeCollector;
    activeCollector = this;
    this.#pendingDeps = [];
    let next: T;
    try {
      next = this.#fn();
    } finally {
      activeCollector = prevCollector;
    }
    const deps = this.#pendingDeps as ReactiveNode[];
    this.#pendingDeps = null;

    for (const dep of this.#deps) {
      if (!deps.includes(dep)) dep.downstream.delete(this as Computed<unknown>);
    }
    for (const dep of deps) {
      dep.downstream.add(this as Computed<unknown>);
    }
    this.#deps = deps;
    this.#depVersions = deps.map((d) => d.version);

    // 拓扑高度只增不减，保证队列的拓扑序始终成立
    let height = 1;
    for (const dep of deps) height = Math.max(height, dep.height + 1);
    if (height > this.height) {
      this.height = height;
      this.#raiseDownstream();
    }

    this.computeCount++;
    // 值未变则不推进版本号，下游的版本校验会因此跳过重算
    if (this.#version === 0 || !Object.is(next, this.#value)) {
      this.#value = next;
      this.#version = ++globalVersion;
    }
  }

  #raiseDownstream(): void {
    for (const node of this.downstream) {
      if (node instanceof Computed && node.height < this.height + 1) {
        node.height = this.height + 1;
        node.#raiseDownstream();
      }
    }
  }
}

// ---------------------------------------------------------------------------
// Effect
// ---------------------------------------------------------------------------

export class Effect extends ReactiveNode {
  #fn: () => void;
  #deps: ReactiveNode[] = [];
  #depVersions: number[] = [];
  #pendingDeps: ReactiveNode[] | null = null;
  #scheduled = false;
  #disposed = false;
  /** 观测用：副作用实际执行次数 */
  runCount = 0;

  constructor(fn: () => void) {
    super();
    this.height = Number.MAX_SAFE_INTEGER;
    this.#fn = fn;
    this.#execute();
  }

  collect(dep: ReactiveNode): void {
    const pending = this.#pendingDeps as ReactiveNode[];
    if (!pending.includes(dep)) pending.push(dep);
  }

  _schedule(): void {
    if (this.#disposed || this.#scheduled) return;
    this.#scheduled = true;
    pendingEffects.add(this);
  }

  _runIfStale(): void {
    this.#scheduled = false;
    if (this.#disposed) return;
    let stale = false;
    for (let i = 0; i < this.#deps.length; i++) {
      if (this.#deps[i].version !== this.#depVersions[i]) {
        stale = true;
        break;
      }
    }
    if (stale) this.#execute();
  }

  #execute(): void {
    const prevCollector = activeCollector;
    activeCollector = this;
    this.#pendingDeps = [];
    try {
      this.#fn();
    } finally {
      activeCollector = prevCollector;
    }
    const deps = this.#pendingDeps as ReactiveNode[];
    this.#pendingDeps = null;

    for (const dep of this.#deps) {
      if (!deps.includes(dep)) dep.downstream.delete(this);
    }
    for (const dep of deps) {
      dep.downstream.add(this);
    }
    this.#deps = deps;
    this.#depVersions = deps.map((d) => d.version);
    this.runCount++;
  }

  dispose(): void {
    if (this.#disposed) return;
    this.#disposed = true;
    for (const dep of this.#deps) dep.downstream.delete(this);
    this.#deps = [];
    this.#depVersions = [];
    pendingEffects.delete(this);
  }
}

// ---------------------------------------------------------------------------
// 工厂函数
// ---------------------------------------------------------------------------

export function signal<T>(initial: T): Signal<T> {
  return new Signal(initial);
}

export function computed<T>(fn: () => T): Computed<T> {
  return new Computed(fn);
}

/** 返回 dispose 函数 */
export function effect(fn: () => void): () => void {
  const e = new Effect(fn);
  return () => e.dispose();
}
