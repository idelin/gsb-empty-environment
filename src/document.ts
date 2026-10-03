/**
 * 基于 LSEQ 标识符的协同序列文档（CRDT）。
 *
 * - 每个元素携带全局唯一、全序的 LSEQ 标识符；
 * - 插入/删除以操作（Op）形式广播，任意顺序、任意次数送达均收敛；
 * - 删除采用墓碑（tombstone），允许 delete 先于对应 insert 到达；
 * - 合并结果只依赖操作集合，不依赖送达顺序 => 严格因果序。
 */

import {
  type Identifier,
  StrategyTable,
  allocate,
  compare,
  depth,
  bitWidth,
  format,
} from './lseq.ts';

export interface InsertOp {
  type: 'insert';
  id: Identifier;
  value: string;
}

export interface DeleteOp {
  type: 'delete';
  id: Identifier;
}

export type Op = InsertOp | DeleteOp;

interface Element {
  id: Identifier;
  value: string;
  deleted: boolean;
}

function keyOf(id: Identifier): string {
  return id.map((c) => `${c.digit}.${c.site}`).join('/');
}

export class LseqDocument {
  readonly site: number;
  private readonly random: () => number;
  private readonly strategies: StrategyTable;
  private elements: Element[] = []; // 始终按 id 升序
  private indexByKey = new Map<string, number>(); // key -> elements 下标（重建维护）
  private tombstones = new Set<string>(); // 已删除但 insert 尚未到达
  private seen = new Set<string>(); // 已应用的 insert，保证幂等

  constructor(site: number, random: () => number = Math.random) {
    this.site = site;
    this.random = random;
    this.strategies = new StrategyTable(random);
  }

  /** 当前可见文本。 */
  text(): string {
    let out = '';
    for (const el of this.elements) if (!el.deleted) out += el.value;
    return out;
  }

  /** 可见长度。 */
  get length(): number {
    let n = 0;
    for (const el of this.elements) if (!el.deleted) n++;
    return n;
  }

  /** 本地插入：在可见下标 index 处插入 value，返回可广播的操作。 */
  insert(index: number, value: string): InsertOp {
    const visible = this.visibleElements();
    if (index < 0 || index > visible.length) {
      throw new RangeError(`insert index ${index} out of range [0, ${visible.length}]`);
    }
    const prev = index > 0 ? visible[index - 1].id : null;
    const next = index < visible.length ? visible[index].id : null;
    const id = allocate(prev, next, this.site, this.strategies, this.random);
    const op: InsertOp = { type: 'insert', id, value };
    this.apply(op);
    return op;
  }

  /** 本地删除：删除可见下标 index 处的元素，返回可广播的操作。 */
  delete(index: number): DeleteOp {
    const visible = this.visibleElements();
    if (index < 0 || index >= visible.length) {
      throw new RangeError(`delete index ${index} out of range [0, ${visible.length})`);
    }
    const op: DeleteOp = { type: 'delete', id: visible[index].id };
    this.apply(op);
    return op;
  }

  /** 应用远端/本地操作，幂等且与顺序无关。 */
  apply(op: Op): void {
    const key = keyOf(op.id);
    if (op.type === 'insert') {
      if (this.seen.has(key)) return;
      this.seen.add(key);
      const el: Element = {
        id: op.id,
        value: op.value,
        deleted: this.tombstones.has(key),
      };
      this.insertSorted(el);
    } else {
      this.tombstones.add(key);
      const i = this.indexByKey.get(key);
      if (i !== undefined) this.elements[i].deleted = true;
    }
  }

  /** 批量合并（任意排列的操作序列）。 */
  merge(ops: readonly Op[]): void {
    for (const op of ops) this.apply(op);
  }

  /** 与另一文档合并：直接吸收对方全部操作状态。 */
  mergeDocument(other: LseqDocument): void {
    for (const el of other.elements) {
      this.apply({ type: 'insert', id: el.id, value: el.value });
      if (el.deleted) this.apply({ type: 'delete', id: el.id });
    }
    for (const key of other.tombstones) {
      if (!this.seen.has(key)) this.tombstones.add(key);
    }
  }

  /** 诊断统计：标识符深度与位宽。 */
  stats(): { count: number; maxDepth: number; avgDepth: number; maxBits: number } {
    let maxDepth = 0;
    let sumDepth = 0;
    let maxBits = 0;
    for (const el of this.elements) {
      const d = depth(el.id);
      maxDepth = Math.max(maxDepth, d);
      sumDepth += d;
      maxBits = Math.max(maxBits, bitWidth(el.id));
    }
    return {
      count: this.elements.length,
      maxDepth,
      avgDepth: this.elements.length ? sumDepth / this.elements.length : 0,
      maxBits,
    };
  }

  /** 调试：格式化所有标识符。 */
  debugIds(): string[] {
    return this.elements.map((el) => `${format(el.id)}${el.deleted ? ' (del)' : ''} ${el.value}`);
  }

  private visibleElements(): Element[] {
    return this.elements.filter((el) => !el.deleted);
  }

  private insertSorted(el: Element): void {
    let lo = 0;
    let hi = this.elements.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (compare(this.elements[mid].id, el.id) < 0) lo = mid + 1;
      else hi = mid;
    }
    this.elements.splice(lo, 0, el);
    this.rebuildIndex(lo);
  }

  private rebuildIndex(from = 0): void {
    for (let i = from; i < this.elements.length; i++) {
      this.indexByKey.set(keyOf(this.elements[i].id), i);
    }
  }
}

/** 可复现的伪随机数发生器（mulberry32），测试与样例使用。 */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
