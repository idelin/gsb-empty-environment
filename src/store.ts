import { FixpointEngine, type SettleResult } from './engine.ts';

/** 可序列化的状态操作，构成可回放日志。 */
export interface Op {
  readonly type: 'set';
  readonly cell: string;
  readonly value: unknown;
}

/** 一条已提交事务的日志记录。 */
export interface LogEntry {
  readonly tx: number;
  readonly ops: readonly Op[];
}

/**
 * 事务：写操作先进私有缓冲，读者完全不可见；
 * commit 时一次性应用、一次 settle、一次原子换快照。
 */
export class Transaction {
  private readonly ops: Op[] = [];
  private closed = false;
  private readonly store: Store;

  constructor(store: Store) {
    this.store = store;
  }

  set(cell: string, value: unknown): this {
    if (this.closed) throw new Error('transaction already closed');
    this.ops.push({ type: 'set', cell, value });
    return this;
  }

  commit(): SettleResult {
    if (this.closed) throw new Error('transaction already closed');
    this.closed = true;
    return this.store.commit(this.ops);
  }
}

/**
 * 带版本与操作日志的存储层。
 *
 * - 读者只读最近一次提交并收敛后的快照，永远看不到事务中间态；
 * - 每次提交追加一条日志，replay() 可在全新引擎上重放得到相同状态。
 */
export class Store {
  private readonly engine: FixpointEngine;
  private readonly engineFactory: () => FixpointEngine;
  private committed: ReadonlyMap<string, unknown>;
  private txCount = 0;
  readonly log: LogEntry[] = [];

  constructor(engine: FixpointEngine, engineFactory: () => FixpointEngine) {
    this.engine = engine;
    this.engineFactory = engineFactory;
    this.committed = engine.settle().values;
  }

  /** 已提交事务数，即版本号。 */
  get version(): number {
    return this.txCount;
  }

  /** 只读已提交快照；事务缓冲中的写不可见。 */
  read(name: string): unknown {
    return this.committed.get(name);
  }

  snapshot(): ReadonlyMap<string, unknown> {
    return this.committed;
  }

  begin(): Transaction {
    return new Transaction(this);
  }

  /** 原子提交：应用全部写 -> 一次 settle -> 版本 +1 -> 整体换快照。 */
  commit(ops: readonly Op[]): SettleResult {
    for (const op of ops) this.engine.setInput(op.cell, op.value);
    const result = this.engine.settle();
    this.txCount++;
    this.log.push({ tx: this.txCount, ops: [...ops] });
    this.committed = result.values;
    return result;
  }

  /** 在全新引擎上按日志逐事务重放，返回重放后的状态。 */
  replay(): ReadonlyMap<string, unknown> {
    const fresh = this.engineFactory();
    let state = fresh.settle().values;
    for (const entry of this.log) {
      for (const op of entry.ops) fresh.setInput(op.cell, op.value);
      state = fresh.settle().values;
    }
    return state;
  }
}
