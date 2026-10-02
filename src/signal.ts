/**
 * Reactivity core: signals with automatic dependency tracking.
 *
 * A Computation records every Signal it reads while running. When one of
 * those signals later changes, exactly that computation is re-scheduled,
 * which is what allows the engine to skip any tree-wide diff.
 */
interface Dep {
  _subscribe(computation: Computation): void;
  _unsubscribe(computation: Computation): void;
}

let currentComputation: Computation | null = null;

const dirtyQueue: Computation[] = [];
let isFlushing = false;

function schedule(computation: Computation): void {
  if (computation.queued || computation.disposed) return;
  computation.queued = true;
  dirtyQueue.push(computation);
  if (!isFlushing) flushQueue();
}

function flushQueue(): void {
  isFlushing = true;
  try {
    while (dirtyQueue.length > 0) {
      const computation = dirtyQueue.shift()!;
      computation.queued = false;
      if (!computation.disposed) computation.run();
    }
  } finally {
    isFlushing = false;
  }
}

/** Defers recomputation until the whole block has run (each computation re-runs at most once). */
export function batch(fn: () => void): void {
  const wasFlushing = isFlushing;
  isFlushing = true;
  try {
    fn();
  } finally {
    isFlushing = wasFlushing;
    if (!isFlushing) flushQueue();
  }
}

export class Computation {
  readonly deps = new Set<Dep>();
  queued = false;
  disposed = false;
  private readonly fn: () => void;

  constructor(fn: () => void) {
    this.fn = fn;
  }

  run(): void {
    if (this.disposed) return;
    for (const dep of this.deps) dep._unsubscribe(this);
    this.deps.clear();
    const previous = currentComputation;
    currentComputation = this;
    try {
      this.fn();
    } finally {
      currentComputation = previous;
    }
  }

  notify(): void {
    schedule(this);
  }

  dispose(): void {
    this.disposed = true;
    for (const dep of this.deps) dep._unsubscribe(this);
    this.deps.clear();
  }
}

export class Signal<T> implements Dep {
  private readonly subscribers = new Set<Computation>();
  private value: T;

  constructor(value: T) {
    this.value = value;
  }

  _subscribe(computation: Computation): void {
    this.subscribers.add(computation);
  }

  _unsubscribe(computation: Computation): void {
    this.subscribers.delete(computation);
  }

  /** Reactive read: registers the currently running computation as a dependent. */
  get(): T {
    if (currentComputation !== null) {
      this.subscribers.add(currentComputation);
      currentComputation.deps.add(this);
    }
    return this.value;
  }

  /** Non-reactive read. */
  peek(): T {
    return this.value;
  }

  set(next: T): void {
    if (Object.is(next, this.value)) return;
    this.value = next;
    for (const computation of [...this.subscribers]) computation.notify();
  }

  update(fn: (value: T) => T): void {
    this.set(fn(this.value));
  }

  get subscriberCount(): number {
    return this.subscribers.size;
  }
}

export function signal<T>(initial: T): Signal<T> {
  return new Signal(initial);
}

/** Runs fn immediately and re-runs it whenever any signal it read changes. */
export function effect(fn: () => void): () => void {
  const computation = new Computation(fn);
  computation.run();
  return () => computation.dispose();
}

const signalCache = new WeakMap<object, Map<PropertyKey, Signal<unknown>>>();

function keySignal(target: object, key: PropertyKey): Signal<unknown> {
  let perKey = signalCache.get(target);
  if (perKey === undefined) {
    perKey = new Map();
    signalCache.set(target, perKey);
  }
  let entry = perKey.get(key);
  if (entry === undefined) {
    entry = new Signal((target as Record<PropertyKey, unknown>)[key]);
    perKey.set(key, entry);
  }
  return entry;
}

/**
 * Wraps a plain object so every property is backed by its own fine-grained
 * signal: user.name inside a binding tracks only the "name" key.
 */
export function reactive<T extends object>(obj: T): T {
  return new Proxy(obj, {
    get(target, key, receiver) {
      if (typeof key === "symbol") return Reflect.get(target, key, receiver);
      return keySignal(target, key).get();
    },
    set(target, key, value) {
      keySignal(target, key).set(value);
      return true;
    },
  });
}
