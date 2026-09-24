/**
 * BranchingHistory —— 可分支的撤销历史。
 *
 * 核心语义：
 * - 撤销后继续编辑，从当前节点长出新的历史分支，旧分支保留，可在分支间切换。
 * - 连续键入（插入/退格）自动合并为同一节点；光标移动、撤销/重做、分支切换、
 *   以及显式事务边界 commit() 都会打断合并。
 * - 历史容量回收（compact）只把“所有存活分支的公共前缀”压缩为基线快照，
 *   绝不破坏存活分支的可回放性。
 * - 所有公开偏移均为 UTF-16 code unit 偏移；编辑范围会自动外扩，禁止切开代理对。
 */

export interface Selection {
  anchor: number;
  focus: number;
}

export interface Edit {
  /** 替换起点（UTF-16 偏移） */
  start: number;
  /** 被删除的 UTF-16 code unit 数 */
  deleteCount: number;
  /** 插入的文本 */
  text: string;
}

export interface BranchInfo {
  id: number;
  text: string;
  selection: Selection;
}

export interface NodeInfo {
  id: number;
  parentId: number | null;
  edit: Edit | null;
}

interface HistNode {
  id: number;
  parent: HistNode | null;
  edit: Edit | null;
  selBefore: Selection;
  selAfter: Selection;
  children: HistNode[];
  lastChild: HistNode | null;
  snapshot: string | null;
}

function isHighSurrogate(code: number): boolean {
  return code >= 0xd800 && code <= 0xdbff;
}

function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}

/** offset 是否落在某个代理对中间（前面是高代理、后面是低代理）。 */
export function splitsSurrogatePair(text: string, offset: number): boolean {
  return (
    offset > 0 &&
    offset < text.length &&
    isHighSurrogate(text.charCodeAt(offset - 1)) &&
    isLowSurrogate(text.charCodeAt(offset))
  );
}

function cloneSel(sel: Selection): Selection {
  return { anchor: sel.anchor, focus: sel.focus };
}

export class BranchingHistory {
  private root: HistNode;
  private current: HistNode;
  private selection: Selection;
  private nextId = 1;
  private coalescible = false;
  readonly maxNodes: number;

  constructor(initialText = "", options: { maxNodes?: number } = {}) {
    this.maxNodes = options.maxNodes ?? Infinity;
    const sel: Selection = { anchor: 0, focus: 0 };
    this.root = {
      id: 0,
      parent: null,
      edit: null,
      selBefore: sel,
      selAfter: sel,
      children: [],
      lastChild: null,
      snapshot: initialText,
    };
    this.current = this.root;
    this.selection = cloneSel(sel);
  }

  // ---------- 查询 ----------

  get text(): string {
    return this.textAtNode(this.current);
  }

  get selectionRange(): Selection {
    return cloneSel(this.selection);
  }

  get currentNodeId(): number {
    return this.current.id;
  }

  /** 当前所有存活分支（叶子节点）。 */
  branches(): BranchInfo[] {
    const tips: BranchInfo[] = [];
    this.walk(this.root, (node) => {
      if (node.children.length === 0) {
        tips.push({
          id: node.id,
          text: this.textAtNode(node),
          selection: cloneSel(node.selAfter),
        });
      }
    });
    return tips;
  }

  /** 当前仍可见（保留在树中）的节点列表。 */
  nodes(): NodeInfo[] {
    const infos: NodeInfo[] = [];
    this.walk(this.root, (node) => {
      infos.push({
        id: node.id,
        parentId: node.parent ? node.parent.id : null,
        edit: node.edit ? { ...node.edit } : null,
      });
    });
    return infos;
  }

  nodeCount(): number {
    let count = 0;
    this.walk(this.root, () => count++);
    return count;
  }

  /** 回放任意可见节点的文本。 */
  textAt(id: number): string | undefined {
    const node = this.find(id);
    return node ? this.textAtNode(node) : undefined;
  }

  // ---------- 编辑 ----------

  /**
   * 用 text 替换 [start, end)（UTF-16 偏移，end 排他）。
   * 范围会被钳制到文本内，并向外扩展以避免切开代理对。
   */
  replace(start: number, end: number, text: string): void {
    const doc = this.text;
    let s = Math.max(0, Math.min(start, doc.length));
    let e = Math.max(s, Math.min(end, doc.length));
    if (splitsSurrogatePair(doc, s)) s -= 1;
    if (splitsSurrogatePair(doc, e)) e += 1;
    const edit: Edit = { start: s, deleteCount: e - s, text };

    if (this.coalescible && this.current.edit && this.tryMerge(this.current.edit, edit)) {
      this.current.snapshot = null;
      this.current.selAfter = { anchor: s + text.length, focus: s + text.length };
      this.selection = cloneSel(this.current.selAfter);
      return;
    }

    const node: HistNode = {
      id: this.nextId++,
      parent: this.current,
      edit,
      selBefore: cloneSel(this.selection),
      selAfter: { anchor: s + text.length, focus: s + text.length },
      children: [],
      lastChild: null,
      snapshot: null,
    };
    this.current.children.push(node);
    this.current.lastChild = node;
    this.current = node;
    this.selection = cloneSel(node.selAfter);
    this.coalescible = true;
    this.enforceCapacity();
  }

  /** 在当前选择区插入文本（若有选区则替换之）。 */
  insert(text: string): void {
    const lo = Math.min(this.selection.anchor, this.selection.focus);
    const hi = Math.max(this.selection.anchor, this.selection.focus);
    this.replace(lo, hi, text);
  }

