/**
 * MVCC 快照隔离存储 + 声明式约束注册表 + 提交期重验（写倾斜检测）。
 *
 * 核心机制：
 *  - 每行数据保留版本链，事务在 begin 时获得全局版本号作为一致性快照，
 *    读路径只能看到 version <= snapshotVersion 的已提交数据（快照隔离）；
 *  - 事务写入先进入本地写缓冲（read-your-writes），提交时才落盘；
 *  - 提交被串行化（commit 临界区），在临界区内依次执行：
 *      1. 写-写冲突检测（first-committer-wins）；
 *      2. 写倾斜检测：若某约束的依赖表在本事务快照之后被并发事务修改，
 *         或本事务自身写入了该表，则在「最新已提交状态 + 本事务写入」
 *         的合并视图上重验该约束谓词；谓词不满足则中止并安全回滚，
 *         从而精准拦截并发交叉修改导致的业务不变量破坏。
 */

export type Row = Record<string, unknown>;

/** 约束求值时使用的只读视图 */
export interface ReadView {
  get(table: string, key: string): Row | undefined;
  scan(table: string): Row[];
  count(table: string, filter?: (row: Row) => boolean): number;
}

/** 声明式约束：命名 + 依赖表集合 + 谓词 */
export interface Constraint {
  name: string;
  /** 谓词读取的表（依赖范围），用于精准判断提交期是否需要重验 */
  tables: string[];
  predicate: (view: ReadView) => boolean;
  description?: string;
}

export class ConstraintViolationError extends Error {
  readonly constraintName: string;
  constructor(constraintName: string, detail?: string) {
    super(
      `约束 "${constraintName}" 在提交期重验失败，事务已中止回滚` +
        (detail ? `：${detail}` : ""),
    );
    this.name = "ConstraintViolationError";
    this.constraintName = constraintName;
  }
}

export class WriteConflictError extends Error {
  constructor(key: string) {
    super(`写-写冲突：键 "${key}" 在事务快照之后已被其他事务修改`);
    this.name = "WriteConflictError";
  }
}

interface VersionedRow {
  version: number;
  row: Row | null; // null 表示该版本删除了此行
}

type TableData = Map<string, VersionedRow[]>;

/** 基于「已提交版本链 + 可选写缓冲覆盖层」的只读视图 */
class StateView implements ReadView {
  private readonly data: Map<string, TableData>;
  private readonly asOfVersion: number;
  private readonly overlay?: Map<string, Row | null>;

  constructor(
    data: Map<string, TableData>,
    asOfVersion: number,
    overlay?: Map<string, Row | null>,
  ) {
    this.data = data;
    this.asOfVersion = asOfVersion;
    this.overlay = overlay;
  }

  get(table: string, key: string): Row | undefined {
    const overlayKey = `${table}/${key}`;
    if (this.overlay?.has(overlayKey)) {
      return this.overlay.get(overlayKey) ?? undefined;
    }
    const chain = this.data.get(table)?.get(key);
    if (!chain) return undefined;
    for (let i = chain.length - 1; i >= 0; i--) {
      if (chain[i].version <= this.asOfVersion) {
        return chain[i].row ?? undefined;
      }
    }
    return undefined;
  }

  scan(table: string): Row[] {
    const rows: Row[] = [];
    const seen = new Set<string>();
    if (this.overlay) {
      for (const [overlayKey, row] of this.overlay) {
        const slash = overlayKey.indexOf("/");
        if (overlayKey.slice(0, slash) !== table) continue;
        const key = overlayKey.slice(slash + 1);
        seen.add(key);
        if (row !== null) rows.push(row);
      }
    }
    for (const key of this.data.get(table)?.keys() ?? []) {
      if (seen.has(key)) continue;
      const row = this.get(table, key);
      if (row !== undefined) rows.push(row);
    }
    return rows;
  }

  count(table: string, filter?: (row: Row) => boolean): number {
    const rows = this.scan(table);
    return filter ? rows.filter(filter).length : rows.length;
  }
}

export class Database {
  private data = new Map<string, TableData>();
  private version = 0;
  /** 表 -> 最后被修改时的全局版本号 */
  private tableVersions = new Map<string, number>();
  /** "table/key" -> 最后被修改时的全局版本号 */
  private keyVersions = new Map<string, number>();
  private constraints: Constraint[] = [];
  /** 提交临界区串行化链 */
  private commitChain: Promise<unknown> = Promise.resolve();

  /** 注册声明式约束（业务不变量） */
  addConstraint(constraint: Constraint): void {
    this.constraints.push(constraint);
  }

