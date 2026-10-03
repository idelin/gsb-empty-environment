/**
 * Peritext/Fugue 风格的富文本格式标注区间 CRDT。
 *
 * 两层结构：
 * 1. 字符序列层（RGA）：每个字符拥有全局唯一、全序的 ID `<lamport>@<actor>`，
 *    删除以墓碑（tombstone）方式记录，并发插入按 ID 确定性排序。
 * 2. 格式标注层（Peritext）：每条格式操作（mark）是一对锚点界定的区间
 *    `{attr, value, startAnchor, endAnchor}`，`value = null` 表示“取消该格式”。
 *    锚点引用字符 ID 而非下标，因此并发编辑下区间不会漂移。
 *
 * 冲突判定（边界因果）：
 * - 同一字符、同一属性上若有多条 mark 覆盖，按 (lamport, actor) 取最大者生效（LWW）。
 * - 因果上后发生的操作（lamport 更大）必然获胜；并发操作（lamport 相同）
 *   按 actor 字典序确定性裁决，所有副本收敛到同一结果。
 */

export type MarkValue = string | true;
export type Attrs = Record<string, MarkValue>;

export type AnchorSide = "before" | "after";

/** 区间边界锚点。charId 为 null 时表示文档起点（start 锚）或终点（end 锚）。 */
export interface Anchor {
  side: AnchorSide;
  charId: string | null;
}

/** 在区间边界处键入新字符时，格式是否扩展包含新字符。 */
export type Expand = "none" | "start" | "end" | "both";

export interface InsertOp {
  type: "insert";
  id: string;
  afterId: string | null;
  value: string;
}

export interface DeleteOp {
  type: "delete";
  id: string; // 被删除字符的 ID
}

export interface MarkOp {
  type: "mark";
  opId: string;
  lamport: number;
  actor: string;
  attr: string;
  value: MarkValue | null; // null 表示移除该格式（如取消加粗）
  start: Anchor;
  end: Anchor;
}

export type Op = InsertOp | DeleteOp | MarkOp;

export interface Span {
  start: number;
  end: number;
  text: string;
  marks: Attrs;
}

interface Char {
  id: string;
  afterId: string | null;
  value: string;
}

/** 比较字符/操作 ID：先按 lamport 计数，再按 actor 字典序。 */
export function compareIds(a: string, b: string): number {
  const [ca, aa] = a.split("@");
  const [cb, ab] = b.split("@");
  const na = Number(ca);
  const nb = Number(cb);
  if (na !== nb) return na - nb;
  if (aa === ab) return 0;
  return aa! < ab! ? -1 : 1;
}

function compareMarks(a: MarkOp, b: MarkOp): number {
  if (a.lamport !== b.lamport) return a.lamport - b.lamport;
  if (a.actor === b.actor) return 0;
  return a.actor < b.actor ? -1 : 1;
}

function sameAttrs(a: Attrs, b: Attrs): boolean {
  const ka = Object.keys(a);
  const kb = Object.keys(b);
  if (ka.length !== kb.length) return false;
  return ka.every((k) => a[k] === b[k]);
}

export class RichText {
  readonly actor: string;
  private chars: Char[] = [];
  private deletedIds = new Set<string>();
  private marks: MarkOp[] = [];
  private appliedOps = new Set<string>();
  private pendingInserts: InsertOp[] = [];
  private clock = 0;

  constructor(actor: string) {
    this.actor = actor;
  }

  static from(text: string, actor: string): RichText {
    const doc = new RichText(actor);
    doc.insert(0, text);
    return doc;
  }

  /** 用一批操作构造副本（等价于 new + apply）。 */
  static fromOps(actor: string, ops: Op[]): RichText {
    const doc = new RichText(actor);
    doc.apply(ops);
    return doc;
  }

  // ---------- 本地编辑 ----------