  /** 退格：删除光标前 count 个 code unit，自动扩展到完整码点（不切开代理对）。 */
  deleteBackward(count = 1): void {
    const doc = this.text;
    const lo = Math.min(this.selection.anchor, this.selection.focus);
    const hi = Math.max(this.selection.anchor, this.selection.focus);
    if (lo !== hi) {
      this.replace(lo, hi, "");
      return;
    }
    let start = Math.max(0, hi - count);
    if (splitsSurrogatePair(doc, start)) start -= 1;
    this.replace(start, hi, "");
  }

  /** 显式事务边界：下一次键入不与之前合并。 */
  commit(): void {
    this.coalescible = false;
  }

  /** 移动光标/选区（UTF-16 偏移），会打断键入合并；偏移不切开代理对。 */
  setSelection(anchor: number, focus = anchor): void {
    const len = this.text.length;
    let a = Math.max(0, Math.min(anchor, len));
    let f = Math.max(0, Math.min(focus, len));
    const doc = this.text;
    if (splitsSurrogatePair(doc, a)) a -= 1;
    if (splitsSurrogatePair(doc, f)) f -= 1;
    this.selection = { anchor: a, focus: f };
    this.coalescible = false;
  }

  // ---------- 历史导航 ----------

  undo(): boolean {
    if (!this.current.parent) return false;
    const undone = this.current;
    this.current.parent.lastChild = undone;
    this.current = this.current.parent;
    this.selection = cloneSel(undone.selBefore);
    this.coalescible = false;
    return true;
  }

  redo(): boolean {
    const next = this.current.lastChild;
    if (!next) return false;
    this.current = next;
    this.selection = cloneSel(next.selAfter);
    this.coalescible = false;
    return true;
  }

  /** 切换到任意可见节点（分支），文本与选择区一起恢复。 */
  switchTo(id: number): boolean {
    const node = this.find(id);
    if (!node) return false;
    this.current = node;
    this.selection = cloneSel(node.selAfter);
    this.coalescible = false;
    return true;
  }

  // ---------- 容量回收 ----------

  /**
   * 压缩：把所有存活分支（叶子 + 当前节点）的公共前缀折叠为一个
   * 带快照的基线节点。压缩后每个仍可见的节点都可以正常回放。
   * 返回回收的节点数；若公共前缀就是根（无可回收），返回 0。
   */
  compact(): number {
    const tips: HistNode[] = [];
    this.walk(this.root, (node) => {
      if (node.children.length === 0) tips.push(node);
    });
    if (!tips.includes(this.current)) tips.push(this.current);

    let base = tips[0];
    for (const tip of tips.slice(1)) base = this.commonAncestor(base, tip);
    if (base === this.root) return 0;

    const reclaimed = this.depth(base);
    const baseline: HistNode = {
      id: this.nextId++,
      parent: null,
      edit: null,
      selBefore: cloneSel(base.selAfter),
      selAfter: cloneSel(base.selAfter),
      children: base.children,
      lastChild: base.lastChild,
      snapshot: this.textAtNode(base),
    };
    for (const child of base.children) child.parent = baseline;
    this.root = baseline;
    if (this.current === base) this.current = baseline;
    return reclaimed;
  }

  // ---------- 内部 ----------

  private tryMerge(prev: Edit, next: Edit): boolean {
    // 连续插入：新插入紧跟在上次插入末尾
    if (prev.deleteCount === 0 && next.deleteCount === 0 && next.start === prev.start + prev.text.length) {
      prev.text += next.text;
      return true;
    }
    // 连续退格：新删除区间紧接上次的起点
    if (prev.text === "" && next.text === "" && next.start + next.deleteCount === prev.start) {
      prev.start = next.start;
      prev.deleteCount += next.deleteCount;
      return true;
    }
    return false;
  }

  private textAtNode(node: HistNode): string {
    if (node.snapshot !== null) return node.snapshot;
    const chain: HistNode[] = [];
    let cursor: HistNode | null = node;
    while (cursor !== null && cursor.snapshot === null) {
      chain.push(cursor);
      cursor = cursor.parent;
    }
    let text = cursor!.snapshot!;
    for (let i = chain.length - 1; i >= 0; i--) {
      const e = chain[i].edit!;
      text = text.slice(0, e.start) + e.text + text.slice(e.start + e.deleteCount);
    }
    node.snapshot = text;
    return text;
  }

  private find(id: number): HistNode | null {
    let found: HistNode | null = null;
    this.walk(this.root, (node) => {
      if (node.id === id) found = node;
    });
    return found;
  }

  private walk(node: HistNode, visit: (node: HistNode) => void): void {
    visit(node);
    for (const child of node.children) this.walk(child, visit);
  }

  private commonAncestor(a: HistNode, b: HistNode): HistNode {
    const ancestors = new Set<HistNode>();
    for (let n: HistNode | null = a; n; n = n.parent) ancestors.add(n);
    let cursor: HistNode | null = b;
    while (cursor && !ancestors.has(cursor)) cursor = cursor.parent;
    return cursor!;
  }

  private depth(node: HistNode): number {
    let d = 0;
    for (let n: HistNode | null = node; n.parent; n = n.parent) d++;
    return d;
  }

  private enforceCapacity(): void {
    let guard = 0;
    while (this.nodeCount() > this.maxNodes && guard++ < 1000) {
      if (this.compact() === 0) break; // 不破坏存活分支基线，容量是尽力而为
    }
  }
}
