/**
 * Serializable Snapshot Isolation (SSI)
 * -------------------------------------
 * 在快照隔离（SI/MVCC）之上，通过 SIREAD 锁追踪 rw-反依赖（rw-antidependency），
 * 检测"危险结构" Tn -rw-> Tp -rw-> Tm（其中 Tm 最先提交，Tn 可与 Tm 相同）。
 * 根据 Fekete/Cahill 的 SSI 理论：SI 下任何串行化异常环都必然包含该危险结构，
 * 因此在成环前夕中止枢轴事务即可保证完全可串行化。
 */

export class SerializationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "SerializationError";
  }
}

export type TxnState = "active" | "committed" | "aborted";

interface Version {
  value: number;
  commitTs: number;
  writer: Transaction | null;
}

export interface DatabaseOptions {
  /** 默认 true；置 false 时退化为纯快照隔离（用于对照演示写倾斜异常）。 */
  ssi?: boolean;
}

export class Database {
  private readonly ssiEnabled: boolean;
  private readonly versions = new Map<string, Version[]>();
  /** SIREAD 锁：key -> 读过该 key 的事务集合（提交后仍保留）。 */
  private readonly sireadLocks = new Map<string, Set<Transaction>>();
  private readonly pendingWrites = new Map<string, Set<Transaction>>();
  private commitCounter = 0;
  private nextTxnId = 1;

  constructor(options: DatabaseOptions = {}) {
    this.ssiEnabled = options.ssi ?? true;
  }

  get ssi(): boolean {
    return this.ssiEnabled;
  }

  /** 初始化数据（相当于系统事务在 ts=0 时写入）。 */
  seed(key: string, value: number): void {
    this.versions.set(key, [{ value, commitTs: 0, writer: null }]);
  }

  begin(): Transaction {
    return new Transaction(this, this.nextTxnId++, this.commitCounter);
  }

  /** 读取最新已提交值（测试/演示用）。 */
  committedValue(key: string): number | undefined {
    const versions = this.versions.get(key);
    if (!versions || versions.length === 0) return undefined;
    return versions[versions.length - 1].value;
  }

  /** @internal 由 Transaction.read 调用。 */
  snapshotRead(txn: Transaction, key: string): number | undefined {
    if (txn.hasWrite(key)) return txn.ownWrite(key);

    const versions = this.versions.get(key) ?? [];
    let visible: Version | null = null;
    for (const version of versions) {
      if (version.commitTs <= txn.snapshotTs) {
        if (!visible || version.commitTs > visible.commitTs) visible = version;
      }
    }

    if (this.ssiEnabled) {
      // 加 SIREAD 锁（事务提交后锁仍保留，直到并发事务结束）。
      let readers = this.sireadLocks.get(key);
      if (!readers) {
        readers = new Set();
        this.sireadLocks.set(key, readers);
      }
      readers.add(txn);

      // 读到的快照之外已存在更新的已提交版本 => reader -rw-> writer。
      for (const version of versions) {
        if (
          version.writer !== null &&
          version.writer !== txn &&
          version.writer.state !== "aborted" &&
          version.commitTs > txn.snapshotTs
        ) {
          this.addRwConflict(txn, version.writer);
        }
      }
      // 其他活跃事务对该 key 有未提交写 => reader -rw-> writer。
      const pending = this.pendingWrites.get(key);
      if (pending) {
        for (const writer of pending) {
          if (writer !== txn && writer.state === "active") {
            this.addRwConflict(txn, writer);
          }
        }
      }
    }
    return visible ? visible.value : undefined;
  }

  /** @internal 由 Transaction.write 调用。 */
  stageWrite(txn: Transaction, key: string, value: number): void {
    txn.putWrite(key, value);
    let pending = this.pendingWrites.get(key);
    if (!pending) {
      pending = new Set();
      this.pendingWrites.set(key, pending);
    }
    pending.add(txn);

    if (this.ssiEnabled) {
      // 该 key 上的 SIREAD 锁持有者读不到本次写 => reader -rw-> txn。
      const readers = this.sireadLocks.get(key);
      if (readers) {
        for (const reader of readers) {
          if (reader !== txn && reader.state !== "aborted") {
            this.addRwConflict(reader, txn);
          }
        }
      }
    }
  }

