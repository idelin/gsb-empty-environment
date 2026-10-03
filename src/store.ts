import type { CdcEvent, CdcOp, RowImage } from './types.ts';

/** CDC 事件监听器 */
export type CdcListener = (event: CdcEvent) => void;

/**
 * 内存表存储：模拟一张数据库表，所有写操作都会捕获
 * Before/After 镜像并以 CDC 事件形式发布（毫秒级时间戳）。
 */
export class InMemoryTable {
  readonly name: string;
  readonly pkColumn: string;
  private rows = new Map<unknown, RowImage>();
  private listeners: CdcListener[] = [];
  private sequence = 0;
  private txCounter = 0;

  constructor(name: string, pkColumn: string) {
    this.name = name;
    this.pkColumn = pkColumn;
  }

  onCdc(listener: CdcListener): void {
    this.listeners.push(listener);
  }

  /** 开启一个事务，返回事务 ID；同一事务内的变更共享 txId */
  beginTx(): string {
    this.txCounter += 1;
    return `tx-${this.txCounter}`;
  }

  private emit(op: CdcOp, txId: string, pk: RowImage, before: RowImage | null, after: RowImage | null): void {
    this.sequence += 1;
    const event: CdcEvent = {
      sequence: this.sequence,
      txId,
      // performance.now 提供亚毫秒精度，满足毫秒级原貌比对
      timestampMs: Math.floor(performance.now() * 1000) / 1000,
      op,
      table: this.name,
      primaryKey: pk,
      before: before ? { ...before } : null,
      after: after ? { ...after } : null,
    };
    for (const listener of this.listeners) listener(event);
  }

  insert(row: RowImage, txId = this.beginTx()): void {
    const pk = row[this.pkColumn];
    if (this.rows.has(pk)) throw new Error(`主键冲突: ${String(pk)}`);
    this.rows.set(pk, { ...row });
    this.emit('INSERT', txId, { [this.pkColumn]: pk }, null, row);
  }

  /** 按谓词更新，返回受影响行数；逐行捕获 Before/After 镜像 */
  update(predicate: (row: RowImage) => boolean, set: RowImage, txId = this.beginTx()): number {
    let affected = 0;
    for (const [pk, row] of this.rows) {
      if (!predicate(row)) continue;
      const before = { ...row };
      const after = { ...row, ...set };
      this.rows.set(pk, after);
      this.emit('UPDATE', txId, { [this.pkColumn]: pk }, before, after);
      affected += 1;
    }
    return affected;
  }

  delete(predicate: (row: RowImage) => boolean, txId = this.beginTx()): number {
    let affected = 0;
    for (const [pk, row] of [...this.rows]) {
      if (!predicate(row)) continue;
      this.rows.delete(pk);
      this.emit('DELETE', txId, { [this.pkColumn]: pk }, row, null);
      affected += 1;
    }
    return affected;
  }

  get(pk: unknown): RowImage | undefined {
    const row = this.rows.get(pk);
    return row ? { ...row } : undefined;
  }

  /** 当前全表快照（按主键排序，便于确定性比对） */
  snapshot(): RowImage[] {
    return [...this.rows.entries()]
      .sort(([a], [b]) => (a as number) - (b as number))
      .map(([, row]) => ({ ...row }));
  }

  get size(): number {
    return this.rows.size;
  }
}