  insert(index: number, text: string): void {
    const visible = this.visibleChars();
    if (index < 0 || index > visible.length) {
      throw new RangeError(`insert 下标越界: ${index}`);
    }
    let afterId: string | null = index === 0 ? null : visible[index - 1]!.id;
    for (const ch of text) {
      const id = `${++this.clock}@${this.actor}`;
      this.applyOp({ type: "insert", id, afterId, value: ch });
      afterId = id;
    }
  }

  delete(index: number, length: number): void {
    const visible = this.visibleChars();
    if (index < 0 || index + length > visible.length) {
      throw new RangeError(`delete 区间越界: [${index}, ${index + length})`);
    }
    for (let i = index; i < index + length; i++) {
      this.applyOp({ type: "delete", id: visible[i]!.id });
    }
  }

  /**
   * 对可见文本区间 [start, start+length) 设置格式。
   * value 为 null 表示移除该格式（取消加粗/取消链接等）。
   * expand 控制边界扩展行为，默认：link 不扩展，其余格式在末尾边界扩展。
   */
  format(
    start: number,
    length: number,
    attr: string,
    value: MarkValue | null,
    expand?: Expand,
  ): void {
    const visible = this.visibleChars();
    if (length <= 0 || start < 0 || start + length > visible.length) {
      throw new RangeError(`format 区间越界: [${start}, ${start + length})`);
    }
    const first = visible[start]!;
    const last = visible[start + length - 1]!;
    const before = start > 0 ? visible[start - 1]! : null;
    const after = start + length < visible.length ? visible[start + length]! : null;

    const exp = expand ?? (attr === "link" ? "none" : "end");
    const expandStart = exp === "start" || exp === "both";
    const expandEnd = exp === "end" || exp === "both";

    const startAnchor: Anchor = expandStart
      ? { side: "after", charId: before ? before.id : null }
      : { side: "before", charId: first.id };
    const endAnchor: Anchor = expandEnd
      ? { side: "before", charId: after ? after.id : null }
      : { side: "after", charId: last.id };

    const lamport = ++this.clock;
    this.applyOp({
      type: "mark",
      opId: `${lamport}@${this.actor}`,
      lamport,
      actor: this.actor,
      attr,
      value,
      start: startAnchor,
      end: endAnchor,
    });
  }

  // ---------- 同步 ----------

  /** 导出全部操作（因果日志顺序，可直接传输给对端）。 */
  getOps(): Op[] {
    const ops: Op[] = [];
    for (const c of this.chars) {
      ops.push({ type: "insert", id: c.id, afterId: c.afterId, value: c.value });
    }
    for (const id of this.deletedIds) {
      ops.push({ type: "delete", id });
    }
    for (const m of this.marks) {
      ops.push({ ...m, start: { ...m.start }, end: { ...m.end } });
    }
    return ops;
  }

  /** 应用一批远程操作。幂等、可乱序（缺失父节点的插入会挂起重试）。 */
  apply(ops: Op[]): void {
    for (const op of ops) this.applyOp(op);
    // 重试因父节点尚未到达而挂起的插入
    let progress = true;
    while (progress && this.pendingInserts.length > 0) {
      progress = false;
      const rest: InsertOp[] = [];
      for (const op of this.pendingInserts) {
        if (this.tryApplyInsert(op)) progress = true;
        else rest.push(op);
      }
      this.pendingInserts = rest;
    }
  }

  /** 合并另一个副本的全部状态。 */
  merge(other: RichText): void {
    this.apply(other.getOps());
  }

  // ---------- 查询 ----------

  getText(): string {
    return this.visibleChars()
      .map((c) => c.value)
      .join("");
  }

  /** 把相邻且格式集合相同的字符合并为区间，保证无格式断裂。 */
  getSpans(): Span[] {
    const spans: Span[] = [];
    let visibleIndex = 0;
    for (let i = 0; i < this.chars.length; i++) {
      const c = this.chars[i]!;
      if (this.deletedIds.has(c.id)) continue;
      const marks = this.attrsAt(i);
      const last = spans[spans.length - 1];
      if (last && sameAttrs(last.marks, marks)) {
        last.text += c.value;
        last.end = visibleIndex + 1;
      } else {
        spans.push({ start: visibleIndex, end: visibleIndex + 1, text: c.value, marks });
      }
      visibleIndex++;
    }
    return spans;
  }