  /** @internal 由 Transaction.commit 调用。 */
  commitTransaction(txn: Transaction): void {
    // 写/写冲突：first-committer-wins。
    for (const key of txn.writeKeys()) {
      for (const version of this.versions.get(key) ?? []) {
        if (version.commitTs > txn.snapshotTs) {
          throw new SerializationError(
            `T${txn.id}: write/write conflict on "${key}" (first committer wins)`
          );
        }
      }
    }

    if (this.ssiEnabled) {
      // 危险结构检测点一：N -rw-> P -rw-> txn，且 txn 是三者中最先提交的。
      for (const pivot of txn.inConflicts) {
        if (pivot.state !== "active") continue; // pivot 已提交 => txn 非首个提交
        for (const first of pivot.inConflicts) {
          if (first.state !== "active") continue; // first 已提交/已中止 => 不构成危险
          pivot.abort(
            `SSI: dangerous structure T${first.id} -rw-> T${pivot.id} -rw-> T${txn.id} ` +
              `(T${txn.id} commits first); aborting pivot T${pivot.id}`
          );
          break;
        }
      }
    }

    txn.markCommitted(++this.commitCounter);
    for (const key of txn.writeKeys()) {
      let versions = this.versions.get(key);
      if (!versions) {
        versions = [];
        this.versions.set(key, versions);
      }
      versions.push({ value: txn.ownWrite(key)!, commitTs: txn.commitSeq, writer: txn });
      this.pendingWrites.get(key)?.delete(txn);
    }
  }

  /** @internal 事务中止时清理其未提交写。 */
  releasePendingWrites(txn: Transaction): void {
    for (const key of txn.writeKeys()) {
      this.pendingWrites.get(key)?.delete(txn);
    }
  }

  /** 记录一条 rw-反依赖边 reader -rw-> writer，并检测危险结构。 */
  private addRwConflict(reader: Transaction, writer: Transaction): void {
    if (reader === writer) return;
    if (reader.outConflicts.has(writer)) return;
    reader.outConflicts.add(writer);
    writer.inConflicts.add(reader);

    // 危险结构检测点二：reader -rw-> writer -rw-> X，且 X 已提交（最先提交）。
    for (const x of writer.outConflicts) {
      if (x.state !== "committed") continue;
      // X 必须是最先提交者；若 reader/writer 已先于 X 提交则不构成危险。
      if (writer.state === "committed" && writer.commitSeq < x.commitSeq) continue;
      if (reader.state === "committed" && reader.commitSeq < x.commitSeq) continue;
      if (writer.state === "active") {
        writer.abort(
          `SSI: dangerous structure T${reader.id} -rw-> T${writer.id} -rw-> T${x.id} ` +
            `(T${x.id} already committed); aborting pivot T${writer.id}`
        );
      } else if (reader.state === "active") {
        reader.abort(
          `SSI: dangerous structure T${reader.id} -rw-> T${writer.id} -rw-> T${x.id} ` +
            `(T${x.id} already committed); aborting T${reader.id}`
        );
      }
    }
  }
}

export class Transaction {
  readonly id: number;
  readonly snapshotTs: number;
  state: TxnState = "active";
  commitSeq = 0;
  abortReason: string | null = null;
  /** X -rw-> this（别人读旧值、本事务写了新值）。 */
  readonly inConflicts = new Set<Transaction>();
  /** this -rw-> X（本事务读旧值、别人写了新值）。 */
  readonly outConflicts = new Set<Transaction>();
  private readonly db: Database;
  private readonly writes = new Map<string, number>();

  /** @internal 请使用 Database.begin()。 */
  constructor(db: Database, id: number, snapshotTs: number) {
    this.db = db;
    this.id = id;
    this.snapshotTs = snapshotTs;
  }

  read(key: string): number | undefined {
    this.ensureActive();
    return this.db.snapshotRead(this, key);
  }

  write(key: string, value: number): void {
    this.ensureActive();
    this.db.stageWrite(this, key, value);
  }

  commit(): void {
    this.ensureActive();
    this.db.commitTransaction(this);
  }

  abort(reason = "aborted by user"): void {
    if (this.state !== "active") return;
    this.state = "aborted";
    this.abortReason = reason;
    this.db.releasePendingWrites(this);
  }

  /** @internal */
  markCommitted(seq: number): void {
    this.state = "committed";
    this.commitSeq = seq;
  }

  /** @internal */
  hasWrite(key: string): boolean {
    return this.writes.has(key);
  }

  /** @internal */
  ownWrite(key: string): number | undefined {
    return this.writes.get(key);
  }

  /** @internal */
  putWrite(key: string, value: number): void {
    this.writes.set(key, value);
  }

  /** @internal */
  writeKeys(): Iterable<string> {
    return this.writes.keys();
  }

  private ensureActive(): void {
    if (this.state === "aborted") {
      throw new SerializationError(`T${this.id} is aborted: ${this.abortReason}`);
    }
    if (this.state === "committed") {
      throw new Error(`T${this.id} is already committed`);
    }
  }
}
