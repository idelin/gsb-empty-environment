/**
 * 事务性存储引擎：快照隔离读 + 提交阶段串行化校验。
 *
 * 两条防线：
 *  1. 写倾斜检测（并发检测器）：事务读集记录版本号，提交时若发现
 *     读过的行已被其它事务并发修改，则中止（检测交叉修改）。
 *  2. 声明式不变量重验（业务不变量保护）：提交前在“当前已提交状态 +
 *     本事务写集”的叠加视图上重跑所有相关谓词，违反则安全回滚。
 *
 * 所有提交经互斥锁串行化，因此先提交者生效、后提交者被精准中止。
 */

export type Row = Record<string, unknown>;
export type RowId = string;

export interface ReadView {
  get(table: string, id: RowId): Row | undefined;
  scan(table: string, predicate?: (row: Row, id: RowId) => boolean): Row[];
  count(table: string, predicate?: (row: Row, id: RowId) => boolean): number;
}

export interface Invariant {
  /** 不变量名称，违例时出现在错误信息中 */
  readonly name: string;
  /** 涉及的表：仅当事务写集触及这些表时才重验（精准、避免无谓校验） */
  readonly tables: readonly string[];
  /** 谓词：在叠加视图上求值，返回 false 表示不变量被破坏 */
  readonly assert: (view: ReadView) => boolean;
  /** 可选的人类可读描述 */
  readonly description?: string;
}

export class InvariantViolationError extends Error {
  readonly invariantName: string;
  constructor(invariant: Invariant) {
    super(
      `事务已中止：违反业务不变量 "${invariant.name}"` +
        (invariant.description ? `（${invariant.description}）` : ''),
    );
    this.name = 'InvariantViolationError';
    this.invariantName = invariant.name;
  }
}

export class WriteSkewError extends Error {
  readonly conflicts: string[];
  constructor(conflicts: string[]) {
    super(`事务已中止：检测到写倾斜，读集被并发修改 -> ${conflicts.join(', ')}`);
    this.name = 'WriteSkewError';
    this.conflicts = conflicts;
  }
}

interface TableState {
  rows: Map<RowId, Row>;
  /** 每行版本号，随提交单调递增 */
  versions: Map<RowId, number>;
}

interface WriteOp {
  before: Row | undefined;
  after: Row | undefined; // undefined 表示删除
}

const keyOf = (table: string, id: RowId): string => `${table}:${id}`;

/** 提交阶段的叠加视图：已提交状态 + 本事务写集，用于重验谓词 */
class OverlayView implements ReadView {
  private readonly store: TransactionalStore;
  private readonly writeSet: Map<string, WriteOp>;

  constructor(store: TransactionalStore, writeSet: Map<string, WriteOp>) {
    this.store = store;
    this.writeSet = writeSet;
  }

  get(table: string, id: RowId): Row | undefined {
    const op = this.writeSet.get(keyOf(table, id));
    if (op) return op.after;
    return this.store.committedGet(table, id);
  }

  scan(table: string, predicate?: (row: Row, id: RowId) => boolean): Row[] {
    const ids = new Set<RowId>(this.store.committedIds(table));
    for (const [key, op] of this.writeSet) {
      const [t, id] = key.split(':');
      if (t !== table) continue;
      if (op.after) ids.add(id);
      else ids.delete(id);
    }
    const out: Row[] = [];
    for (const id of ids) {
      const row = this.get(table, id);
      if (row && (!predicate || predicate(row, id))) out.push(row);
    }
    return out;
  }

  count(table: string, predicate?: (row: Row, id: RowId) => boolean): number {
    return this.scan(table, predicate).length;
  }
}

export class Transaction implements ReadView {
  private readonly store: TransactionalStore;
  private readonly readVersions = new Map<string, number>();
  private readonly writeSet = new Map<string, WriteOp>();
  private finished = false;

  constructor(store: TransactionalStore) {
    this.store = store;
  }

  get(table: string, id: RowId): Row | undefined {
    this.assertActive();
    const op = this.writeSet.get(keyOf(table, id));
    if (op) return op.after;
    const row = this.store.committedGet(table, id);
    this.readVersions.set(keyOf(table, id), this.store.versionOf(table, id));
    return row;
  }

  scan(table: string, predicate?: (row: Row, id: RowId) => boolean): Row[] {
    this.assertActive();
    const out: Row[] = [];
    for (const id of this.store.committedIds(table)) {
      const row = this.get(table, id);
      if (row && (!predicate || predicate(row, id))) out.push(row);
    }
    // 读己之写：本事务新增的行
    for (const [key, op] of this.writeSet) {
      const [t, id] = key.split(':');
      if (t === table && op.after && op.before === undefined) {
        if (!predicate || predicate(op.after, id)) out.push(op.after);
      }
    }
    return out;
  }

