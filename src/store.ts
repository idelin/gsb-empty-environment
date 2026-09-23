/**
 * 事务式批量状态存储（无响应式框架依赖）。
 *
 * 语义：
 * - 同一调度周期（batch）内的多次更新合并为一次提交；
 * - 函数式更新按顺序读取前一值，直接赋值与函数更新次序保留；
 * - 批次在事务草稿上按序求值，成功后一次性原子提交；
 * - 读者只能看到已提交快照，永远看不到事务中间态；
 * - 失败批次（mutator 抛错 / 不变量违反）不留任何部分值；
 * - 嵌套批次只在最外层通知一次；
 * - 取消只移除尚未提交的排队操作，不回滚后续独立更新；
 * - 携带操作 ID 的 dispatch 幂等，重试不会重复应用；
 * - 已提交操作进入日志，可回放重建状态；
 * - merged / unmerged 两种执行策略在结果、错误与副作用次数上一致。
 */

export type Mutator<S> = (draft: S) => void;

export interface OpOptions {
  /** 幂等键：相同 ID 的操作只会被应用一次。 */
  readonly id?: string;
  readonly label?: string;
}

export interface Op<S> extends OpOptions {
  readonly mutate: Mutator<S>;
}

export type Invariant<S> = (state: S) => void;
export type Listener<S> = (state: S) => void;

/** 直接赋值操作。 */
export function set<S, K extends keyof S>(key: K, value: S[K], options?: OpOptions): Op<S> {
  return {
    ...options,
    label: options?.label ?? `set(${String(key)})`,
    mutate(draft) {
      draft[key] = value;
    },
  };
}

/** 函数式更新：按顺序读取草稿中的前一值。 */
export function update<S, K extends keyof S>(
  key: K,
  fn: (prev: S[K]) => S[K],
  options?: OpOptions,
): Op<S> {
  return {
    ...options,
    label: options?.label ?? `update(${String(key)})`,
    mutate(draft) {
      draft[key] = fn(draft[key]);
    },
  };
}

/** 任意事务操作：条件更新、跨字段不变量相关写入、读后再写等。 */
export function tx<S>(mutate: Mutator<S>, options?: OpOptions): Op<S> {
  return { ...options, mutate };
}

export type CommitMode = 'merged' | 'unmerged';

export class Store<S> {
  #state: S;
  readonly #initial: S;
  readonly #mode: CommitMode;
  #pending: Op<S>[] = [];
  #pendingIds = new Set<string>();
  #appliedIds = new Set<string>();
  #log: Op<S>[] = [];
  #listeners = new Set<Listener<S>>();
  #invariants: Invariant<S>[] = [];
  #batchDepth = 0;

  constructor(initial: S, options?: { mode?: CommitMode }) {
    this.#initial = structuredClone(initial);
    this.#state = structuredClone(initial);
    this.#mode = options?.mode ?? 'merged';
  }

  get mode(): CommitMode {
    return this.#mode;
  }

  /** 只返回已提交快照；批次进行中读到的仍是提交前的值。 */
  getState(): S {
    return this.#state;
  }

  /** 已提交操作日志，可用于回放。 */
  getLog(): readonly Op<S>[] {
    return this.#log;
  }

  subscribe(listener: Listener<S>): () => void {
    this.#listeners.add(listener);
    return () => {
      this.#listeners.delete(listener);
    };
  }

  /** 提交点不变量：跨字段约束在草稿求值完成后、提交前检查。 */
  addInvariant(invariant: Invariant<S>): void {
    this.#invariants.push(invariant);
  }

  /**
   * 入队一个操作。批次外交互时立即作为单操作事务提交。
   * 返回 false 表示该操作因 ID 幂等被跳过（重试安全）。
   */
  dispatch(op: Op<S>): boolean {
    if (op.id !== undefined && (this.#appliedIds.has(op.id) || this.#pendingIds.has(op.id))) {
      return false;
    }
    if (this.#batchDepth > 0) {
      this.#pending.push(op);
      if (op.id !== undefined) this.#pendingIds.add(op.id);
      return true;
    }
    this.#commitAll([op]);
    return true;
  }

  /**
   * 取消尚未提交的排队操作；已提交的操作不受影响，
   * 因此取消不会撤销后续独立更新。返回移除的操作数。
   */
  cancel(id: string): number {
    const before = this.#pending.length;
    this.#pending = this.#pending.filter((op) => op.id !== id);
    const removed = before - this.#pending.length;
    if (removed > 0) this.#pendingIds.delete(id);
    return removed;
  }

  /** 批次：嵌套调用合并到最外层，只在最外层提交并通知一次。 */
  batch<T>(fn: () => T): T {
    this.#batchDepth++;
    try {
      const result = fn();
      this.#batchDepth--;
      if (this.#batchDepth === 0) this.#flush();
      return result;
    } catch (err) {
      this.#batchDepth--;
      if (this.#batchDepth === 0) this.#discardPending();
      throw err;
    }
  }

  /** 回放：从初始状态重放已提交日志，重建状态。 */
  rebuild(): S {
    const working = structuredClone(this.#initial);
    for (const op of this.#log) op.mutate(working);
    return working;
  }

  #flush(): void {
    const ops = this.#pending;
    this.#pending = [];
    this.#pendingIds.clear();
    if (ops.length === 0) return;
    this.#commitAll(ops);
  }

  #discardPending(): void {
    this.#pending = [];
    this.#pendingIds.clear();
  }

  /**
   * 在事务草稿上按序求值全部操作，校验不变量后一次性原子提交。
   * 任何一步抛错都不会触碰已提交状态（失败批次不留部分值）。
   */
  #commitAll(ops: Op<S>[]): void {
    let working: S;
    if (this.#mode === 'merged') {
      // 合并优化：整批共享一份草稿。
      working = structuredClone(this.#state);
      for (const op of ops) op.mutate(working);
    } else {
      // 未合并参考执行：每个操作各自求值一次，等价于逐次提交。
      working = this.#state;
      for (const op of ops) {
        const draft = structuredClone(working);
        op.mutate(draft);
        working = draft;
      }
    }
    for (const invariant of this.#invariants) invariant(working);

    // 提交点：单次原子交换，读者看不到中间态。
    this.#state = working;
    for (const op of ops) {
      this.#log.push(op);
      if (op.id !== undefined) this.#appliedIds.add(op.id);
    }
    this.#notify();
  }

  #notify(): void {
    for (const listener of this.#listeners) listener(this.#state);
  }
}
