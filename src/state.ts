/**
 * 源状态层：事务化写入 + 可回放操作日志。
 * - 事务写入先缓冲，提交时一次性同步换入，读者永远看不到中间态；
 * - 提交失败（如预算不足）整体回滚，状态保持不变；
 * - 每次提交追加一条可序列化操作记录，Store.replay 可完整重建状态。
 */

import { BlockStore } from './blocks.ts';

export interface WriteOp {
  cell: string;
  value: unknown;
}

export interface CommitResult {
  seq: number;
  /** 本次提交实际写入的单元（合并去重后）。 */
  cells: string[];
}

export type OpRecord =
  | { type: 'define'; cell: string; value: unknown }
  | { type: 'tx'; seq: number; writes: WriteOp[] };

export interface StateSnapshot {
  version: number;
  get(cell: string): unknown;
  toObject(): Record<string, unknown>;
}

export class Transaction {
  private buffer: WriteOp[] = [];

  set(cell: string, value: unknown): void {
    this.buffer.push({ cell, value });
  }

  get writes(): readonly WriteOp[] {
    return this.buffer;
  }
}

interface CellState {
  block: number;
  version: number;
}

export class Store {
  private blocks: BlockStore;
  private cells = new Map<string, CellState>();
  private log: OpRecord[] = [];
  private seq = 0;
  private version = 0;

  constructor(blocks: BlockStore) {
    this.blocks = blocks;
  }

  defineCell(id: string, initial: unknown): void {
    if (this.cells.has(id)) throw new Error(`cell "${id}" already defined`);
    const block = this.blocks.intern(initial);
    this.cells.set(id, { block, version: 0 });
    this.log.push({ type: 'define', cell: id, value: initial });
  }

  has(id: string): boolean {
    return this.cells.has(id);
  }

  cellBlock(id: string): number {
    return this.mustCell(id).block;
  }

  cellVersion(id: string): number {
    return this.mustCell(id).version;
  }

  readCell(id: string): unknown {
    return this.blocks.resolve(this.mustCell(id).block);
  }

  get storeVersion(): number {
    return this.version;
  }

  /** 可序列化操作日志（深拷贝）。 */
  get opLog(): OpRecord[] {
    return JSON.parse(JSON.stringify(this.log)) as OpRecord[];
  }

  cells_(): { id: string; block: number; version: number }[] {
    const out: { id: string; block: number; version: number }[] = [];
    for (const [id, c] of this.cells) out.push({ id, block: c.block, version: c.version });
    return out;
  }

  async transact(fn: (tx: Transaction) => void | Promise<void>): Promise<CommitResult> {
    const tx = new Transaction();
    await fn(tx);
    return this.commit(tx);
  }

  /**
   * 原子提交：先校验并暂存全部新块（失败即整体回滚），
   * 然后在一个同步片段内换入——读者不可能观察到半提交状态。
   */
  commit(tx: Transaction): CommitResult {
    const merged = new Map<string, unknown>();
    for (const w of tx.writes) merged.set(w.cell, w.value); // 同一 cell 多次写，后者生效
    for (const id of merged.keys()) {
      if (!this.cells.has(id)) throw new Error(`commit: unknown cell "${id}"`);
    }
    const staged: { id: string; block: number }[] = [];
    try {
      for (const [id, value] of merged) staged.push({ id, block: this.blocks.intern(value) });
    } catch (err) {
      for (const s of staged) this.blocks.release(s.block);
      throw err;
    }
    for (const s of staged) {
      const cell = this.cells.get(s.id) as CellState;
      this.blocks.release(cell.block);
      cell.block = s.block;
      cell.version++;
    }
    this.version++;
    const seq = ++this.seq;
    this.log.push({ type: 'tx', seq, writes: tx.writes.map((w) => ({ ...w })) });
    return { seq, cells: [...merged.keys()] };
  }

  /** 物化一致快照（深拷贝当前已提交状态，之后的提交不影响它）。 */
  snapshot(): StateSnapshot {
    const values: Record<string, unknown> = {};
    for (const [id, c] of this.cells) values[id] = this.blocks.resolve(c.block);
    const version = this.version;
    return {
      version,
      get(cell: string): unknown {
        if (!(cell in values)) throw new Error(`snapshot: unknown cell "${cell}"`);
        return values[cell];
      },
      toObject(): Record<string, unknown> {
        return { ...values };
      },
    };
  }

  /** 按操作日志重放，重建出等价状态。 */
  static replay(log: readonly OpRecord[], blocks: BlockStore): Store {
    const store = new Store(blocks);
    for (const op of log) {
      if (op.type === 'define') {
        store.defineCell(op.cell, op.value);
      } else {
        const tx = new Transaction();
        for (const w of op.writes) tx.set(w.cell, w.value);
        store.commit(tx);
      }
    }
    return store;
  }

  private mustCell(id: string): CellState {
    const c = this.cells.get(id);
    if (!c) throw new Error(`unknown cell "${id}"`);
    return c;
  }
}
