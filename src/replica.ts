/**
 * Replica：一个协作者副本。
 *  - 维护操作 DAG（nodes + heads）与 RGA 文档；
 *  - 本地操作 / Undo / Redo 都会向 DAG 追加节点并广播；
 *  - Undo/Redo 通过生成反向补偿节点实现，历史只增不改。
 */
import { RgaDoc, makeCharId } from './rga.ts';
import type {
  CharId,
  DeletePayload,
  InsertPayload,
  NodeId,
  OpNode,
  RestorePayload,
  SiteId,
} from './types.ts';

export class Replica {
  readonly site: SiteId;
  readonly doc = new RgaDoc();

  /** 操作 DAG：所有已知节点 */
  readonly nodes = new Map<NodeId, OpNode>();
  /** DAG 当前头部（无出边的节点集合） */
  private heads = new Set<NodeId>();
  /** 因果缓冲：父节点尚未到齐的远程节点 */
  private pending: OpNode[] = [];

  private counter = 0;

  /** 本站可撤销栈：自己发起的原始操作节点 */
  private undoStack: NodeId[] = [];
  /** 本站可重做栈：自己发起的补偿节点 */
  private redoStack: NodeId[] = [];

  /**  outgoing 广播队列（测试/样例中由外层投递给其他副本） */
  private outbox: OpNode[] = [];

  constructor(site: SiteId) {
    this.site = site;
  }

  private nextCounter(): number {
    return ++this.counter;
  }

  /** ---------------- 本地操作 ---------------- */

  /** 在 after 之后插入文本，返回新节点 */
  insertText(after: CharId | null, text: string): OpNode {
    const base = this.nextCounter();
    const node = this.makeNode('insert', null, {
      text,
      after,
      base,
    } satisfies InsertPayload);
    // 为整段文本预留 counter 区间，保证字符 id 唯一
    this.counter += text.length - 1;
    this.commit(node);
    this.undoStack.push(node.id);
    return node;
  }

  /** 删除指定字符（墓碑），返回新节点 */
  deleteChars(targets: CharId[]): OpNode {
    const node = this.makeNode('delete', null, { targets } satisfies DeletePayload);
    this.commit(node);
    this.undoStack.push(node.id);
    return node;
  }

  /** 撤销自己最近一个未撤销的操作：追加反向补偿节点 */
  undo(): OpNode | null {
    const targetId = this.undoStack.pop();
    if (targetId === undefined) return null;
    const target = this.nodes.get(targetId)!;
    const comp = this.makeNode(
      inverseKind(target.kind),
      target.id,
      inversePayload(target),
    );
    this.commit(comp);
    this.redoStack.push(comp.id);
    return comp;
  }

  /** 重做：对最近一个补偿节点再取逆，无缝恢复原操作效果 */
  redo(): OpNode | null {
    const compId = this.redoStack.pop();
    if (compId === undefined) return null;
    const comp = this.nodes.get(compId)!;
    const node = this.makeNode(
      inverseKind(comp.kind),
      comp.id,
      inversePayload(comp),
    );
    this.commit(node);
    // 重做后该操作重新生效，可再次撤销
    this.undoStack.push(node.id);
    return node;
  }

  /** ---------------- 协同 ---------------- */

  /** 取出待广播节点并清空队列 */
  drainOutbox(): OpNode[] {
    const out = this.outbox;
    this.outbox = [];
    return out;
  }

  /** 接收远程节点；因果未就绪时进入缓冲，就绪后按拓扑序集成 */
  receive(node: OpNode): void {
    if (this.nodes.has(node.id)) return;
    this.pending.push(node);
    let progressed = true;
    while (progressed) {
      progressed = false;
      for (let i = 0; i < this.pending.length; i++) {
        const n = this.pending[i];
        if (n.parents.every((p) => this.nodes.has(p))) {
          this.pending.splice(i, 1);
          this.integrate(n);
          progressed = true;
          break;
        }
      }
    }
  }

  /** ---------------- 内部 ---------------- */

  private makeNode(
    kind: OpNode['kind'],
    compensates: NodeId | null,
    payload: OpNode['payload'],
  ): OpNode {
    return {
      id: `node:${this.site}:${this.nextCounter()}`,
      author: this.site,
      kind,
      parents: [...this.heads],
      compensates,
      payload,
    };
  }

  /** 本地提交：集成 + 广播 */
  private commit(node: OpNode): void {
    this.integrate(node);
    this.outbox.push(node);
  }

  /** 将节点集成进 DAG 与文档（调用方需保证因果就绪、且未集成过） */
  private integrate(node: OpNode): void {
    this.nodes.set(node.id, node);
    for (const p of node.parents) this.heads.delete(p);
    this.heads.add(node.id);

    if (node.kind === 'insert') {
      const { text, base } = node.payload as InsertPayload;
      let after = (node.payload as InsertPayload).after;
      for (let i = 0; i < text.length; i++) {
        this.doc.integrateInsert(makeCharId(node.author, base + i), text[i], after);
        after = makeCharId(node.author, base + i);
      }
    } else if (node.kind === 'delete') {
      for (const t of (node.payload as DeletePayload).targets) this.doc.applyDelete(t);
    } else {
      for (const t of (node.payload as RestorePayload).targets) this.doc.applyRestore(t);
    }
  }
}

/** 反向操作种类：insert↔delete，delete↔restore，restore↔delete */
function inverseKind(kind: OpNode['kind']): OpNode['kind'] {
  switch (kind) {
    case 'insert':
      return 'delete';
    case 'delete':
      return 'restore';
    case 'restore':
      return 'delete';
  }
}

/** 反向负载：insert 的逆是删除其全部字符；delete/restore 的逆是同一目标集合 */
function inversePayload(node: OpNode): OpNode['payload'] {
  if (node.kind === 'insert') {
    const { text, base } = node.payload as InsertPayload;
    const targets: CharId[] = [];
    for (let i = 0; i < text.length; i++) targets.push(makeCharId(node.author, base + i));
    return { targets } satisfies DeletePayload;
  }
  const { targets } = node.payload as DeletePayload | RestorePayload;
  return { targets: [...targets] };
}
