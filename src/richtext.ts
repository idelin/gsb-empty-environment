/**
 * Peritext/Fugue 风格富文本格式标注 CRDT。
 *
 * 文本层：RGA（Replicated Growable Array）字符序列。每个字符有全局唯一 ID
 * 与 Lamport 时间戳；并发插入按 (lamport, id) 全序归并，所有副本收敛到
 * 同一字符顺序。
 *
 * 格式层（Peritext 风格）：
 * - 格式标注（bold / italic / underline / link ...）表示为"锚定区间"：
 *   start/end 边界锚定到字符 ID 而非数组下标，并发插入不会移动语义边界。
 * - 边界是 RGA 树中的"虚拟节点"：锚定在某个字符之后，并携带创建该标注的
 *   操作的 (lamport, opId) 作为排序键。于是在边界处新插入的字符
 *   （lamport 更大）排在边界之前、落入区间内；标注创建时已存在的字符
 *   排在边界之后、落在区间外。由此得到 Peritext 边界语义：
 *   在区间末尾插入的字符继承格式，在区间开头插入的字符不继承。
 * - removeMark 通过"删除被覆盖的标注 + 为剩余片段重新生成标注"实现；
 *   删除操作按 markId 因果引用其可见的标注，因此并发的 addMark 不受影响
 *   （add-wins：并发"加粗 vs 取消加粗"时，新加的标注胜出）。
 * - 所有操作可交换、幂等：副本间只需交换操作日志即可收敛。
 */

export type CharId = string;
export type MarkId = string;
export type OpId = string;
export type MarkType = string;

/**
 * 区间边界：位于锚点字符（RGA 子树中）之后、且排序键大于 (lamport, opId)
 * 的子孙之后的逻辑位置。after 为 null 表示锚定在文档开头（根节点）。
 */
export interface Boundary {
  after: CharId | null;
  lamport: number;
  opId: OpId;
}

/** 一条格式标注：类型 + 锚定区间。 */
export interface Mark {
  id: MarkId;
  type: MarkType;
  start: Boundary;
  end: Boundary;
}

export interface InsertOp {
  kind: 'insert';
  opId: OpId; // 同时作为新字符的 CharId
  ch: string;
  prevId: CharId | null; // RGA：插入到该字符之后；null 表示文档开头
  lamport: number;
}

export interface AddMarkOp {
  kind: 'addMark';
  opId: OpId; // 同时作为 MarkId
  lamport: number;
  mark: Mark;
}

export interface RemoveMarkOp {
  kind: 'removeMark';
  opId: OpId;
  lamport: number;
  markId: MarkId; // 因果引用：只删除发起者已见过的标注
}

export type Op = InsertOp | AddMarkOp | RemoveMarkOp;

/** 解析到当前文本下标的标注区间（左闭右开）。 */
export interface Span {
  markId: MarkId;
  type: MarkType;
  start: number;
  end: number;
}

interface CharInfo {
  ch: string;
  lamport: number;
}

const ROOT_KEY = '';

/** 边界构造参数：完整 Boundary（复用旧边界）或待绑定新排序键的锚点。 */
type BoundaryInput = Boundary | { anchor: CharId | null };

export class RichTextDoc {
  readonly actor: string;
  private clock = 0;
  private counter = 0;
  private chars = new Map<CharId, CharInfo>();
  private children = new Map<string, CharId[]>();
  private marks = new Map<MarkId, Mark>();
  private tombstones = new Set<MarkId>();
  private seen = new Set<OpId>();
  private log: Op[] = [];
  private pendingInserts: InsertOp[] = [];

  constructor(actor: string) {
    this.actor = actor;
  }

  static fromText(actor: string, text: string): RichTextDoc {
    const doc = new RichTextDoc(actor);
    doc.insertText(0, text);
    return doc;
  }

  // ---------- 本地编辑 ----------

  insertText(index: number, text: string): void {
    let at = index;
    for (const ch of text) {
      this.insertChar(at, ch);
      at += 1;
    }
  }

  insertChar(index: number, ch: string): void {
    const order = this.charOrder();
    if (index < 0 || index > order.length) {
      throw new RangeError(`insert 位置越界: ${index}（文本长度 ${order.length}）`);
    }
    this.apply({
      kind: 'insert',
      opId: this.freshId(),
      ch,
      prevId: index === 0 ? null : order[index - 1],
      lamport: this.tick(),
    });
  }

