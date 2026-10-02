/**
 * Undo/Redo CRDT Graph — 可逆编辑撤销树
 *
 * 核心思想：
 * - 所有编辑（含撤销/重做）都是追加到 DAG 上的操作节点，历史永不修改。
 * - Undo 不是删除节点，而是追加一个"反向补偿节点"（insert->delete, delete->restore）。
 * - 每个用户拥有独立的 undo/redo 栈，因此用户 A 的撤销只补偿 A 自己的操作，
 *   不会破坏用户 B 并发输入的内容。
 * - 文档内容通过对操作图做拓扑排序后重放得到，墓碑（tombstone）元素仍然
 *   作为其他并发插入的锚点，保证拓扑有效性。
 */

export type SiteId = string;
export type OpId = string;
export type ElementId = string;

interface BaseOp {
  id: OpId;
  author: SiteId;
  /** 创建该操作时图的头部节点（因果父节点） */
  parents: OpId[];
  /** 若本节点是补偿节点，指向被补偿的原始操作 */
  compensates: OpId | null;
}

export interface InsertOp extends BaseOp {
  kind: 'insert';
  elementId: ElementId;
  afterId: ElementId | null;
  text: string;
}

export interface DeleteOp extends BaseOp {
  kind: 'delete';
  targetId: ElementId;
}

export interface RestoreOp extends BaseOp {
  kind: 'restore';
  targetId: ElementId;
}

export type Op = InsertOp | DeleteOp | RestoreOp;

interface Element {
  id: ElementId;
  text: string;
  afterId: ElementId | null;
  deleted: boolean;
  insertedBy: OpId;
}

const ROOT = '<root>';

export class UndoTree {
  readonly replicaId: string;
  private opSeq = 0;
  private elementSeq = 0;
  private ops = new Map<OpId, Op>();
  private pending: Op[] = [];
  private headIds = new Set<OpId>();
  private undoStacks = new Map<SiteId, Op[]>();
  private redoStacks = new Map<SiteId, Op[]>();

  constructor(replicaId = 'replica') {
    this.replicaId = replicaId;
  }

  // ---------- 编辑 API ----------

  insert(author: SiteId, afterId: ElementId | null, text: string): InsertOp {
    const op: InsertOp = {
      ...this.baseOp(author),
      kind: 'insert',
      elementId: `el#${this.replicaId}:${this.elementSeq++}`,
      afterId,
      text,
    };
    this.commit(op);
    this.pushUndoable(op);
    this.redoStacks.set(author, []);
    return op;
  }

  delete(author: SiteId, targetId: ElementId): DeleteOp {
    const op: DeleteOp = { ...this.baseOp(author), kind: 'delete', targetId };
    this.commit(op);
    this.pushUndoable(op);
    this.redoStacks.set(author, []);
    return op;
  }

  undo(author: SiteId): Op | null {
    const stack = this.undoStacks.get(author) ?? [];
    const target = stack.pop();
    if (!target) return null;
    const compensation = this.inverseOf(target, author);
    this.commit(compensation);
    this.push(this.redoStacks, author, compensation);
    return compensation;
  }

  redo(author: SiteId): Op | null {
    const stack = this.redoStacks.get(author) ?? [];
    const target = stack.pop();
    if (!target) return null;
    const reapply = this.inverseOf(target, author);
    this.commit(reapply);
    this.pushUndoable(reapply);
    return reapply;
  }

  // ---------- 协同：接收远端操作 ----------

  receive(op: Op): void {
    if (this.ops.has(op.id)) return;
    this.pending.push(op);
    this.flushPending();
  }

  private flushPending(): void {
    let progressed = true;
    while (progressed) {
      progressed = false;
      for (let i = 0; i < this.pending.length; i++) {
        const op = this.pending[i];
        if (op.parents.every((p) => this.ops.has(p))) {
          this.commit(op);
          if (op.compensates === null) this.pushUndoable(op);
          this.pending.splice(i, 1);
          progressed = true;
          break;
        }
      }
    }
  }

  // ---------- 查询 ----------

  text(): string {
    return this.materialize()
      .filter((el) => !el.deleted)
      .map((el) => el.text)
      .join('');
  }

  /** 文档末尾可见元素 id，作为后续插入的锚点 */
  tailElementId(): ElementId | null {
    const visible = this.materialize().filter((el) => !el.deleted);
    return visible.length ? visible[visible.length - 1].id : null;
  }

  heads(): OpId[] {
    return [...this.headIds].sort();
  }

  getOps(): Op[] {
    return this.orderedOps();
  }

  /** 拓扑有效性：所有父引用都能解析，且图无环（拓扑排序可完成） */
  validateTopology(): boolean {
    for (const op of this.ops.values()) {
      for (const p of op.parents) {
        if (!this.ops.has(p)) return false;
      }
    }
    return this.orderedOps().length === this.ops.size;
  }

