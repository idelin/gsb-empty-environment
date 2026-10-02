import { ReactiveLoopGuard } from './guard.ts';

export interface Signal<T> {
  readonly label: string;
  value: T;
}

export interface Computed<T> {
  readonly label: string;
  readonly value: T;
}

export interface EffectHandle {
  readonly label: string;
  dispose(): void;
}

export interface ReactiveSystem {
  readonly guard: ReactiveLoopGuard;
  readonly Signal: new <T>(value: T, name?: string) => Signal<T>;
  readonly Computed: new <T>(fn: () => T, name?: string) => Computed<T>;
  readonly Effect: new (fn: () => void, name?: string) => EffectHandle;
}

export interface ReactiveSystemOptions {
  maxIterations?: number;
}

interface Subscribable {
  readonly label: string;
  unsubscribe(observer: Observer): void;
}

interface Observer {
  readonly label: string;
  addDependency(source: Subscribable): void;
  notify(source: Subscribable): void;
}

interface Job {
  readonly label: string;
  run(): void;
}

export function createReactiveSystem(options: ReactiveSystemOptions = {}): ReactiveSystem {
  const guard = new ReactiveLoopGuard(options.maxIterations ?? 50);
  let idCounter = 0;
  const autoLabel = (kind: string): string => `${kind}#${++idCounter}`;

  let activeObserver: Observer | null = null;
  const queue: Job[] = [];
  let flushing = false;

  function flushQueue(): void {
    if (flushing) return;
    flushing = true;
    guard.beginEpoch();
    try {
      while (queue.length > 0) {
        const job = queue.shift()!;
        guard.recordIteration(job.label);
        job.run();
      }
    } finally {
      flushing = false;
      queue.length = 0;
      guard.endEpoch();
    }
  }

  function kick(): void {
    if (!flushing && activeObserver === null && queue.length > 0) {
      flushQueue();
    }
  }

  class SignalImpl<T> implements Subscribable {
    readonly label: string;
    private current: T;
    private readonly subscribers = new Set<Observer>();

    constructor(value: T, name?: string) {
      this.current = value;
      this.label = name ?? autoLabel('signal');
    }

    get value(): T {
      if (activeObserver !== null) {
        this.subscribers.add(activeObserver);
        activeObserver.addDependency(this);
      }
      return this.current;
    }

    set value(next: T) {
      if (Object.is(next, this.current)) return;
      const writer = activeObserver;
      this.current = next;
      guard.record(writer === null ? '<external>' : writer.label, this.label, 'writes');
      for (const sub of [...this.subscribers]) {
        guard.record(this.label, sub.label, 'notifies');
        sub.notify(this);
      }
      kick();
    }

    unsubscribe(observer: Observer): void {
      this.subscribers.delete(observer);
    }
  }

  class ComputedImpl<T> implements Subscribable, Observer {
    readonly label: string;
    private readonly fn: () => T;
    private readonly deps = new Set<Subscribable>();
    private readonly subscribers = new Set<Observer>();
    private cached!: T;
    private dirty = true;
    private computing = false;
    private selfInvalidated = false;

    constructor(fn: () => T, name?: string) {
      this.fn = fn;
      this.label = name ?? autoLabel('computed');
    }

    get value(): T {
      if (activeObserver !== null) {
        this.subscribers.add(activeObserver);
        activeObserver.addDependency(this);
      }
      if (this.dirty) this.recompute();
      return this.cached;
    }

    addDependency(source: Subscribable): void {
      this.deps.add(source);
    }

    notify(_source: Subscribable): void {
      if (this.computing) {
        this.selfInvalidated = true;
      } else if (this.dirty) {
        return;
      }
      this.dirty = true;
      for (const sub of [...this.subscribers]) {
        guard.record(this.label, sub.label, 'notifies');
        sub.notify(this);
      }
    }

    unsubscribe(observer: Observer): void {
      this.subscribers.delete(observer);
    }

    private recompute(): void {
      guard.enterNode(this.label);
      for (const dep of this.deps) dep.unsubscribe(this);
      this.deps.clear();
      const prev = activeObserver;
      activeObserver = this;
      this.computing = true;
      this.selfInvalidated = false;
      try {
        this.cached = this.fn();
        this.dirty = this.selfInvalidated;
      } finally {
        this.computing = false;
        activeObserver = prev;
        guard.exitNode(this.label);
      }
      kick();
    }
  }

  class EffectImpl implements Observer, Job {
    readonly label: string;
    private readonly fn: () => void;
    private readonly deps = new Set<Subscribable>();
    private enqueued = false;
    private disposed = false;

    constructor(fn: () => void, name?: string) {
      this.fn = fn;
      this.label = name ?? autoLabel('effect');
      this.run();
    }

    addDependency(source: Subscribable): void {
      this.deps.add(source);
    }

    notify(_source: Subscribable): void {
      if (this.disposed || this.enqueued) return;
      this.enqueued = true;
      queue.push(this);
    }

    run(): void {
      if (this.disposed) return;
      this.enqueued = false;
      for (const dep of this.deps) dep.unsubscribe(this);
      this.deps.clear();
      guard.enterNode(this.label);
      const prev = activeObserver;
      activeObserver = this;
      try {
        this.fn();
      } finally {
        activeObserver = prev;
        guard.exitNode(this.label);
      }
      kick();
    }

    dispose(): void {
      this.disposed = true;
      for (const dep of this.deps) dep.unsubscribe(this);
      this.deps.clear();
    }
  }

  return {
    guard,
    Signal: SignalImpl,
    Computed: ComputedImpl,
    Effect: EffectImpl,
  };
}
