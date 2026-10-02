/**
 * 响应式核心：Signal / Computed / Effect。
 * 所有读写都会经过 guard（守护器）记录与检查。
 */

import { guard } from './guard.ts';

export interface DebugOptions {
  /** 调试名称，出现在因果链报告中 */
  name?: string;
}

export interface EffectHandle {
  /** 停止响应并退订所有依赖 */
  dispose(): void;
  readonly label: string;
}

interface Subscribable {
  readonly label: string;
  subscribe(observer: Observer): void;
  unsubscribe(observer: Observer): void;
}

type Observer = EffectImpl | ComputedImpl<unknown>;

let nextId = 1;
let activeObserver: Observer | null = null;

const pending: Observer[] = [];
let flushing = false;

function formatValue(value: unknown): string {
  let text: string;
  try {
    text = JSON.stringify(value) ?? String(value);
  } catch {
    text = String(value);
  }
  return text.length > 40 ? `${text.slice(0, 37)}...` : text;
}

function schedule(observer: Observer): void {
  if (!pending.includes(observer)) {
    pending.push(observer);
  }
}

function flush(): void {
  if (flushing) {
    return;
  }
  flushing = true;
  guard.enterCascade();
  try {
    while (pending.length > 0) {
      const observer = pending.shift() as Observer;
      guard.beginIteration(observer.label);
      observer.execute();
    }
  } finally {
    flushing = false;
    pending.length = 0;
    guard.exitCascade();
  }
}

class SignalImpl<T> implements Subscribable {
  readonly label: string;
  private value: T;
  private readonly subscribers = new Set<Observer>();

  constructor(initial: T, name?: string) {
    this.value = initial;
    this.label = `Signal#${nextId++}${name ? ` "${name}"` : ''}`;
  }

  get(): T {
    if (activeObserver !== null) {
      this.subscribe(activeObserver);
      activeObserver.deps.add(this);
      guard.log(`[read  ] ${activeObserver.label} 读取 ${this.label} (value=${formatValue(this.value)})`);
    }
    return this.value;
  }

  set(next: T): void {
    guard.enterCascade();
    try {
      const previous = this.value;
      const writer = activeObserver;
      if (writer !== null) {
        const relation = writer.dependencyRelationTo(this);
        let note = '';
        if (relation !== null) {
          note = `  << 自依赖写入（${relation}）>>`;
          guard.recordSelfWrite({
            observerLabel: writer.label,
            signalLabel: this.label,
            relation,
          });
        }
        guard.log(`[write ] ${writer.label} 写入 ${this.label}: ${formatValue(previous)} -> ${formatValue(next)}${note}`);
      } else {
        guard.log(`[write ] 外部代码 写入 ${this.label}: ${formatValue(previous)} -> ${formatValue(next)}`);
      }
      this.value = next;
      if (Object.is(previous, next)) {
        return;
      }
      for (const subscriber of [...this.subscribers]) {
        guard.log(`[notify] ${this.label} 变更，调度 ${subscriber.label}`);
        subscriber.onDependencyChanged();
      }
      flush();
    } finally {
      guard.exitCascade();
    }
  }

  subscribe(observer: Observer): void {
    this.subscribers.add(observer);
  }

  unsubscribe(observer: Observer): void {
    this.subscribers.delete(observer);
  }
}

class ComputedImpl<T> implements Subscribable {
  readonly label: string;
  readonly deps = new Set<Subscribable>();
  private readonly subscribers = new Set<Observer>();
  private readonly computeFn: () => T;
  private value!: T;
  private dirty = true;

  constructor(computeFn: () => T, name?: string) {
    this.computeFn = computeFn;
    this.label = `Computed#${nextId++}${name ? ` "${name}"` : ''}`;
  }

  get(): T {
    guard.enterCascade();
    try {
      if (activeObserver !== null) {
        this.subscribe(activeObserver);
        activeObserver.deps.add(this);
        guard.log(`[read  ] ${activeObserver.label} 读取 ${this.label}`);
      }
      if (this.dirty) {
        this.recompute();
      }
      return this.value;
    } finally {
      guard.exitCascade();
    }
  }