  describe(): string {
    const lines: string[] = [];
    for (const op of this.orderedOps()) {
      const comp = op.compensates ? ` compensates=${op.compensates}` : '';
      const parents = `parents=[${op.parents.join(', ')}]`;
      if (op.kind === 'insert') {
        lines.push(`${op.id} [${op.author}] insert ${JSON.stringify(op.text)} (${op.elementId}) after ${op.afterId ?? ROOT} ${parents}${comp}`);
      } else {
        lines.push(`${op.id} [${op.author}] ${op.kind} ${op.targetId} ${parents}${comp}`);
      }
    }
    return lines.join('\n');
  }

  toDot(): string {
    const lines = ['digraph UndoTree {', '  rankdir=LR;'];
    for (const op of this.orderedOps()) {
      const label =
        op.kind === 'insert'
          ? `${op.id}\\n${op.author}: insert ${op.text}`
          : `${op.id}\\n${op.author}: ${op.kind}`;
      const style = op.compensates ? ' [style=dashed]' : '';
      lines.push(`  "${op.id}" [label="${label}"]${style};`);
      for (const p of op.parents) lines.push(`  "${p}" -> "${op.id}";`);
      if (op.compensates) lines.push(`  "${op.compensates}" -> "${op.id}" [style=dotted, label="inverse"];`);
    }
    lines.push('}');
    return lines.join('\n');
  }

  // ---------- 内部实现 ----------

  private nextOpId(): OpId {
    return `op#${this.replicaId}:${++this.opSeq}`;
  }

  private baseOp(author: SiteId): BaseOp {
    return { id: this.nextOpId(), author, parents: this.heads(), compensates: null };
  }

  private inverseOf(op: Op, author: SiteId): Op {
    const base: BaseOp = {
      id: this.nextOpId(),
      author,
      parents: this.heads(),
      compensates: op.id,
    };
    switch (op.kind) {
      case 'insert':
        return { ...base, kind: 'delete', targetId: op.elementId };
      case 'delete':
        return { ...base, kind: 'restore', targetId: op.targetId };
      case 'restore':
        return { ...base, kind: 'delete', targetId: op.targetId };
    }
  }

  private commit(op: Op): void {
    this.ops.set(op.id, op);
    for (const p of op.parents) this.headIds.delete(p);
    this.headIds.add(op.id);
  }

  private pushUndoable(op: Op): void {
    this.push(this.undoStacks, op.author, op);
  }

  private push(stacks: Map<SiteId, Op[]>, author: SiteId, op: Op): void {
    if (!stacks.has(author)) stacks.set(author, []);
    stacks.get(author)!.push(op);
  }

  /** Kahn 拓扑排序，就绪集按 op id 字典序保证跨副本确定性 */
  private orderedOps(): Op[] {
    const indegree = new Map<OpId, number>();
    const children = new Map<OpId, OpId[]>();
    for (const op of this.ops.values()) {
      indegree.set(op.id, op.parents.filter((p) => this.ops.has(p)).length);
      for (const p of op.parents) {
        if (!this.ops.has(p)) continue;
        if (!children.has(p)) children.set(p, []);
        children.get(p)!.push(op.id);
      }
    }
    const ready = [...this.ops.values()].filter((o) => indegree.get(o.id) === 0).map((o) => o.id);
    const result: Op[] = [];
    while (ready.length > 0) {
      ready.sort();
      const id = ready.shift()!;
      result.push(this.ops.get(id)!);
      for (const child of children.get(id) ?? []) {
        indegree.set(child, indegree.get(child)! - 1);
        if (indegree.get(child) === 0) ready.push(child);
      }
    }
    return result;
  }

  /** 重放操作图，得到带墓碑的元素序列（RGA 风格：墓碑仍是子元素的锚点） */
  private materialize(): Element[] {
    const elements = new Map<ElementId, Element>();
    for (const op of this.orderedOps()) {
      if (op.kind === 'insert') {
        elements.set(op.elementId, {
          id: op.elementId,
          text: op.text,
          afterId: op.afterId,
          deleted: false,
          insertedBy: op.id,
        });
      } else {
        const el = elements.get(op.targetId);
        if (el) el.deleted = op.kind === 'delete';
      }
    }
    const children = new Map<string, Element[]>();
    for (const el of elements.values()) {
      const key = el.afterId ?? ROOT;
      if (!children.has(key)) children.set(key, []);
      children.get(key)!.push(el);
    }
    for (const list of children.values()) {
      list.sort((a, b) => b.insertedBy.localeCompare(a.insertedBy) || b.id.localeCompare(a.id));
    }
    const out: Element[] = [];
    const visit = (key: string) => {
      for (const el of children.get(key) ?? []) {
        out.push(el);
        visit(el.id);
      }
    };
    visit(ROOT);
    return out;
  }
}