  count(table: string, predicate?: (row: Row, id: RowId) => boolean): number {
    return this.scan(table, predicate).length;
  }

  put(table: string, id: RowId, row: Row): void {
    this.assertActive();
    const key = keyOf(table, id);
    const existing = this.writeSet.get(key);
    const before = existing ? existing.before : this.store.committedGet(table, id);
    this.writeSet.set(key, { before, after: { ...row } });
  }

  delete(table: string, id: RowId): void {
    this.assertActive();
    const key = keyOf(table, id);
    const existing = this.writeSet.get(key);
    const before = existing ? existing.before : this.store.committedGet(table, id);
    this.writeSet.set(key, { before, after: undefined });
  }

  /** 提交：进入串行化提交管线，可能因写倾斜或不变量违例被中止 */
  async commit(): Promise<void> {
    this.assertActive();
    await this.store.commit(this);
    this.finished = true;
  }

  rollback(): void {
    this.finished = true;
  }

  /** 以下供引擎内部使用 */
  get reads(): ReadonlyMap<string, number> {
    return this.readVersions;
  }
  get writes(): ReadonlyMap<string, WriteOp> {
    return this.writeSet;
  }
  /** 中止时也标记结束，避免句柄复用 */
  markFinished(): void {
    this.finished = true;
  }

  private assertActive(): void {
    if (this.finished) throw new Error('事务已结束，不能继续操作');
  }
}

export class TransactionalStore {
  private readonly tables = new Map<string, TableState>();
  private readonly invariants: Invariant[] = [];
  private commitChain: Promise<void> = Promise.resolve();

  /** 声明式注册业务不变量 */
  defineInvariant(invariant: Invariant): void {
    this.invariants.push(invariant);
  }

  begin(): Transaction {
    return new Transaction(this);
  }

  /** 便捷入口：单事务执行并提交 */
  async transact(fn: (tx: Transaction) => void | Promise<void>): Promise<void> {
    const tx = this.begin();
    await fn(tx);
    await tx.commit();
  }

  committedGet(table: string, id: RowId): Row | undefined {
    return this.tables.get(table)?.rows.get(id);
  }

  committedIds(table: string): Iterable<RowId> {
    return this.tables.get(table)?.rows.keys() ?? [];
  }

  versionOf(table: string, id: RowId): number {
    return this.tables.get(table)?.versions.get(id) ?? 0;
  }

  /** 串行化提交管线：互斥执行，保证重验期间无其它提交插入 */
  commit(tx: Transaction): Promise<void> {
    const result = this.commitChain.then(() => this.commitLocked(tx));
    // 吞掉链上错误，保证后续提交仍可进入管线
    this.commitChain = result.then(
      () => undefined,
      () => undefined,
    );
    return result;
  }

  private commitLocked(tx: Transaction): void {
    // 防线 1：写倾斜检测 —— 读集版本校验
    const conflicts: string[] = [];
    for (const [key, version] of tx.reads) {
      const [table, id] = key.split(':');
      // 本事务自己写的行不算冲突
      if (tx.writes.has(key)) continue;
      if (this.versionOf(table, id) !== version) conflicts.push(key);
    }
    if (conflicts.length > 0) {
      tx.markFinished();
      throw new WriteSkewError(conflicts);
    }

    // 防线 2：在叠加视图上重验被触及表相关的全部不变量
    const touchedTables = new Set<string>();
    for (const key of tx.writes.keys()) touchedTables.add(key.split(':')[0]);
    const view = new OverlayView(this, new Map(tx.writes));
    for (const invariant of this.invariants) {
      if (!invariant.tables.some((t) => touchedTables.has(t))) continue;
      if (!invariant.assert(view)) {
        tx.markFinished();
        throw new InvariantViolationError(invariant);
      }
    }

    // 校验通过：应用写集并递增版本
    for (const [key, op] of tx.writes) {
      const [table, id] = key.split(':');
      let state = this.tables.get(table);
      if (!state) {
        state = { rows: new Map(), versions: new Map() };
        this.tables.set(table, state);
      }
      const nextVersion = (state.versions.get(id) ?? 0) + 1;
      if (op.after) state.rows.set(id, { ...op.after });
      else state.rows.delete(id);
      state.versions.set(id, nextVersion);
    }
  }
}
