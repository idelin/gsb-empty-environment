import type { VersionVector } from "./version-vector.ts";

/** 插入操作：在 after 所指元素之后插入一个字符（after 为 null 表示文档头部）。 */
export interface InsertOp {
  t: "i";
  replica: string;
  seq: number;
  after: string | null;
  char: string;
}

/** 删除操作：墓碑化目标元素。 */
export interface DeleteOp {
  t: "d";
  replica: string;
  seq: number;
  target: string;
}

export type Op = InsertOp | DeleteOp;

export interface ElementState {
  id: string;
  replica: string;
  seq: number;
  after: string | null;
  char: string;
  deleted: boolean;
}

type Element = ElementState;

export function elementId(replica: string, seq: number): string {
  return `${replica}:${seq}`;
}

/**
 * RGA 风格的 Delta-CRDT 文档。
 *
 * - 每个字符是一个带全局唯一 id 的 CRDT 节点，删除即墓碑。
 * - 所有已应用操作按因果顺序追加到 log，deltaSince(vv) 只切出
 *   未被 vv 覆盖的操作切片，即增量 Delta。
 * - 版本向量保证每个副本的操作恰好应用一次；乱序到达的操作
 *   会被缓冲，直到前驱缺口补齐。
 */
export class DocumentCRDT {
  readonly replica: string;
  private vv: VersionVector = Object.create(null);
  private elements = new Map<string, Element>();
  private children = new Map<string | null, string[]>();
  private log: Op[] = [];
  private pending = new Map<string, Op[]>();
  private pendingDeletes = new Set<string>();

  constructor(replica: string) {
    this.replica = replica;
  }

  get version(): VersionVector {
    return { ...this.vv };
  }

  /** 当前增量日志中尚未被全部对等端确认的操作数。 */
  get logSize(): number {
    return this.log.length;
  }

  get length(): number {
    let count = 0;
    for (const el of this.elements.values()) if (!el.deleted) count++;
    return count;
  }

  private nextSeq(): number {
    return (this.vv[this.replica] ?? 0) + 1;
  }

  /** 在指定元素之后插入字符，返回新元素 id。 */
  insertAfter(after: string | null, char: string): string {
    const op: InsertOp = { t: "i", replica: this.replica, seq: this.nextSeq(), after, char };
    this.apply(op);
    return elementId(op.replica, op.seq);
  }

  /** 在可见文本的 index 处插入字符。 */
  insertAt(index: number, char: string): string {
    const visible = this.visibleIds();
    if (index < 0 || index > visible.length) {
      throw new RangeError(`insert index ${index} out of range [0, ${visible.length}]`);
    }
    return this.insertAfter(index === 0 ? null : visible[index - 1], char);
  }

  /** 删除可见文本 index 处的字符。 */
  deleteAt(index: number): void {
    const visible = this.visibleIds();
    if (index < 0 || index >= visible.length) {
      throw new RangeError(`delete index ${index} out of range [0, ${visible.length})`);
    }
    const op: DeleteOp = { t: "d", replica: this.replica, seq: this.nextSeq(), target: visible[index] };
    this.apply(op);
  }

  /** 应用单个操作（本地或远端），幂等；乱序操作进入缓冲队列。 */
  apply(op: Op): boolean {
    const seen = this.vv[op.replica] ?? 0;
    if (op.seq <= seen) return false;
    if (op.seq > seen + 1) {
      const queue = this.pending.get(op.replica) ?? [];
      let i = queue.length;
      while (i > 0 && queue[i - 1].seq > op.seq) i--;
      if (i < queue.length && queue[i].seq === op.seq) return false;
      queue.splice(i, 0, op);
      this.pending.set(op.replica, queue);
      return false;
    }
    this.exec(op);
    this.vv[op.replica] = seen + 1;
    this.drainPending(op.replica);
    return true;
  }