  /** 给 [from, to) 添加格式标注。 */
  addMark(type: MarkType, from: number, to: number): void {
    const order = this.charOrder();
    this.checkRange(order, from, to);
    this.applyAddMark(
      type,
      { anchor: from === 0 ? null : order[from - 1] },
      { anchor: order[to - 1] },
    );
  }

  /**
   * 移除 [from, to) 内的某类格式。
   * 对每条与之重叠的标注：删除该标注，并为范围外的左右剩余片段补发新标注
   * （外侧边界复用原标注的边界，保持原有扩张行为）。
   */
  removeMark(type: MarkType, from: number, to: number): void {
    const order = this.charOrder();
    this.checkRange(order, from, to);
    const overlapping = this.getSpans().filter(
      (s) => s.type === type && s.start < to && s.end > from,
    );
    for (const span of overlapping) {
      const mark = this.marks.get(span.markId);
      if (!mark) continue;
      this.apply({ kind: 'removeMark', opId: this.freshId(), lamport: this.tick(), markId: mark.id });
      if (span.start < from) {
        this.applyAddMark(type, mark.start, { anchor: order[from - 1] });
      }
      if (span.end > to) {
        this.applyAddMark(type, { anchor: order[to - 1] }, mark.end);
      }
    }
  }

  // ---------- 副本同步 ----------

  /** 返回本副本已应用的完整操作日志（因果序）。 */
  getOps(): readonly Op[] {
    return this.log;
  }

  applyAll(ops: Iterable<Op>): void {
    for (const op of ops) this.apply(op);
  }

  /** 并入另一个副本的全部操作（幂等，可重复调用）。 */
  merge(other: RichTextDoc): void {
    this.applyAll(other.getOps());
  }

  apply(op: Op): void {
    if (this.seen.has(op.opId)) return;
    this.seen.add(op.opId);
    this.clock = Math.max(this.clock, op.lamport);
    this.log.push(op);
    this.exec(op);
    this.flushPending();
  }

  // ---------- 查询 ----------

  getText(): string {
    let out = '';
    for (const id of this.charOrder()) out += this.chars.get(id)?.ch ?? '';
    return out;
  }

  /** 全部格式标注，解析为当前下标区间，按 (type, start, end) 排序。 */
  getSpans(): Span[] {
    const { order, size } = this.sequence();
    const pos = new Map<CharId, number>();
    order.forEach((id, i) => pos.set(id, i));
    const spans: Span[] = [];
    for (const mark of this.marks.values()) {
      const start = this.resolveBoundary(mark.start, pos, size);
      const end = this.resolveBoundary(mark.end, pos, size);
      if (start === undefined || end === undefined) continue; // 锚点尚未到达
      if (start < end) spans.push({ markId: mark.id, type: mark.type, start, end });
    }
    return spans.sort((a, b) =>
      a.type === b.type
        ? a.start - b.start || a.end - b.end || (a.markId < b.markId ? -1 : a.markId > b.markId ? 1 : 0)
        : a.type < b.type
          ? -1
          : 1,
    );
  }

  /** 每个字符上的格式集合（已排序）。 */
  getFormats(): string[][] {
    const formats: string[][] = this.charOrder().map(() => []);
    for (const span of this.getSpans()) {
      for (let i = span.start; i < span.end; i += 1) {
        if (!formats[i].includes(span.type)) formats[i].push(span.type);
      }
    }
    for (const f of formats) f.sort();
    return formats;
  }

  /** 收敛判定：文本与解析后的标注区间完全一致。 */
  equals(other: RichTextDoc): boolean {
    if (this.getText() !== other.getText()) return false;
    const norm = (doc: RichTextDoc) => doc.getSpans().map((s) => [s.type, s.start, s.end]);
    return JSON.stringify(norm(this)) === JSON.stringify(norm(other));
  }

  // ---------- 内部实现 ----------

  private exec(op: Op): void {
    switch (op.kind) {
      case 'insert': {
        if (this.chars.has(op.opId)) return;
        if (op.prevId !== null && !this.chars.has(op.prevId)) {
          this.pendingInserts.push(op); // 父字符未到达，先缓冲
          return;
        }
        this.chars.set(op.opId, { ch: op.ch, lamport: op.lamport });
        const key = op.prevId ?? ROOT_KEY;
        const siblings = this.children.get(key) ?? [];
        siblings.push(op.opId);
        siblings.sort((x, y) => this.compareSibling(y, x)); // (lamport, id) 降序
        this.children.set(key, siblings);
        return;
      }
      case 'addMark': {
        const mark = op.mark;
        if (this.tombstones.has(mark.id) || this.marks.has(mark.id)) return;
        this.marks.set(mark.id, mark);
        return;
      }
      case 'removeMark': {
        this.tombstones.add(op.markId);
        this.marks.delete(op.markId);
        return;
      }
    }
  }

