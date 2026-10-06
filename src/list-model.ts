/**
 * 乱序收敛列表模型（CRDT）
 *
 * 语义：
 * - insert 携带全局唯一 id 与前驱 id（pred 为 null 表示列表头部）。
 * - 同一前驱下的兄弟节点按 id 字典序排列，列表顺序为从根深度优先遍历。
 * - delete 可先于 insert 到达：先建占位墓碑，insert 到达后补全内容，
 *   墓碑永久保留，作为后继节点的锚点。
 * - 操作幂等：重复 insert / delete 安全忽略。
 * - 前驱完全未知（无 insert 也无墓碑）时，insert 进入暂存区，
 *   待前驱出现后自动级联挂载，绝不丢弃。
 */

export type Id = string;

export interface InsertOp {
  kind: "insert";
  id: Id;
  pred: Id | null;
  value: string;
}

export interface DeleteOp {
  kind: "delete";
  id: Id;
}

export type Op = InsertOp | DeleteOp;

interface Node {
  id: Id;
  pred: Id | null;
  value: string;
  deleted: boolean;
  /** insert 是否已到达（false 表示 delete 先至留下的占位墓碑） */
  anchored: boolean;
}

export interface Diagnostics {
  /** 因前驱未知而暂存的 insert 操作（按 id 排序，保证确定性） */
  pendingInserts: InsertOp[];
  /** 被引用但 insert 从未到达的 id：暂存操作的前驱 + 占位墓碑自身 */
  missingDependencies: Id[];
}

export class ListModel {
  private nodes = new Map<Id, Node>();
  /** 未知前驱 id -> 等待它的 insert 列表 */
  private pending = new Map<Id, InsertOp[]>();

  apply(op: Op): void {
    if (op.kind === "insert") this.applyInsert(op);
    else this.applyDelete(op);
  }

  applyAll(ops: Iterable<Op>): void {
    for (const op of ops) this.apply(op);
  }

  private applyInsert(op: InsertOp): void {
    const existing = this.nodes.get(op.id);
    if (existing?.anchored) return; // 重复 insert，幂等忽略

    if (op.pred !== null && !this.nodes.has(op.pred)) {
      this.buffer(op); // 前驱完全未知：暂存而非丢弃
      return;
    }

    if (existing) {
      // delete 先至的占位墓碑：补全内容，deleted 标记保留
      existing.pred = op.pred;
      existing.value = op.value;
      existing.anchored = true;
    } else {
      this.nodes.set(op.id, {
        id: op.id,
        pred: op.pred,
        value: op.value,
        deleted: false,
        anchored: true,
      });
    }
    this.drain(op.id); // 新锚点出现，级联挂载暂存的后继
  }

  private applyDelete(op: DeleteOp): void {
    const existing = this.nodes.get(op.id);
    if (existing) {
      existing.deleted = true; // 墓碑保留，继续锚定后继
      return;
    }
    // delete 先于 insert 到达：建立占位墓碑
    this.nodes.set(op.id, {
      id: op.id,
      pred: null,
      value: "",
      deleted: true,
      anchored: false,
    });
    this.drain(op.id); // 占位墓碑同样可作为暂存后继的锚点
  }

  private buffer(op: InsertOp): void {
    const key = op.pred as Id;
    const list = this.pending.get(key) ?? [];
    if (!list.some((o) => o.id === op.id)) list.push(op); // 暂存区去重
    this.pending.set(key, list);
  }

  private drain(pred: Id): void {
    const list = this.pending.get(pred);
    if (!list) return;
    this.pending.delete(pred);
    for (const op of list) this.applyInsert(op); // 可能级联唤醒更多暂存操作
  }

  /** 可见序列：深度优先遍历，墓碑不输出但其子树照常展开 */
  toArray(): Array<{ id: Id; value: string }> {
    const children = new Map<Id | null, Node[]>();
    for (const node of this.nodes.values()) {
      if (!node.anchored) continue; // 占位墓碑尚未定位，整棵子树随之浮动
      const key = node.pred;
      const list = children.get(key) ?? [];
      list.push(node);
      children.set(key, list);
    }
    for (const list of children.values()) {
      list.sort((x, y) => (x.id < y.id ? -1 : x.id > y.id ? 1 : 0));
    }

    const out: Array<{ id: Id; value: string }> = [];
    const visit = (node: Node): void => {
      if (!node.deleted) out.push({ id: node.id, value: node.value });
      for (const child of children.get(node.id) ?? []) visit(child);
    };
    for (const root of children.get(null) ?? []) visit(root);
    return out;
  }

  diagnostics(): Diagnostics {
    const pendingInserts: InsertOp[] = [];
    const missing = new Set<Id>();
    for (const [pred, ops] of this.pending) {
      missing.add(pred);
      pendingInserts.push(...ops);
    }
    for (const node of this.nodes.values()) {
      if (!node.anchored) missing.add(node.id);
    }
    pendingInserts.sort((x, y) => (x.id < y.id ? -1 : x.id > y.id ? 1 : 0));
    return {
      pendingInserts,
      missingDependencies: [...missing].sort(),
    };
  }
}