  merge(ops: readonly Op[]): void {
    for (const op of ops) this.apply(op);
  }

  private drainPending(replica: string): void {
    const queue = this.pending.get(replica);
    if (!queue) return;
    while (queue.length > 0 && queue[0].seq === (this.vv[replica] ?? 0) + 1) {
      const op = queue.shift()!;
      this.exec(op);
      this.vv[replica] = op.seq;
    }
    if (queue.length === 0) this.pending.delete(replica);
  }

  private exec(op: Op): void {
    if (op.t === "i") this.execInsert(op);
    else this.execDelete(op);
    this.log.push(op);
  }

  private execInsert(op: InsertOp): void {
    const id = elementId(op.replica, op.seq);
    if (this.elements.has(id)) return;
    const el: Element = {
      id,
      replica: op.replica,
      seq: op.seq,
      after: op.after,
      char: op.char,
      deleted: this.pendingDeletes.delete(id),
    };
    this.elements.set(id, el);
    this.attachChild(el);
  }

  private execDelete(op: DeleteOp): void {
    const el = this.elements.get(op.target);
    if (el) el.deleted = true;
    else this.pendingDeletes.add(op.target);
  }

  /** RGA 兄弟排序规则：序号降序，副本 id 降序（并发插入确定性收敛）。 */
  private attachChild(el: Element): void {
    const siblings = this.children.get(el.after) ?? [];
    let i = 0;
    while (i < siblings.length) {
      const sib = this.elements.get(siblings[i])!;
      if (sib.seq > el.seq || (sib.seq === el.seq && sib.replica > el.replica)) i++;
      else break;
    }
    siblings.splice(i, 0, el.id);
    this.children.set(el.after, siblings);
  }

  /** 迭代式先序遍历，避免长链递归爆栈。 */
  private order(): string[] {
    const result: string[] = [];
    const stack: string[] = [];
    const roots = this.children.get(null) ?? [];
    for (let i = roots.length - 1; i >= 0; i--) stack.push(roots[i]);
    while (stack.length > 0) {
      const id = stack.pop()!;
      result.push(id);
      const kids = this.children.get(id);
      if (kids) for (let i = kids.length - 1; i >= 0; i--) stack.push(kids[i]);
    }
    return result;
  }

  private visibleIds(): string[] {
    return this.order().filter((id) => !this.elements.get(id)!.deleted);
  }

  text(): string {
    let out = "";
    for (const id of this.order()) {
      const el = this.elements.get(id)!;
      if (!el.deleted) out += el.char;
    }
    return out;
  }

  /** 核心增量接口：仅返回未被 base 版本向量覆盖的操作切片。 */
  deltaSince(base: VersionVector): Op[] {
    return this.log.filter((op) => op.seq > (base[op.replica] ?? 0));
  }

  /** 全量状态快照（用于初始引导或与传统 State-based 传输对照）。 */
  snapshot(): { vv: VersionVector; elements: ElementState[] } {
    return { vv: this.version, elements: [...this.elements.values()].map((el) => ({ ...el })) };
  }

  mergeState(state: { vv: VersionVector; elements: ElementState[] }): void {
    for (const e of state.elements) {
      const existing = this.elements.get(e.id);
      if (existing) {
        existing.deleted = existing.deleted || e.deleted;
        continue;
      }
      const el: Element = { ...e, deleted: e.deleted || this.pendingDeletes.delete(e.id) };
      this.elements.set(e.id, el);
      this.attachChild(el);
    }
    for (const [replica, seq] of Object.entries(state.vv)) {
      this.vv[replica] = Math.max(this.vv[replica] ?? 0, seq);
    }
    for (const replica of [...this.pending.keys()]) this.drainPending(replica);
  }

  /** 回收已被所有对等端确认的日志前缀。 */
  gc(coverage: VersionVector): void {
    this.log = this.log.filter((op) => op.seq > (coverage[op.replica] ?? 0));
  }
}
