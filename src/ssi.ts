/**
 * Serializable Snapshot Isolation (SSI) 防异常器
 *
 * 基于 Cahill et al. "Serializable Snapshot Isolation" 的思想：
 *  - MVCC 快照隔离（Snapshot Isolation）+ 先提交者获胜（first-committer-wins）
 *  - SIREAD 锁：读操作在元组上留下 SIREAD 标记，写操作检查并建立
 *    rw-antidependency（读写反依赖）边
 *  - 危险结构（dangerous structure）：T_in -rw-> T_pivot -rw-> T_out，
 *    且 T_out 在三者中最先提交。检测到即将成环时，精准中止破坏
 *    可串行化的事务，其余事务不受影响。
 */

export type IsolationMode = 'si' | 'ssi';

export type TxnStatus = 'active' | 'committed' | 'aborted';

export class AbortError extends Error {
  readonly reason: 'write-skew-danger' | 'write-write-conflict';
  constructor(
    message: string,
    reason: 'write-skew-danger' | 'write-write-conflict',
  ) {
    super(message);
    this.name = 'AbortError';
    this.reason = reason;
  }
}

interface Version {
  value: number;
  createdBy: number; // 创建该版本的事务 id（0 表示初始数据）
  beginTs: number; // 创建者的提交时间戳
  endTs: number; // 被覆盖时的提交时间戳，Infinity 表示当前最新版本
}

class TxnState {
  readonly rwIn = new Set<TxnState>(); // 指向本事务的 rw 反依赖边（reader ->rw this）
  readonly rwOut = new Set<TxnState>(); // 本事务指出的 rw 反依赖边（this ->rw writer）
  commitTs = -1;
  status: TxnStatus = 'active';
  readonly id: number;
  readonly snapshotTs: number;
  constructor(id: number, snapshotTs: number) {
    this.id = id;
    this.snapshotTs = snapshotTs;
  }
}

export class SsiDatabase {
  private clock = 0;
  private nextTxnId = 1;
  private readonly store = new Map<string, Version[]>();
  private readonly sireadLocks = new Map<string, Set<TxnState>>();
  private readonly txns = new Map<number, TxnState>();

  private readonly mode: IsolationMode;
  constructor(mode: IsolationMode = 'ssi') {
    this.mode = mode;
  }

  /** 写入初始数据（由“创世事务”在 ts=0 提交）。 */
  seed(key: string, value: number): void {
    this.store.set(key, [{ value, createdBy: 0, beginTs: 0, endTs: Infinity }]);
  }

  /** 当前已提交的最新值（供外部校验约束使用）。 */
  latest(key: string): number {
    const versions = this.store.get(key);
    if (!versions) throw new Error(`key not found: ${key}`);
    return versions[versions.length - 1].value;
  }

  begin(): Transaction {
    const state = new TxnState(this.nextTxnId++, this.clock);
    this.txns.set(state.id, state);
    return new Transaction(this, state);
  }

  /** 以下方法仅供 Transaction 调用。 */

  _read(txn: TxnState, key: string): number {
    const versions = this.store.get(key);
    if (!versions) throw new Error(`key not found: ${key}`);

    const latest = versions[versions.length - 1];
    const visible =
      [...versions]
        .reverse()
        .find((v) => v.beginTs <= txn.snapshotTs && v.endTs > txn.snapshotTs) ??
      versions[0];

    if (this.mode === 'ssi') {
      // 留下 SIREAD 锁（事务提交后仍保留，供并发写事务检测）。
      let holders = this.sireadLocks.get(key);
      if (!holders) this.sireadLocks.set(key, (holders = new Set()));
      holders.add(txn);

      // 若最新版本对本快照不可见（由并发事务写入），则存在
      // 本事务 ->rw 写者 的反依赖边。
      if (latest.createdBy !== txn.id && latest.createdBy !== 0) {
        const writer = this.txns.get(latest.createdBy);
        if (
          writer &&
          writer.status !== 'aborted' &&
          (writer.status === 'active' || writer.commitTs > txn.snapshotTs)
        ) {
          this.addRwEdge(txn, writer);
          this.checkPivotOrAbort(writer, txn);
        }
      }
    }
    return visible.value;
  }

