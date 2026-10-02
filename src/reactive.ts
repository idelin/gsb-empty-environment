/**
 * 极简响应式内核：Signal / Computed / Effect。
 * 采用 push 模型：依赖变化时同步调度 Effect 重新执行，
 * Computed 采用惰性求值 + 脏标记，保证依赖链按拓扑序重新对齐。
 */

type AnyNode = ReactiveNode;

let currentObserver: ReactiveNode | null = null;

const pendingEffects = new Set<EffectNode>();
let flushing = false;

function schedule(effect: EffectNode): void {
  pendingEffects.add(effect);
  if (!flushing) flushEffects();
}

function flushEffects(): void {
  flushing = true;
  try {
    while (pendingEffects.size > 0) {
      const batch = [...pendingEffects];
      pendingEffects.clear();
      for (const effect of batch) effect.run();
    }
  } finally {
    flushing = false;
  }
}

abstract class ReactiveNode {
  readonly observers = new Set<ReactiveNode>();
  readonly deps = new Set<AnyNode>();

  track(node: AnyNode): void {
    this.deps.add(node);
    node.observers.add(this);
  }

  untrackAll(): void {
    for (const dep of this.deps) dep.observers.delete(this);
    this.deps.clear();
  }

  notify(): void {
    for (const observer of [...this.observers]) observer.onDependencyChanged();
  }

  abstract onDependencyChanged(): void;
}

export class Signal<T> extends ReactiveNode {
  #value: T;

  constructor(initial: T) {
    super();
    this.#value = initial;
  }

  get(): T {
    currentObserver?.track(this);
    return this.#value;
  }

  set(next: T): void {
    if (Object.is(next, this.#value)) return;
    this.#value = next;
    this.notify();
  }

  onDependencyChanged(): void {
    // Signal 是依赖链的源头，无需响应上游。
  }
}

export class Computed<T> extends ReactiveNode {
  readonly #derive: () => T;
  #value!: T;
  #dirty = true;

  constructor(derive: () => T) {
    super();
    this.#derive = derive;
  }

  get(): T {
    currentObserver?.track(this);
    if (this.#dirty) this.#recompute();
    return this.#value;
  }

  #recompute(): void {
    this.untrackAll();
    const prev = currentObserver;
    currentObserver = this;
    try {
      this.#value = this.#derive();
      this.#dirty = false;
    } finally {
      currentObserver = prev;
    }
  }

  onDependencyChanged(): void {
    if (this.#dirty) return;
    this.#dirty = true;
    this.notify();
  }
}

class EffectNode extends ReactiveNode {
  readonly #fn: () => void;

  constructor(fn: () => void) {
    super();
    this.#fn = fn;
  }

  run(): void {
    this.untrackAll();
    const prev = currentObserver;
    currentObserver = this;
    try {
      this.#fn();
    } finally {
      currentObserver = prev;
    }
  }

  onDependencyChanged(): void {
    schedule(this);
  }
}

export class EffectHandle {
  readonly #node: EffectNode;
  #disposed = false;

  constructor(fn: () => void) {
    this.#node = new EffectNode(fn);
    this.#node.run();
  }

  /** 强制重新对齐：重新执行 Effect 并重建依赖图。 */
  realign(): void {
    if (!this.#disposed) this.#node.run();
  }

  dispose(): void {
    this.#disposed = true;
    this.#node.untrackAll();
  }
}