  private flushPending(): void {
    let progress = true;
    while (progress) {
      progress = false;
      for (let i = 0; i < this.pendingInserts.length; i += 1) {
        const op = this.pendingInserts[i];
        if (op.prevId === null || this.chars.has(op.prevId)) {
          this.pendingInserts.splice(i, 1);
          this.exec(op);
          progress = true;
          break;
        }
      }
    }
  }

  /**
   * 把边界解析为当前文本下标：锚点字符之后，再加上排序键大于边界键的
   * 直接子字符的整个子树（这些子字符是在标注创建之后插入到边界处的）。
   */
  private resolveBoundary(
    b: Boundary,
    pos: Map<CharId, number>,
    size: Map<CharId, number>,
  ): number | undefined {
    let base: number;
    let parentKey: string;
    if (b.after === null) {
      base = 0;
      parentKey = ROOT_KEY;
    } else {
      const i = pos.get(b.after);
      if (i === undefined) return undefined;
      base = i + 1;
      parentKey = b.after;
    }
    let offset = 0;
    for (const child of this.children.get(parentKey) ?? []) {
      if (this.compareKey(child, b.lamport, b.opId) > 0) offset += size.get(child) ?? 1;
    }
    return base + offset;
  }

  /** 比较字符的排序键与给定 (lamport, opId) 键：字符键更大返回 1。 */
  private compareKey(id: CharId, lamport: number, opId: OpId): number {
    const info = this.chars.get(id);
    if (!info) return 0;
    if (info.lamport !== lamport) return info.lamport > lamport ? 1 : -1;
    return id === opId ? 0 : id > opId ? 1 : -1;
  }

  private compareSibling(a: CharId, b: CharId): number {
    const ca = this.chars.get(a);
    const cb = this.chars.get(b);
    if (!ca || !cb) return 0;
    if (ca.lamport !== cb.lamport) return ca.lamport - cb.lamport;
    return a < b ? -1 : a > b ? 1 : 0;
  }

  /** RGA 前序遍历：根的孩子 + 每个字符的子列表均按 (lamport, id) 降序。 */
  private charOrder(): CharId[] {
    return this.sequence().order;
  }

  /** 前序遍历字符序列，并计算每个字符的 RGA 子树大小（供边界解析）。 */
  private sequence(): { order: CharId[]; size: Map<CharId, number> } {
    const out: CharId[] = [];
    const stack: CharId[] = [...(this.children.get(ROOT_KEY) ?? [])].reverse();
    while (stack.length > 0) {
      const id = stack.pop() as CharId;
      out.push(id);
      const kids = this.children.get(id);
      if (kids) for (let i = kids.length - 1; i >= 0; i -= 1) stack.push(kids[i]);
    }
    // 前序遍历中子孙一定排在祖先之后，倒序累加即可得到子树大小
    const size = new Map<CharId, number>();
    for (let i = out.length - 1; i >= 0; i -= 1) {
      const id = out[i];
      let total = 1;
      for (const kid of this.children.get(id) ?? []) total += size.get(kid) ?? 1;
      size.set(id, total);
    }
    return { order: out, size };
  }

  private applyAddMark(type: MarkType, start: BoundaryInput, end: BoundaryInput): void {
    const opId = this.freshId();
    const lamport = this.tick();
    const bind = (input: BoundaryInput): Boundary =>
      'anchor' in input ? { after: input.anchor, lamport, opId } : input;
    const mark: Mark = { id: opId, type, start: bind(start), end: bind(end) };
    this.apply({ kind: 'addMark', opId, lamport, mark });
  }

  private checkRange(order: CharId[], from: number, to: number): void {
    if (from < 0 || to > order.length || from >= to) {
      throw new RangeError(`非法区间 [${from}, ${to})，文本长度 ${order.length}`);
    }
  }

  private freshId(): OpId {
    const id = `${this.actor}:${this.counter}`;
    this.counter += 1;
    return id;
  }

  private tick(): number {
    this.clock += 1;
    return this.clock;
  }
}