  _write(txn: TxnState, key: string, value: number): void {
    const versions = this.store.get(key);
    if (!versions) throw new Error(`key not found: ${key}`);
    const latest = versions[versions.length - 1];

    // SI 写-写冲突：先提交者获胜。
    if (latest.createdBy !== txn.id && latest.createdBy !== 0) {
      const writer = this.txns.get(latest.createdBy);
      if (
        writer &&
        writer.status !== 'aborted' &&
        (writer.status === 'active' || writer.commitTs > txn.snapshotTs)
      ) {
        throw new AbortError(
          `T${txn.id} aborted: write-write conflict on "${key}" with T${writer.id}`,
          'write-write-conflict',
        );
      }
    }

    if (this.mode === 'ssi') {
      // 写操作撞上他人的 SIREAD 锁：建立 reader ->rw 本事务 的反依赖边。
      const holders = this.sireadLocks.get(key);
      if (holders) {
        for (const reader of [...holders]) {
          if (reader === txn || reader.status === 'aborted') continue;
          // 仅当读者与本事务并发（读者看到的不是本事务将产生的版本）。
          if (reader.status === 'committed' && reader.commitTs <= txn.snapshotTs)
            continue;
          this.addRwEdge(reader, txn);
          // 读者可能成为危险结构的枢纽（pivot）。
          this.checkPivotOrAbort(reader, txn);
        }
      }
    }
  }

  _commit(txn: TxnState, writeSet: Map<string, number>): void {
    if (this.mode === 'ssi' && this.isDangerous(txn)) {
      throw new AbortError(
        `T${txn.id} aborted: dangerous rw-antidependency structure (write skew) detected at commit`,
        'write-skew-danger',
      );
    }
    txn.commitTs = ++this.clock;
    for (const [key, value] of writeSet) {
      const versions = this.store.get(key)!;
      versions[versions.length - 1].endTs = txn.commitTs;
      versions.push({ value, createdBy: txn.id, beginTs: txn.commitTs, endTs: Infinity });
    }
    txn.status = 'committed';
  }

  _abort(txn: TxnState): void {
    txn.status = 'aborted';
    txn.rwIn.clear();
    txn.rwOut.clear();
    for (const holders of this.sireadLocks.values()) holders.delete(txn);
  }

  private addRwEdge(from: TxnState, to: TxnState): void {
    from.rwOut.add(to);
    to.rwIn.add(from);
  }

  /**
   * 危险结构判定：pivot 同时具有 rw 入边与 rw 出边，且存在
   * 已提交的出边目标 T_out，使得入边来源 T_in 不早于 T_out 提交
   * （即 T_out 在三者中最先提交）。
   */
  private isDangerous(pivot: TxnState): boolean {
    if (pivot.rwIn.size === 0 || pivot.rwOut.size === 0) return false;
    for (const out of pivot.rwOut) {
      if (out.status !== 'committed') continue;
      for (const inn of pivot.rwIn) {
        if (inn.status === 'aborted') continue;
        if (inn.status === 'active' || inn.commitTs >= out.commitTs) return true;
      }
    }
    return false;
  }

  /**
   * 在建立新边后检查 pivot 是否构成危险结构：
   *  - pivot 仍活跃：精准中止 pivot，当前事务继续执行；
   *  - pivot 已提交（无法回滚）：抛出 AbortError，由当前事务自行中止。
   */
  private checkPivotOrAbort(pivot: TxnState, current: TxnState): void {
    if (!this.isDangerous(pivot)) return;
    if (pivot.status === 'active' && pivot !== current) {
      this._abort(pivot);
      return;
    }
    throw new AbortError(
      `T${current.id} aborted: dangerous rw-antidependency structure ` +
        `(pivot T${pivot.id}) detected`,
      'write-skew-danger',
    );
  }
}

export class Transaction {
  private readonly writeSet = new Map<string, number>();
  private finished = false;

  private readonly db: SsiDatabase;
  private readonly state: TxnState;
  constructor(db: SsiDatabase, state: TxnState) {
    this.db = db;
    this.state = state;
  }

  get id(): number {
    return this.state.id;
  }

  get status(): TxnStatus {
    return this.state.status;
  }

  read(key: string): number {
    this.assertActive();
    if (this.writeSet.has(key)) return this.writeSet.get(key)!;
    try {
      return this.db._read(this.state, key);
    } catch (err) {
      if (err instanceof AbortError) this.abort();
      throw err;
    }
  }

  write(key: string, value: number): void {
    this.assertActive();
    try {
      this.db._write(this.state, key, value);
    } catch (err) {
      if (err instanceof AbortError) this.abort();
      throw err;
    }
    this.writeSet.set(key, value);
  }

  commit(): void {
    this.assertActive();
    try {
      this.db._commit(this.state, this.writeSet);
    } catch (err) {
      if (err instanceof AbortError) this.abort();
      this.finished = true;
      throw err;
    }
    this.finished = true;
  }

  abort(): void {
    if (this.finished) return;
    this.db._abort(this.state);
    this.finished = true;
  }

  private assertActive(): void {
    if (this.finished || this.state.status !== 'active') {
      throw new Error(`T${this.id} is not active (status=${this.state.status})`);
    }
  }
}
