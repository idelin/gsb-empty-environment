/**
 * 细粒度响应式核心：Signal / Computed / Effect
 *
 * 防钻石依赖（glitch-free）的两个关键机制：
 *
 * 1. 拓扑染色（topological coloring / level）
 *    每个节点维护一个 level：Signal 为 0，Computed/Effect 为 1 + max(依赖的 level)。
 *    依赖图因此天然分层，下游节点的 level 恒大于其所有上游节点。
 *
 * 2. 两阶段更新（two-phase update）
 *    阶段一（标记 / 染色）：源信号写入后，按 level 升序（小顶堆）遍历下游子图，
 *      仅把节点染为 STALE，不做任何重算。由于按拓扑序染色，父节点一定先于
 *      子节点被染色，且每个节点只染色一次（STALE 去重）。
 *    阶段二（结算 / 拉取）：批量结束后，按 level 升序运行 Effect；Effect 读取
 *      Computed 时惰性拉取（pull）重算。重算沿依赖边向上递归，B、C 先于 D
 *      被拉到最新值，因此 D 每次更新周期只计算一次，且读到的依赖必然全是
 *      最新版本，绝不会看到新旧混杂的中间瞬态。
 */

const CLEAN = 0;
const STALE = 1;
type State = typeof CLEAN | typeof STALE;

let tick = 0; // 全局版本号，随每次有效写入单调递增
let batchDepth = 0;
let activeObserver: ReactiveNode | null = null;

const pendingEffects = new Set<Effect>();

/** 按 level 升序的小顶堆，用于拓扑序遍历 */
class LevelHeap<T extends { level: number }> {
  private items: T[] = [];

  get size(): number {
    return this.items.length;
  }

  push(item: T): void {
    const items = this.items;
    items.push(item);
    let i = items.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (items[parent].level <= items[i].level) break;
      [items[parent], items[i]] = [items[i], items[parent]];
      i = parent;
    }
  }

  pop(): T | undefined {
    const items = this.items;
    const top = items[0];
    const last = items.pop();
    if (items.length > 0 && last !== undefined) {
      items[0] = last;
      let i = 0;
      for (;;) {
        const left = i * 2 + 1;
        const right = left + 1;
        let smallest = i;
        if (left < items.length && items[left].level < items[smallest].level) smallest = left;
        if (right < items.length && items[right].level < items[smallest].level) smallest = right;
        if (smallest === i) break;
        [items[smallest], items[i]] = [items[i], items[smallest]];
        i = smallest;
      }
    }
    return top;
  }
}

/**
 * 响应式图节点基类。
 * 作为数据源时被下游订阅（subscribers）；作为观察者时订阅上游（deps）。
 * Signal 只用前者，Effect 只用后者，Computed 两者皆用。
 */
abstract class ReactiveNode {
  abstract level: number;
  /** 值最近一次变化时的全局版本号 */
  version = 0;
  state: State = CLEAN;
  readonly subscribers = new Set<ReactiveNode>();
  readonly deps = new Set<ReactiveNode>();

  protected unsubscribeAll(): void {
    for (const dep of this.deps) dep.subscribers.delete(this);
    this.deps.clear();
  }

  /** 拓扑染色高度：1 + max(依赖 level)，在重算/重跑后调用 */
  protected refreshLevel(): void {
    let level = 1;
    for (const dep of this.deps) {
      if (dep.level + 1 > level) level = dep.level + 1;
    }
    this.level = level;
  }
}

function subscribe(source: ReactiveNode): void {
  const observer = activeObserver;
  if (observer === null || observer.deps.has(source)) return;
  observer.deps.add(source);
  source.subscribers.add(observer);
}

/**
 * 阶段一：拓扑染色。
 * 从变更源出发，按 level 升序把下游子图染为 STALE；
 * Effect 入待运行队列，Computed 继续向下传播。纯标记，不重算。
 */
