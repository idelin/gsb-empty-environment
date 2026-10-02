import { rowChecksum, type Row } from './rowHash.ts';

export type ChangeEvent =
  | { type: 'upsert'; id: number; row: Row }
  | { type: 'delete'; id: number };

export type ChangeListener = (event: ChangeEvent) => void;

/**
 * 模拟一侧数据库（源库或目标库）的一张表。
 * 每次写入/删除都会广播变更事件，ChecksumIndex 订阅事件后
 * 以 O(1) 增量维护滚动 XOR 校验和 —— 对应真实系统中
 * 数据库基于自身数据持续计算摘要的角色。
 */
export class TableStore {
  readonly name: string;
  private readonly rows = new Map<number, Row>();
  private readonly listeners = new Set<ChangeListener>();

  constructor(name: string) {
    this.name = name;
  }

  onChange(listener: ChangeListener): void {
    this.listeners.add(listener);
  }

  upsert(row: Row): void {
    this.rows.set(row.id, { ...row });
    this.emit({ type: 'upsert', id: row.id, row: this.rows.get(row.id)! });
  }

  delete(id: number): void {
    if (!this.rows.has(id)) return;
    this.rows.delete(id);
    this.emit({ type: 'delete', id });
  }

  get(id: number): Row | undefined {
    const row = this.rows.get(id);
    return row ? { ...row } : undefined;
  }

  get size(): number {
    return this.rows.size;
  }

  /** 全量重算校验和（用于测试校验增量维护的正确性）。 */
  recomputeChecksums(): Map<number, string> {
    const out = new Map<number, string>();
    for (const row of this.rows.values()) out.set(row.id, rowChecksum(row));
    return out;
  }

  private emit(event: ChangeEvent): void {
    for (const listener of this.listeners) listener(event);
  }
}