  // ---------- 内部实现 ----------

  private visibleChars(): Char[] {
    return this.chars.filter((c) => !this.deletedIds.has(c.id));
  }

  private applyOp(op: Op): void {
    switch (op.type) {
      case "insert": {
        if (!this.tryApplyInsert(op)) this.pendingInserts.push(op);
        break;
      }
      case "delete": {
        const key = `del:${op.id}`;
        if (this.appliedOps.has(key)) return;
        this.appliedOps.add(key);
        this.deletedIds.add(op.id);
        break;
      }
      case "mark": {
        if (this.appliedOps.has(op.opId)) return;
        this.appliedOps.add(op.opId);
        this.marks.push({ ...op, start: { ...op.start }, end: { ...op.end } });
        this.clock = Math.max(this.clock, op.lamport);
        break;
      }
    }
  }

  /** RGA 插入：父节点缺失时返回 false（调用方负责挂起重试）。 */
  private tryApplyInsert(op: InsertOp): boolean {
    if (this.appliedOps.has(op.id)) return true;
    let parentIdx = -1;
    if (op.afterId !== null) {
      parentIdx = this.chars.findIndex((c) => c.id === op.afterId);
      if (parentIdx === -1) return false;
    }
    let i = parentIdx + 1;
    // 跳过并发先到的、ID 更大的兄弟子树，保证所有副本顺序一致
    while (i < this.chars.length && compareIds(this.chars[i]!.id, op.id) > 0) i++;
    this.chars.splice(i, 0, { id: op.id, afterId: op.afterId, value: op.value });
    this.appliedOps.add(op.id);
    const counter = Number(op.id.split("@")[0]);
    if (counter > this.clock) this.clock = counter;
    return true;
  }

  private anchorIndex(anchor: Anchor): number {
    if (anchor.charId === null) {
      // start 锚的 null 表示文档起点之前，end 锚的 null 表示文档终点之后
      return anchor.side === "after" ? -1 : this.chars.length;
    }
    return this.chars.findIndex((c) => c.id === anchor.charId);
  }

  /** 判断 mark 是否覆盖字符序列（含墓碑）中下标为 charIndex 的字符。 */
  private covers(mark: MarkOp, charIndex: number): boolean {
    const s = mark.start;
    const e = mark.end;
    let startOk: boolean;
    if (s.charId === null) {
      startOk = true; // 锚在文档起点
    } else {
      const si = this.anchorIndex(s);
      if (si === -1) return false; // 锚字符尚未到达
      startOk = s.side === "after" ? charIndex > si : charIndex >= si;
    }
    let endOk: boolean;
    if (e.charId === null) {
      endOk = true; // 锚在文档终点
    } else {
      const ei = this.anchorIndex(e);
      if (ei === -1) return false; // 锚字符尚未到达
      endOk = e.side === "after" ? charIndex <= ei : charIndex < ei;
    }
    return startOk && endOk;
  }

  /** 计算某字符位置上的生效属性：同一属性取 (lamport, actor) 最大的 mark。 */
  private attrsAt(charIndex: number): Attrs {
    const winner = new Map<string, MarkOp>();
    for (const m of this.marks) {
      if (!this.covers(m, charIndex)) continue;
      const cur = winner.get(m.attr);
      if (!cur || compareMarks(m, cur) > 0) winner.set(m.attr, m);
    }
    // 按属性名排序输出，保证所有副本的序列化结果一致
    const attrs: Attrs = {};
    for (const attr of [...winner.keys()].sort()) {
      const m = winner.get(attr)!;
      if (m.value !== null) attrs[attr] = m.value;
    }
    return attrs;
  }
}
