/**
 * Minimal reactive core: Signal / Computed / Effect with automatic
 * dependency tracking. No external dependencies.
 */

interface Dep {
  observers: Set<ReactiveNode>;
}

interface ReactiveNode {
  deps: Set<Dep>;
  run: () => void;
}

let activeNode: ReactiveNode | null = null;

function track(dep: Dep): void {
  if (activeNode) {
    activeNode.deps.add(dep);
    dep.observers.add(activeNode);
  }
}

function notify(dep: Dep): void {
  for (const observer of [...dep.observers]) observer.run();
}

function detach(node: ReactiveNode): void {
  for (const dep of node.deps) dep.observers.delete(node);
  node.deps.clear();
}

function collect(node: ReactiveNode, fn: () => void): void {
  detach(node);
  const prev = activeNode;
  activeNode = node;
  try {
    fn();
  } finally {
    activeNode = prev;
  }
}

export interface Signal<T> {
  get(): T;
  set(value: T): void;
  peek(): T;
  reset(): void;
  subscribe(fn: (value: T) => void): () => void;
}

export interface Computed<T> {
  get(): T;
  peek(): T;
}

class SignalImpl<T> implements Dep {
  readonly observers = new Set<ReactiveNode>();
  private current: T;
  private readonly initial: T;

  constructor(initial: T) {
    this.initial = initial;
    this.current = initial;
  }

  get(): T {
    track(this);
    return this.current;
  }

  peek(): T {
    return this.current;
  }

  set(value: T): void {
    if (Object.is(value, this.current)) return;
    this.current = value;
    notify(this);
  }

  reset(): void {
    this.set(this.initial);
  }

  subscribe(fn: (value: T) => void): () => void {
    return effect(() => fn(this.get()));
  }
}

class ComputedImpl<T> implements Dep {
  readonly observers = new Set<ReactiveNode>();
  private value!: T;
  private dirty = true;
  private readonly node: ReactiveNode;
  private readonly fn: () => T;

  constructor(fn: () => T) {
    this.fn = fn;
    this.node = {
      deps: new Set(),
      run: () => {
        if (this.dirty) return;
        this.dirty = true;
        notify(this);
      },
    };
  }

  get(): T {
    track(this);
    if (this.dirty) this.recompute();
    return this.value;
  }

  peek(): T {
    if (this.dirty) this.recompute();
    return this.value;
  }

  private recompute(): void {
    collect(this.node, () => {
      this.value = this.fn();
    });
    this.dirty = false;
  }
}

export function signal<T>(initial: T): Signal<T> {
  return new SignalImpl(initial);
}

export function computed<T>(fn: () => T): Computed<T> {
  return new ComputedImpl(fn);
}

export function effect(fn: () => void): () => void {
  const node: ReactiveNode = { deps: new Set(), run: execute };
  let disposed = false;

  function execute(): void {
    if (disposed) return;
    collect(node, fn);
  }

  execute();
  return () => {
    disposed = true;
    detach(node);
  };
}