function markSubscribers(source: ReactiveNode): void {
  const heap = new LevelHeap<ReactiveNode>();
  for (const sub of source.subscribers) heap.push(sub);
  while (heap.size > 0) {
    const node = heap.pop()!;
    if (node.state === STALE) continue; // 已被染色：钻石汇合点只处理一次
    node.state = STALE;
    if (node instanceof Effect) {
      pendingEffects.add(node);
    } else {
      for (const sub of node.subscribers) heap.push(sub);
    }
  }
}

/** 阶段二：结算。按 level 升序运行被染色的 Effect，Computed 随读取惰性重算。 */
function flushEffects(): void {
  if (batchDepth > 0) return;
  const heap = new LevelHeap<Effect>();
  for (const effect of pendingEffects) heap.push(effect);
  pendingEffects.clear();
  while (heap.size > 0) {
    const effect = heap.pop()!;
    if (effect.state !== STALE || effect.disposed) continue;
    effect.run();
    // Effect 运行期间可能写入信号，从而染色出新的 Effect
    for (const next of pendingEffects) heap.push(next);
    pendingEffects.clear();
  }
}

export function batch(fn: () => void): void {
  batchDepth++;
  try {
    fn();
  } finally {
    if (--batchDepth === 0) flushEffects();
  }
}

export class Signal<T> extends ReactiveNode {
  readonly level = 0;
  private current: T;

  constructor(value: T) {
    super();
    this.current = value;
  }

  get value(): T {
    subscribe(this);
    return this.current;
  }

  set value(next: T) {
    if (Object.is(this.current, next)) return;
    this.current = next;
    this.version = ++tick;
    batchDepth++;
    try {
      markSubscribers(this);
    } finally {
      if (--batchDepth === 0) flushEffects();
    }
  }

  peek(): T {
    return this.current;
  }

  update(fn: (value: T) => T): void {
    this.value = fn(this.current);
  }
}

export class Computed<T> extends ReactiveNode {
  level = 1;
  state: State = STALE; // 惰性：首次读取才计算
  private current!: T;
  private initialized = false;
  private readonly compute: () => T;

  constructor(compute: () => T) {
    super();
    this.compute = compute;
  }

  get value(): T {
    subscribe(this);
    if (this.state === STALE) this.recompute();
    return this.current;
  }

  peek(): T {
    if (this.state === STALE) this.recompute();
    return this.current;
  }

  private recompute(): void {
    this.unsubscribeAll();
    const prevObserver = activeObserver;
    activeObserver = this;
    let next: T;
    try {
      next = this.compute();
    } finally {
      activeObserver = prevObserver;
    }
    this.refreshLevel();
    this.state = CLEAN;
    if (!this.initialized || !Object.is(this.current, next)) {
      this.current = next;
      this.version = ++tick;
      this.initialized = true;
    }
  }
}

export type Cleanup = () => void;

export class Effect extends ReactiveNode {
  level = 1;
  disposed = false;
  private readonly fn: () => void | Cleanup;
  private cleanup: Cleanup | undefined;

  constructor(fn: () => void | Cleanup) {
    super();
    this.fn = fn;
    this.state = STALE;
    this.run();
  }

  run(): void {
    if (this.disposed) return;
    this.unsubscribeAll();
    if (this.cleanup !== undefined) {
      this.cleanup();
      this.cleanup = undefined;
    }
    const prevObserver = activeObserver;
    activeObserver = this;
    let cleanup: void | Cleanup;
    try {
      cleanup = this.fn();
    } finally {
      activeObserver = prevObserver;
    }
    this.cleanup = typeof cleanup === "function" ? cleanup : undefined;
    this.refreshLevel();
    this.state = CLEAN;
  }

  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    this.unsubscribeAll();
    if (this.cleanup !== undefined) {
      this.cleanup();
      this.cleanup = undefined;
    }
  }
}

export function effect(fn: () => void | Cleanup): () => void {
  const instance = new Effect(fn);
  return () => instance.dispose();
}

export function computed<T>(compute: () => T): Computed<T> {
  return new Computed(compute);
}

export function signal<T>(value: T): Signal<T> {
  return new Signal(value);
}
