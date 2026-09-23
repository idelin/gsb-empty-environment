/**
 * 响应式异步派生图（不依赖任何响应式框架）。
 *
 * - atom：可写源节点；derived：由异步任务计算的派生节点。
 * - derived 状态机：pending -> success | error，作为纯数据存进 Store。
 * - 依赖通过 get(node) 动态收集；依赖变化时取消旧任务（AbortSignal），
 *   并递增代际号 generation，旧任务晚到的结果按代际直接丢弃。
 * - 取消（AbortError）不进入 error 状态，不被当作业务错误。
 * - 节点是单例：多个派生共享同一依赖时，该依赖的任务只执行一次。
 */

import type { Store } from './store.ts';

export type DerivedState<T> =
  | { readonly status: 'pending' }
  | { readonly status: 'success'; readonly value: T }
  | { readonly status: 'error'; readonly error: SerializedError };

export interface SerializedError {
  readonly name: string;
  readonly message: string;
}

export interface Atom<T> {
  readonly key: string;
  get(): T;
  set(value: T): void;
}

export interface Derived<T> {
  readonly key: string;
  state(): DerivedState<T>;
  dispose(): void;
}

export interface TaskContext {
  /** 任务被取消（依赖变化 / dispose）时触发。 */
  readonly signal: AbortSignal;
}

export type Get = {
  <T>(node: Atom<T>): T;
  <T>(node: Derived<T>): T | undefined;
};

export function atom<T>(store: Store, key: string, initial: T): Atom<T> {
  store.transact([{ key, value: initial }]);
  return {
    key,
    get: () => store.snapshot()[key] as T,
    set: (value) => store.transact([{ key, value }]),
  };
}

export function derived<T>(
  store: Store,
  key: string,
  fn: (get: Get, ctx: TaskContext) => Promise<T> | T,
): Derived<T> {
  let generation = 0;
  let controller: AbortController | null = null;
  let deps = new Set<string>();
  let disposed = false;

  const setState = (state: DerivedState<T>): void => {
    store.transact([{ key, value: state }]);
  };

  const isStale = (gen: number): boolean => disposed || gen !== generation;

  const run = (): void => {
    // 依赖变化：取消旧任务，旧结果之后按代际拒绝。
    controller?.abort();
    const gen = ++generation;
    controller = new AbortController();
    const { signal } = controller;
    const currentDeps = new Set<string>();
    deps = currentDeps;

    setState({ status: 'pending' });

    const get: Get = (node: Atom<unknown> | Derived<unknown>): unknown => {
      currentDeps.add(node.key);
      const raw = store.snapshot()[node.key];
      if (isDerivedState(raw)) {
        return raw.status === 'success' ? raw.value : undefined;
      }
      return raw;
    };

    void Promise.resolve()
      .then(() => fn(get as Get, { signal }))
      .then(
        (value) => {
          if (isStale(gen)) return; // 旧代际结果，丢弃
          setState({ status: 'success', value });
        },
        (err: unknown) => {
          if (isStale(gen)) return; // 旧代际失败（含被取消），丢弃
          if (signal.aborted || isAbortError(err)) return; // 取消不是业务错误
          setState({ status: 'error', error: serializeError(err) });
        },
      );
  };

  const unsubscribe = store.subscribe((_snapshot, changedKeys) => {
    if (disposed) return;
    for (const changed of changedKeys) {
      if (deps.has(changed)) {
        run();
        return;
      }
    }
  });

  run();

  return {
    key,
    state: () => {
      const raw = store.snapshot()[key];
      return isDerivedState(raw) ? (raw as DerivedState<T>) : { status: 'pending' };
    },
    dispose: () => {
      if (disposed) return;
      disposed = true;
      controller?.abort();
      unsubscribe();
    },
  };
}

function isDerivedState(value: unknown): value is DerivedState<unknown> {
  return (
    typeof value === 'object' &&
    value !== null &&
    'status' in value &&
    ((value as { status: unknown }).status === 'pending' ||
      (value as { status: unknown }).status === 'success' ||
      (value as { status: unknown }).status === 'error')
  );
}

function isAbortError(err: unknown): boolean {
  return (
    typeof err === 'object' &&
    err !== null &&
    (err as { name?: unknown }).name === 'AbortError'
  );
}

function serializeError(err: unknown): SerializedError {
  if (err instanceof Error) {
    return { name: err.name, message: err.message };
  }
  return { name: 'Error', message: String(err) };
}