  private recompute(): void {
    guard.enterRun(this.label);
    for (const dep of this.deps) {
      dep.unsubscribe(this);
    }
    this.deps.clear();
    const previousObserver = activeObserver;
    activeObserver = this as Observer;
    guard.log(`[compute] ${this.label} 重新计算`);
    try {
      this.value = this.computeFn();
      this.dirty = false;
    } finally {
      activeObserver = previousObserver;
      guard.exitRun();
    }
  }

  onDependencyChanged(): void {
    if (this.dirty) {
      return;
    }
    this.dirty = true;
    for (const subscriber of [...this.subscribers]) {
      guard.log(`[notify] ${this.label} 失效，调度 ${subscriber.label}`);
      subscriber.onDependencyChanged();
    }
  }

  execute(): void {
    this.recompute();
  }

  dependencyRelationTo(signal: SignalImpl<unknown>): '直接' | '间接' | null {
    if ((this.deps as Set<Subscribable>).has(signal)) {
      return '直接';
    }
    return dependencyClosureContains(this.deps, signal) ? '间接' : null;
  }

  subscribe(observer: Observer): void {
    this.subscribers.add(observer);
  }

  unsubscribe(observer: Observer): void {
    this.subscribers.delete(observer);
  }
}

class EffectImpl {
  readonly label: string;
  readonly deps = new Set<Subscribable>();
  private readonly fn: () => void;
  private disposed = false;

  constructor(fn: () => void, name?: string) {
    this.fn = fn;
    this.label = `Effect#${nextId++}${name ? ` "${name}"` : ''}`;
  }

  execute(): void {
    if (this.disposed) {
      return;
    }
    guard.enterRun(this.label);
    for (const dep of this.deps) {
      dep.unsubscribe(this as Observer);
    }
    this.deps.clear();
    const previousObserver = activeObserver;
    activeObserver = this as Observer;
    guard.log(`[run   ] ${this.label} 开始执行`);
    try {
      this.fn();
    } finally {
      activeObserver = previousObserver;
      guard.exitRun();
    }
  }

  onDependencyChanged(): void {
    schedule(this as Observer);
  }

  dependencyRelationTo(signal: SignalImpl<unknown>): '直接' | '间接' | null {
    if ((this.deps as Set<Subscribable>).has(signal)) {
      return '直接';
    }
    return dependencyClosureContains(this.deps, signal) ? '间接' : null;
  }

  dispose(): void {
    this.disposed = true;
    for (const dep of this.deps) {
      dep.unsubscribe(this as Observer);
    }
    this.deps.clear();
  }
}

/** 沿 Computed 依赖链向下查找，判断 signal 是否在依赖闭包中（间接自依赖） */
function dependencyClosureContains(deps: Set<Subscribable>, signal: SignalImpl<unknown>): boolean {
  const visited = new Set<Subscribable>();
  const stack: Subscribable[] = [...deps];
  while (stack.length > 0) {
    const dep = stack.pop() as Subscribable;
    if (visited.has(dep)) {
      continue;
    }
    visited.add(dep);
    if ((dep as unknown) === signal) {
      return true;
    }
    if (dep instanceof ComputedImpl) {
      for (const inner of dep.deps) {
        stack.push(inner);
      }
    }
  }
  return false;
}

export interface Signal<T> {
  get(): T;
  set(value: T): void;
  readonly label: string;
}

export interface Computed<T> {
  get(): T;
  readonly label: string;
}

/** 创建响应式信号 */
export function signal<T>(initial: T, options?: DebugOptions): Signal<T> {
  return new SignalImpl(initial, options?.name) as Signal<T>;
}

/** 创建派生计算节点（惰性求值，依赖变更后自动失效） */
export function computed<T>(computeFn: () => T, options?: DebugOptions): Computed<T> {
  return new ComputedImpl(computeFn, options?.name) as Computed<T>;
}

/** 创建副作用节点，立即执行一次，依赖变更后自动重新执行 */
export function effect(fn: () => void, options?: DebugOptions): EffectHandle {
  const impl = new EffectImpl(fn, options?.name);
  schedule(impl as Observer);
  flush();
  return {
    dispose: () => impl.dispose(),
    label: impl.label,
  };
}
