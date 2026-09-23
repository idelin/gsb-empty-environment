/**
 * 事务性状态存储。
 *
 * - 所有变更以 Op 形式通过 transact() 批量提交，一次提交产生一个不可变快照；
 *   读者只能拿到完整快照，永远看不到事务中间态。
 * - 每个 Op 追加进操作日志 log，日志为纯数据（可 JSON 序列化），
 *   通过 Store.replay() 可以在新 Store 上完整重放，得到相同状态。
 */

export interface Op {
  readonly key: string;
  readonly value: unknown;
}

export type Snapshot = Readonly<Record<string, unknown>>;

export type Listener = (snapshot: Snapshot, changedKeys: ReadonlySet<string>) => void;

export class Store {
  #state: Record<string, unknown>;
  #log: Op[] = [];
  #listeners = new Set<Listener>();
  #version = 0;

  constructor(initial: Record<string, unknown> = {}) {
    this.#state = Object.freeze({ ...initial });
  }

  get version(): number {
    return this.#version;
  }

  /** 当前不可变快照。 */
  snapshot(): Snapshot {
    return this.#state;
  }

  /** 已提交的操作日志（只读）。 */
  get log(): readonly Op[] {
    return this.#log;
  }

  /**
   * 原子地提交一批操作：先生成完整新快照，再一次性通知订阅者。
   * 订阅者回调里看到的永远是提交后的完整状态。
   */
  transact(ops: readonly Op[]): void {
    if (ops.length === 0) return;
    const next: Record<string, unknown> = { ...this.#state };
    const changed = new Set<string>();
    for (const op of ops) {
      next[op.key] = op.value;
      changed.add(op.key);
    }
    this.#state = Object.freeze(next);
    for (const op of ops) {
      this.#log.push(Object.freeze({ key: op.key, value: op.value }));
    }
    this.#version += 1;
    for (const listener of [...this.#listeners]) {
      listener(this.#state, changed);
    }
  }

  subscribe(listener: Listener): () => void {
    this.#listeners.add(listener);
    return () => {
      this.#listeners.delete(listener);
    };
  }

  /** 在全新 Store 上按序重放操作日志，重建相同状态。 */
  static replay(ops: readonly Op[]): Store {
    const store = new Store();
    for (const op of ops) {
      store.transact([op]);
    }
    return store;
  }
}
