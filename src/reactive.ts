export type Cleanup = () => void;

interface Dep {
  subs: Set<() => void>;
}

type Collector = (dep: Dep) => void;

let activeCollector: Collector | null = null;
let batchDepth = 0;
const pendingEffects = new Set<() => void>();

function scheduleEffect(run: () => void): void {
  if (batchDepth > 0) {
    pendingEffects.add(run);
    return;
  }
  run();
}

export function batch<T>(fn: () => T): T {
  batchDepth += 1;
  try {
    return fn();
  } finally {
    batchDepth -= 1;
    if (batchDepth === 0) {
      const queue = [...pendingEffects];
      pendingEffects.clear();
      for (const run of queue) run();
    }
  }
}

let nextSignalId = 0;

export interface Signal<T> {
  readonly id: number;
  get(): T;
  peek(): T;
  set(value: T): void;
  subscribe(listener: (next: T, prev: T) => void): Cleanup;
}

export function signal<T>(initial: T): Signal<T> {
  const dep: Dep = { subs: new Set() };
  const listeners = new Set<() => void>();
  let value = initial;
  return {
    id: nextSignalId++,
    get() {
      activeCollector?.(dep);
      return value;
    },
    peek() {
      return value;
    },
    set(next: T) {
      if (Object.is(value, next)) return;
      const prev = value;
      value = next;
      batch(() => {
        for (const sub of [...dep.subs]) sub();
        for (const listener of [...listeners]) listener(next, prev);
      });
    },
    subscribe(listener: (next: T, prev: T) => void): Cleanup {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
  };
}

export interface Computed<T> {
  get(): T;
  peek(): T;
}

export function computed<T>(fn: () => T): Computed<T> {
  const dep: Dep = { subs: new Set() };
  let deps: Dep[] = [];
  let value: T;
  let dirty = true;

  function markDirty(): void {
    if (dirty) return;
    dirty = true;
    for (const sub of [...dep.subs]) sub();
  }

  function recompute(): void {
    for (const d of deps) d.subs.delete(markDirty);
    deps = [];
    const prev = activeCollector;
    activeCollector = (d) => {
      if (!deps.includes(d)) {
        deps.push(d);
        d.subs.add(markDirty);
      }
    };
    try {
      value = fn();
    } finally {
      activeCollector = prev;
    }
    dirty = false;
  }

  return {
    get() {
      activeCollector?.(dep);
      if (dirty) recompute();
      return value;
    },
    peek() {
      if (dirty) recompute();
      return value;
    },
  };
}

export function effect(fn: () => void): Cleanup {
  let deps: Dep[] = [];
  let disposed = false;

  function schedule(): void {
    scheduleEffect(run);
  }

  function run(): void {
    if (disposed) return;
    for (const d of deps) d.subs.delete(schedule);
    deps = [];
    const prev = activeCollector;
    activeCollector = (d) => {
      if (!deps.includes(d)) {
        deps.push(d);
        d.subs.add(schedule);
      }
    };
    try {
      fn();
    } finally {
      activeCollector = prev;
    }
  }

  run();

  return () => {
    disposed = true;
    for (const d of deps) d.subs.delete(schedule);
    deps = [];
  };
}