  /** 在最新已提交状态上立即校验全部约束（用于启动自检/演示） */
  checkConstraints(): void {
    const view = new StateView(this.data, this.version);
    for (const c of this.constraints) {
      if (!c.predicate(view)) {
        throw new ConstraintViolationError(c.name, c.description);
      }
    }
  }

  begin(): Transaction {
    return new Transaction(this, this.version);
  }

  /** 供 Transaction 调用：进入串行化提交临界区 */
  async commitTransaction(tx: Transaction): Promise<void> {
    const commit = this.commitChain.then(() => this.commitCriticalSection(tx));
    // 吞掉链上错误，保证后续提交不被前序失败阻塞
    this.commitChain = commit.catch(() => {});
    return commit;
  }

  private commitCriticalSection(tx: Transaction): void {
    const writeSet = tx.internalWriteSet;

    // 1. 写-写冲突检测（first-committer-wins）
    for (const overlayKey of writeSet.keys()) {
      const keyVersion = this.keyVersions.get(overlayKey) ?? 0;
      if (keyVersion > tx.snapshotVersion) {
        throw new WriteConflictError(overlayKey);
      }
    }

    // 2. 写倾斜检测：只重验「依赖范围可能被改变」的约束
    const writtenTables = new Set<string>();
    for (const overlayKey of writeSet.keys()) {
      writtenTables.add(overlayKey.slice(0, overlayKey.indexOf("/")));
    }
    // 合并视图 = 最新已提交状态 + 本事务待提交写入
    const mergedView = new StateView(this.data, this.version, writeSet);
    for (const c of this.constraints) {
      const affectedByOwnWrites = c.tables.some((t) => writtenTables.has(t));
      const affectedByConcurrentCommits = c.tables.some(
        (t) => (this.tableVersions.get(t) ?? 0) > tx.snapshotVersion,
      );
      // 依赖数据自快照以来未变且本事务也未触碰：快照期结论仍然有效，跳过重验
      if (!affectedByOwnWrites && !affectedByConcurrentCommits) continue;
      if (!c.predicate(mergedView)) {
        // 中止：不应用任何写入，事务安全回滚
        throw new ConstraintViolationError(c.name, c.description);
      }
    }

    // 3. 应用写入并推进版本
    this.version += 1;
    for (const [overlayKey, row] of writeSet) {
      const slash = overlayKey.indexOf("/");
      const table = overlayKey.slice(0, slash);
      const key = overlayKey.slice(slash + 1);
      let tableData = this.data.get(table);
      if (!tableData) {
        tableData = new Map();
        this.data.set(table, tableData);
      }
      let chain = tableData.get(key);
      if (!chain) {
        chain = [];
        tableData.set(key, chain);
      }
      chain.push({ version: this.version, row });
      this.keyVersions.set(overlayKey, this.version);
      this.tableVersions.set(table, this.version);
    }
  }

  /** 最新已提交状态的只读视图，供演示/测试断言使用 */
  committedView(): ReadView {
    return new StateView(this.data, this.version);
  }
}

export class Transaction {
  /** "table/key" -> 待写入行（null 表示删除） */
  readonly internalWriteSet = new Map<string, Row | null>();
  private finished = false;

  private readonly db: Database;
  readonly snapshotVersion: number;

  constructor(db: Database, snapshotVersion: number) {
    this.db = db;
    this.snapshotVersion = snapshotVersion;
  }

  private view(): ReadView {
    // 快照读：只能看到 snapshotVersion 之前已提交的数据 + 自己的写缓冲
    return new StateView(
      (this.db as unknown as { data: Map<string, TableData> }).data,
      this.snapshotVersion,
      this.internalWriteSet,
    );
  }

  get(table: string, key: string): Row | undefined {
    this.assertActive();
    return this.view().get(table, key);
  }

  scan(table: string): Row[] {
    this.assertActive();
    return this.view().scan(table);
  }

  count(table: string, filter?: (row: Row) => boolean): number {
    this.assertActive();
    return this.view().count(table, filter);
  }

  put(table: string, key: string, row: Row): void {
    this.assertActive();
    this.internalWriteSet.set(`${table}/${key}`, row);
  }

  delete(table: string, key: string): void {
    this.assertActive();
    this.internalWriteSet.set(`${table}/${key}`, null);
  }

  async commit(): Promise<void> {
    this.assertActive();
    this.finished = true;
    await this.db.commitTransaction(this);
  }

  /** 显式回滚（丢弃写缓冲） */
  rollback(): void {
    this.assertActive();
    this.finished = true;
    this.internalWriteSet.clear();
  }

  private assertActive(): void {
    if (this.finished) throw new Error("事务已结束（提交或回滚后不可再操作）");
  }
}
